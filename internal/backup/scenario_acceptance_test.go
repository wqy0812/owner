package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestScenarioAcceptanceCatalogRoundTripAndTamper(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	playbooks := filepath.Join(root, "playbooks")
	exported := filepath.Join(root, "export")
	if err := os.MkdirAll(exported, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	for _, user := range []domain.User{{ID: "owner", Name: "Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}, {ID: "admin", Name: "Admin", Role: domain.RolePlatformAdmin, CreatedAt: now}} {
		if err = db.UpsertUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO platform_option_categories(id,technical_key,label,category_type,created_by,created_at) VALUES('group','hostGroup','Host group','host_group','admin','2026-09-05')`,
		`INSERT INTO platform_options(id,category_id,technical_value,label,created_by,created_at) VALUES('worker','group','worker','Worker','admin','2026-09-05')`,
	} {
		if _, err = db.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	revision := domain.ScenarioRevision{ID: "source-r1", ScenarioID: "source", Revision: 1, Status: domain.RevisionReleased, DigestVersion: domain.ScenarioDigestVersion, CreatedAt: now, ReleasedAt: &now, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}, AcceptanceWorkspaceRoot: "managed-scenarios/source/source-r1/"}
	files := []Playbook{}
	for relative, data := range map[string]string{"tasks/acceptance/business.yml": "- name: Assert business\n  ansible.builtin.assert:\n    that: true\n", "templates/check.j2": "business {{ endpoint }}"} {
		path := filepath.Join(playbooks, filepath.FromSlash(revision.AcceptanceWorkspaceRoot+relative))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(data))
		files = append(files, Playbook{Path: revision.AcceptanceWorkspaceRoot + relative, SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))})
		if relative == "tasks/acceptance/business.yml" {
			revision.AcceptanceJobs = []domain.ScenarioAcceptanceJob{{ID: "business", Name: "Business", Purpose: "business state", HostGroup: "worker", Playbook: revision.AcceptanceWorkspaceRoot + relative, PlaybookSHA256: hex.EncodeToString(sum[:])}}
		}
	}
	revision.AcceptanceTreeSHA256 = acceptanceManifestDigest(files, revision.AcceptanceWorkspaceRoot)
	sc := domain.Scenario{ID: "source", Slug: "source", Name: "Source", OwnerID: "owner", CreatedAt: now, UpdatedAt: now}
	if err = db.CreateScenario(ctx, sc, revision); err != nil {
		t.Fatal(err)
	}
	branch := domain.Scenario{ID: "branch", Slug: "branch", Name: "Branch", OwnerID: "owner", CreatedAt: now, UpdatedAt: now, ForkedFromScenarioID: sc.ID, ForkedFromRevisionID: revision.ID, ForkedFromDigest: domain.ScenarioRevisionSpecDigest(revision)}
	branchRevision := domain.ScenarioRevision{ID: "branch-r1", ScenarioID: branch.ID, Revision: 1, Status: domain.RevisionReleased, CreatedAt: now, ReleasedAt: &now, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}}
	if err = db.CreateScenario(ctx, branch, branchRevision); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := ExportCatalog(ctx, source, playbooks, exported)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Playbooks) != 2 {
		t.Fatalf("acceptance files omitted: %+v", catalog.Playbooks)
	}
	if err = validateCatalogReferences(catalog); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, filepath.Join(root, "restored.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restoreTables(ctx, restored.DB(), catalog); err != nil {
		t.Fatal(err)
	}
	got, err := restored.GetScenario(ctx, branch.ID, false)
	if err != nil || got.ForkedFromDigest != branch.ForkedFromDigest || got.ForkedFromRevisionID != revision.ID {
		t.Fatalf("branch lineage lost: %+v %v", got, err)
	}
	gotRevision, err := restored.GetScenarioRevision(ctx, revision.ID)
	if err != nil || domain.ScenarioRevisionSpecDigest(gotRevision) != domain.ScenarioRevisionSpecDigest(revision) {
		t.Fatalf("acceptance definition lost: %+v %v", gotRevision, err)
	}
	live, err := store.Open(ctx, filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	tx, err := live.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = restoreUsers(ctx, tx, catalog); err != nil {
		t.Fatal(err)
	}
	if err = restorePlatformOptionCatalog(ctx, tx, catalog); err != nil {
		t.Fatal(err)
	}
	if err = restoreDefinitionTables(ctx, tx, catalog, 1); err != nil {
		t.Fatal(err)
	}
	if err = verifyForeignKeysTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	liveBranch, err := live.GetScenario(ctx, branch.ID, false)
	if err != nil || liveBranch.ForkedFromRevisionID != revision.ID {
		t.Fatalf("live restore lost branch: %+v %v", liveBranch, err)
	}
	corrupted := cloneRelationshipCatalog(t, catalog)
	corrupted.Playbooks = corrupted.Playbooks[:1]
	if err = validateCatalogReferences(corrupted); err == nil {
		t.Fatal("missing acceptance auxiliary file accepted")
	}
	path := filepath.Join(playbooks, filepath.FromSlash(revision.AcceptanceWorkspaceRoot+"templates/check.j2"))
	if err = os.WriteFile(path, []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ExportCatalog(ctx, source, playbooks, exported); err == nil {
		t.Fatal("workspace drift accepted")
	}
}
