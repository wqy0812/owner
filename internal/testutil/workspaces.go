// Package testutil builds disposable current-contract fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

const Playbook = "---\n- name: Fixture check\n  assert:\n    that: true\n"

// Workspaces installs synthetic Playbooks for fixture Releases. Call only before
// creating test evidence: the workspace is part of the Release contract digest.
func Workspaces(t testing.TB, db *store.Store, root string, releaseIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if len(releaseIDs) == 0 {
		rows, err := db.DB().Query("SELECT id FROM component_releases ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			releaseIDs = append(releaseIDs, id)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range releaseIDs {
		r, err := db.GetComponentRelease(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Actions) == 0 {
			continue
		}
		r.Actions = BoundFixtureActions(r.ID, r.Actions)
		for _, a := range r.Actions {
			_, err := db.DB().Exec(`INSERT INTO action_definitions(id,release_id,name,kind,playbook,host_group,timeout_seconds,risk_level,destructive,idempotent,tags_json,required_credentials_json,pre_check_action_id,post_check_action_id,resource_contract_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,pre_check_action_id=excluded.pre_check_action_id,post_check_action_id=excluded.post_check_action_id,resource_contract_json=excluded.resource_contract_json`, a.ID, r.ID, a.Name, a.Kind, a.Playbook, a.HostGroup, a.TimeoutSeconds, a.RiskLevel, a.Destructive, a.Idempotent, "[]", "[]", a.PreCheckActionID, a.PostCheckActionID, resourceFixtureJSON(a.ResourceContract))
			if err != nil {
				t.Fatal(err)
			}
		}
		component, err := db.GetComponent(context.Background(), r.ComponentID, false)
		if err != nil {
			t.Fatal(err)
		}
		prefix := domain.GeneratedComponentWorkspaceRoot(component, r)
		if r.PlaybookWorkspaceRoot != "" {
			prefix = r.PlaybookWorkspaceRoot
		}
		dir := filepath.Join(root, filepath.FromSlash(prefix))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		files := []domain.ComponentPlaybookFile{}
		for _, a := range r.Actions {
			path, err := domain.ActionTaskPath(a)
			if err != nil {
				t.Fatal(err)
			}
			relative := prefix + path
			content := []byte(Playbook)
			if existing, e := os.ReadFile(filepath.Join(dir, path)); e == nil {
				content = existing
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, path), content, 0600); err != nil {
				t.Fatal(err)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(content))
			files = append(files, domain.ComponentPlaybookFile{ReleaseID: id, Path: path, SHA256: sum, SizeBytes: int64(len(content)), MediaType: "application/yaml", UpdatedAt: time.Now().UTC()})
			if _, err := db.DB().Exec("UPDATE action_definitions SET playbook=?,playbook_sha256=? WHERE id=?", relative, sum, a.ID); err != nil {
				t.Fatal(err)
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		var tree strings.Builder
		if _, err := db.DB().Exec("DELETE FROM component_playbook_files WHERE release_id=?", id); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			tree.WriteString(f.Path + "\x00" + f.SHA256 + "\x00")
			if _, err := db.DB().Exec("INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES(?,?,?,?,?,?)", id, f.Path, f.SHA256, f.SizeBytes, f.MediaType, f.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.DB().Exec("UPDATE component_releases SET playbook_workspace_root=?,playbook_tree_sha256=? WHERE id=?", prefix, fmt.Sprintf("%x", sha256.Sum256([]byte(tree.String()))), id); err != nil {
			t.Fatal(err)
		}
	}
}

// BoundFixtureActions creates current-contract actions for isolated unit fixtures.
// It is never called by production code or against a deployed database.
func BoundFixtureActions(releaseID string, input []domain.ActionDefinition) []domain.ActionDefinition {
	actions := append([]domain.ActionDefinition(nil), input...)
	group := "all"
	for _, a := range actions {
		if a.HostGroup != "" {
			group = a.HostGroup
			break
		}
	}
	for i := range actions {
		if actions[i].Kind == domain.ActionVerify || actions[i].Kind == domain.ActionPreflight || actions[i].Kind == domain.ActionInspect {
			actions[i].Kind = domain.ActionCheck
			actions[i].Destructive = false
			actions[i].Idempotent = false
		}
	}
	for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionRollback} {
		found := false
		for _, a := range actions {
			if a.Kind == kind {
				found = true
			}
		}
		if !found {
			actions = append(actions, domain.ActionDefinition{ID: releaseID + "-" + string(kind), ReleaseID: releaseID, Name: string(kind), Kind: kind, Playbook: "fixture", HostGroup: group, TimeoutSeconds: 60, RiskLevel: domain.RiskLow, Idempotent: true})
		}
	}
	original := len(actions)
	for i := 0; i < original; i++ {
		a := &actions[i]
		if a.Kind == domain.ActionCheck {
			continue
		}
		for _, phase := range []string{"pre", "post"} {
			id := a.ID + "-" + phase
			if phase == "pre" && a.PreCheckActionID != "" {
				continue
			}
			if phase == "post" && a.PostCheckActionID != "" {
				continue
			}
			if phase == "pre" {
				a.PreCheckActionID = id
			} else {
				a.PostCheckActionID = id
			}
			actions = append(actions, domain.ActionDefinition{ID: id, ReleaseID: releaseID, Name: string(a.Kind) + " " + phase, Kind: domain.ActionCheck, Playbook: "fixture", HostGroup: group, TimeoutSeconds: 60, RiskLevel: domain.RiskLow})
			a = &actions[i]
		}
	}
	for i := range actions {
		if actions[i].ResourceContract == nil {
			actions[i].ResourceContract = &domain.ResourceContract{Version: 1, NoManagedPaths: true, Claims: []domain.ResourceClaim{}}
		}
	}
	return actions
}

func resourceFixtureJSON(contract *domain.ResourceContract) string {
	data, _ := json.Marshal(contract)
	return string(data)
}
