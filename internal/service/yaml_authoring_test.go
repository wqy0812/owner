package service

import (
	"context"
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
		_, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, domain.ActionDefinition{ID: "", Name: name, Kind: domain.ActionCheck, HostGroup: "all"}, []byte("- assert:\n    that: true\n"), nil, nil)
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
		action := domain.ActionDefinition{Name: string(kind), Kind: kind, HostGroup: "all"}
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

func TestRollbackCheckSourceKeepsRepeatedComponentNodesSeparate(t *testing.T) {
	p, _, _, release, _ := yamlActionFixture(t)
	install, _ := findAction(release, domain.ActionInstall)
	rollback, _ := findAction(release, domain.ActionRollback)
	steps := []lockedStep{
		{NodeID: "first", ComponentID: release.ComponentID, ReleaseID: release.ID, ActionID: install.ID, Action: domain.ActionInstall, Variables: map[string]any{"value": "first-input"}},
		{NodeID: "second", ComponentID: release.ComponentID, ReleaseID: release.ID, ActionID: install.ID, Action: domain.ActionInstall, Variables: map[string]any{"value": "second-input"}},
		{NodeID: "first-rollback", SourceNodeID: "first", ComponentID: release.ComponentID, ReleaseID: release.ID, ActionID: rollback.ID, Action: domain.ActionRollback},
	}
	if err := p.rollback.bindRollbackCheckSources(context.Background(), "unused", steps); err != nil {
		t.Fatal(err)
	}
	if got := steps[2]; got.RollbackSourceActionID != install.ID || got.RollbackSourceVariables["value"] != "first-input" {
		t.Fatalf("rollback borrowed another node's source: %+v", got)
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
