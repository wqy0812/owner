package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func yamlActionFixture(t *testing.T) (*Platform, *store.Store, domain.User, domain.ComponentRelease, string) {
	t.Helper()
	p, db, root := componentImportRecoveryPlatform(t)
	ctx, now := context.Background(), time.Now().UTC()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	seedAtomicActionHostGroup(t, ctx, db, now)
	component := domain.Component{ID: "yaml-component", Slug: "yaml-component", Name: "YAML component", Layer: domain.LayerRuntimeState, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "yaml-release", ComponentID: component.ID, LineID: "yaml-line", LineName: "Stable", Version: "1", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now}
	if err := db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"before", "after"} {
		_, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, domain.ActionDefinition{ID: "", Name: name, Kind: domain.ActionCheck, HostGroup: "all", ResourceContract: &domain.ResourceContract{Version: 1, NoManagedPaths: true}}, []byte("- assert:\n    that: true\n"), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	checks := map[string]string{}
	for _, action := range r.Actions {
		checks[action.Name] = action.ID
	}
	for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionRollback} {
		action := domain.ActionDefinition{Name: string(kind), Kind: kind, HostGroup: "all", ResourceContract: &domain.ResourceContract{Version: 1, NoManagedPaths: true}}
		if kind == domain.ActionInstall {
			action.PreCheckActionID, action.PostCheckActionID = checks["before"], checks["after"]
		}
		if _, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, action, []byte("- assert:\n    that: true\n"), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	return p, db, owner, r, root
}

func TestActionYAMLMigrationRequiresAtomicSourceConfirmation(t *testing.T) {
	p, db, owner, r, root := yamlActionFixture(t)
	ctx := context.Background()
	action, _ := findAction(r, domain.ActionInstall)
	oldContract := *action.ResourceContract
	oldContract.Checks = []domain.RuntimeCheck{{ID: "tool", Kind: "command", Target: "sh"}}
	encoded, _ := json.Marshal(oldContract)
	if _, err := db.DB().Exec(`UPDATE action_definitions SET gather_facts=1,resource_contract_json=? WHERE id=?`, string(encoded), action.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	oldDigest := domain.ComponentReleaseSpecDigest(r)
	if err := domain.ValidateReleaseYAMLAuthoring(r); err == nil {
		t.Fatal("legacy authoring accepted")
	}
	content := []byte("- setup:\n- assert:\n    that: ansible_facts.system is defined\n")
	saved, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, action, content, nil, nil)
	if err != nil || !saved.Action.NeedsYAMLMigration() {
		t.Fatalf("ordinary save lost legacy fields: %+v %v", saved, err)
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	sha, tree := saved.SHA256, r.PlaybookTreeSHA256
	if _, err := db.DB().Exec(`CREATE TRIGGER fail_yaml_migration BEFORE UPDATE ON action_definitions BEGIN SELECT RAISE(ABORT, 'migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, action, []byte("- setup:\n"), &sha, &tree, true); err == nil {
		t.Fatal("failed transaction accepted")
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	preserved, _ := r.ActionByID(action.ID)
	bytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(preserved.Playbook)))
	if err != nil || string(bytes) != string(content) || !preserved.NeedsYAMLMigration() {
		t.Fatalf("failed save lost old source/settings: %v", err)
	}
	if _, err := db.DB().Exec(`DROP TRIGGER fail_yaml_migration`); err != nil {
		t.Fatal(err)
	}
	saved, err = p.catalog.SaveActionAtomic(ctx, owner, r.ID, action, content, &sha, &tree, true)
	if err != nil || saved.Action.NeedsYAMLMigration() {
		t.Fatalf("confirmed migration failed: %v", err)
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	if domain.ComponentReleaseSpecDigest(r) == oldDigest {
		t.Fatal("migration reused evidence identity")
	}
}

func TestRollbackDefaultChecksUseTheSourceAndAcceptTwoStepEvidence(t *testing.T) {
	p, db, _, r, _ := yamlActionFixture(t)
	component, err := db.GetComponent(context.Background(), r.ComponentID, false)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := findAction(r, domain.ActionInstall)
	rollback, _ := findAction(r, domain.ActionRollback)
	body, err := p.actions.lockAction(component, "node", r, rollback, map[string]any{"value": "current"})
	if err != nil {
		t.Fatal(err)
	}
	body.RollbackSourceActionID, body.RollbackSourceVariables, body.RollbackSourceFrozen = install.ID, map[string]any{"value": "original"}, true
	steps, err := p.actions.expandActionSteps(context.Background(), []lockedStep{body})
	if err != nil || len(steps) != 2 {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	if steps[0].Phase != "execute" || steps[1].Phase != "post" || steps[1].ActionID != install.PreCheckActionID {
		t.Fatal("default check did not use install precheck")
	}
	syncActionCheckContext(steps)
	if steps[1].Variables["value"] != "original" {
		t.Fatal("rollback overwrote frozen source inputs")
	}
	if componentTestEvidence(domain.ActionRollback, steps) != "rollback_verify" {
		t.Fatal("two-step rollback evidence rejected")
	}
	if componentTestEvidence(domain.ActionRollback, steps[:1]) != "incomplete" {
		t.Fatal("rollback without postcheck accepted")
	}
	body.RollbackSourceActionID = ""
	if _, err := p.actions.expandActionSteps(context.Background(), []lockedStep{body}); err == nil {
		t.Fatal("ambiguous source accepted")
	}
}

func TestFrozenRecoveryChecksKeepSourceInputsAndRestoreContext(t *testing.T) {
	p, db, _, release, root := yamlActionFixture(t)
	setTestRunner(t, p, &scenarioProtocolRunner{root: root})
	ctx := context.Background()
	component, err := db.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := findAction(release, domain.ActionInstall)
	main, err := p.actions.lockAction(component, "instance", release, install, map[string]any{"source": "original", "clusterforge_backup_operation": "capture"})
	if err != nil {
		t.Fatal(err)
	}
	main.SourceNodeID, main.Phase = "instance", "execute"
	main.BackupRef = "/var/lib/clusterforge/backups/fixture"
	main.Backup = &domain.BackupMetadata{NodeID: "instance", ActionID: install.ID}
	job := ansible.JobPlan{}
	if err := p.rollback.addRecoverySteps(ctx, &job, lockedPlan{ParentSteps: []lockedStep{main}}); err != nil {
		t.Fatal(err)
	}
	if len(job.Recovery) != 2 || job.Recovery[1].ActionID != install.PreCheckActionID {
		t.Fatalf("unexpected recovery: %+v", job.Recovery)
	}
	if job.Recovery[1].Variables["source"] != "original" || job.Recovery[1].Variables["clusterforge_backup_operation"] != "restore" {
		t.Fatalf("lost source or restore context: %+v", job.Recovery[1].Variables)
	}
}

func TestAcceptanceYAMLMigrationPreservesLegacyUntilSourceSave(t *testing.T) {
	p, db, owner, r, _ := scenarioAcceptanceFixture(t)
	ctx, path := context.Background(), "tasks/acceptance/business.yml"
	if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, r.ID, path, []byte("- assert:\n    that: true\n"), acceptanceExpectation(t, p, owner, r.ID, path)); err != nil {
		t.Fatal(err)
	}
	r, _ = db.GetScenarioRevision(ctx, r.ID)
	digest := domain.ScenarioRevisionSpecDigest(r)
	r.AcceptanceJobs[0].GatherFacts = true
	r.AcceptanceJobs[0].RuntimeChecks = []domain.RuntimeCheck{{ID: "tool", Kind: "command", Target: "sh"}}
	if err := db.SaveScenarioRevisionDefinition(ctx, r, digest); err != nil {
		t.Fatal(err)
	}
	definition, _ := p.scenarios.ReadAcceptance(ctx, owner, r.ID)
	job := definition.Jobs[0]
	job.GatherFacts, job.RuntimeChecks = false, nil
	definition, err := p.scenarios.SaveAcceptance(ctx, owner, r.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: definition.RevisionDigest, Jobs: []domain.ScenarioAcceptanceJob{job}})
	if err != nil || !definition.Jobs[0].NeedsYAMLMigration() {
		t.Fatalf("ordinary save erased migration: %v", err)
	}
	expected := acceptanceExpectation(t, p, owner, r.ID, path)
	expected.ConfirmYAMLMigration = true
	if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, r.ID, path, []byte("- setup:\n- assert:\n    that: ansible_facts.system is defined\n"), expected); err != nil {
		t.Fatal(err)
	}
	definition, _ = p.scenarios.ReadAcceptance(ctx, owner, r.ID)
	if definition.Jobs[0].NeedsYAMLMigration() {
		t.Fatal("source confirmation did not clear settings")
	}
}
