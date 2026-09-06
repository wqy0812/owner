package service

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

func scenarioPlannerRelease(t *testing.T, p *Platform, id, componentID, parent string, kinds ...domain.ActionKind) domain.ComponentRelease {
	t.Helper()
	ctx := context.Background()
	if componentID != "component-1" {
		component, err := testDatabase(p).GetComponent(ctx, "component-1", false)
		if err != nil {
			t.Fatal(err)
		}
		component.ID, component.Name, component.Slug = componentID, componentID, componentID
		if err := testDatabase(p).CreateComponent(ctx, component); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	release := domain.ComponentRelease{ID: id, ComponentID: componentID, Version: id, LineID: componentID + "-line", LineName: "Line", Status: domain.ReleaseReleased, ReleasedAt: &now, ParentReleaseID: parent, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now}
	for _, kind := range kinds {
		a := domain.ActionDefinition{ID: id + "-" + string(kind), Kind: kind, Name: string(kind), HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, Idempotent: kind == domain.ActionInstall}
		if kind == domain.ActionUpgrade {
			a.FromReleaseID, a.ToReleaseID = parent, id
		}
		if kind == domain.ActionRollback && parent != "" {
			a.FromReleaseID, a.ToReleaseID = id, parent
		}
		release.Actions = append(release.Actions, a)
	}
	if err := testDatabase(p).CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, testDatabase(p), p.catalog.workspace.root, id)
	release, err := testDatabase(p).GetComponentRelease(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func TestScenarioUpgradePlanDependencyMigrationAndOldParameterUninstall(t *testing.T) {
	p, _, _, _, _, _ := scenarioExecutionFixture(t)
	a := scenarioPlannerRelease(t, p, "old-service", "old-component", "", domain.ActionInstall, domain.ActionUninstall)
	b := scenarioPlannerRelease(t, p, "new-service", "new-component", "", domain.ActionInstall)
	c1 := scenarioPlannerRelease(t, p, "consumer-v1", "component-1", "", domain.ActionInstall)
	c2 := scenarioPlannerRelease(t, p, "consumer-v2", "component-1", c1.ID, domain.ActionInstall, domain.ActionUpgrade)
	source := domain.ScenarioRevision{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "a", ReleaseID: a.ID}, {ID: "c", ReleaseID: c1.ID}}, Edges: []domain.ScenarioEdge{{Source: "a", Target: "c"}}}}
	target := domain.ScenarioRevision{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "b", ReleaseID: b.ID}, {ID: "c", ReleaseID: c2.ID}}, Edges: []domain.ScenarioEdge{{Source: "b", Target: "c"}}}}
	old := []scenarioTargetNode{{NodeID: "a", ComponentID: a.ComponentID, ReleaseID: a.ID, Variables: map[string]any{"endpoint": "actual-old-environment"}}, {NodeID: "c", ComponentID: c1.ComponentID, ReleaseID: c1.ID}}
	next := []scenarioTargetNode{{NodeID: "b", ComponentID: b.ComponentID, ReleaseID: b.ID}, {NodeID: "c", ComponentID: c2.ComponentID, ReleaseID: c2.ID, Variables: map[string]any{"endpoint": "mapped-new-environment"}}}
	steps, ops, err := p.planner.scenarioUpgradeSteps(context.Background(), source, target, old, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].SourceNodeID != "b" || steps[1].SourceNodeID != "c" || steps[2].SourceNodeID != "a" {
		t.Fatalf("unsafe migration order: %+v", steps)
	}
	if steps[1].Action != domain.ActionUpgrade || steps[1].Variables["endpoint"] != "mapped-new-environment" || steps[2].Action != domain.ActionUninstall || steps[2].Variables["endpoint"] != "actual-old-environment" || !steps[2].NeedsApproval {
		t.Fatalf("incorrect actions or frozen parameters: %+v", steps)
	}
	if len(ops) != 3 {
		t.Fatalf("operations=%+v", ops)
	}
}

func TestScenarioUpgradePlanEffectiveParametersAndUnchangedNodes(t *testing.T) {
	p, _, _, _, _, _ := scenarioExecutionFixture(t)
	r := scenarioPlannerRelease(t, p, "configured", "component-1", "", domain.ActionInstall, domain.ActionConfigure)
	revision := domain.ScenarioRevision{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: r.ID}}}}
	old := []scenarioTargetNode{{NodeID: "node", ComponentID: r.ComponentID, ReleaseID: r.ID, Variables: map[string]any{"mapped": "old"}}}
	next := []scenarioTargetNode{{NodeID: "node", ComponentID: r.ComponentID, ReleaseID: r.ID, Variables: map[string]any{"mapped": "new"}}}
	steps, _, err := p.planner.scenarioUpgradeSteps(context.Background(), revision, revision, old, next)
	if err != nil || len(steps) != 1 || steps[0].Action != domain.ActionConfigure || steps[0].Variables["mapped"] != "new" {
		t.Fatalf("configure=%+v err=%v", steps, err)
	}
	steps, ops, err := p.planner.scenarioUpgradeSteps(context.Background(), revision, revision, old, old)
	if err != nil || len(steps) != 0 || len(ops) != 1 || ops[0].Change != "unchanged" {
		t.Fatalf("unchanged reinstalled: %+v %+v %v", steps, ops, err)
	}
}

func TestScenarioUpgradePlanIdempotentReuseRequiresExactEvolutionContract(t *testing.T) {
	for _, variant := range []string{"valid", "wrong-parent", "wrong-line", "no-reverse", "non-idempotent", "wrong-upgrade"} {
		t.Run(variant, func(t *testing.T) {
			p, db, _, _, _, _ := scenarioExecutionFixture(t)
			old := scenarioPlannerRelease(t, p, "reuse-v1", "component-1", "", domain.ActionInstall)
			next := scenarioPlannerRelease(t, p, "reuse-v2", "component-1", old.ID, domain.ActionInstall, domain.ActionRollback)
			ctx := context.Background()
			var err error
			switch variant {
			case "wrong-parent":
				_, err = db.DB().Exec("UPDATE component_releases SET parent_release_id=NULL WHERE id=?", next.ID)
			case "wrong-line":
				_, err = db.DB().Exec("UPDATE component_releases SET line_id='scenario-line' WHERE id=?", next.ID)
			case "no-reverse":
				_, err = db.DB().Exec("UPDATE action_definitions SET to_release_id=NULL WHERE release_id=? AND kind='rollback'", next.ID)
			case "non-idempotent":
				_, err = db.DB().Exec("UPDATE action_definitions SET idempotent=0 WHERE release_id=? AND kind='install'", next.ID)
			case "wrong-upgrade":
				_, err = db.DB().Exec("UPDATE action_definitions SET kind='upgrade',from_release_id=NULL,to_release_id=NULL WHERE release_id=? AND kind='rollback'", next.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			source := domain.ScenarioRevision{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: old.ID}}}}
			target := domain.ScenarioRevision{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: next.ID}}}}
			steps, ops, err := p.planner.scenarioUpgradeSteps(ctx, source, target, []scenarioTargetNode{{NodeID: "node", ComponentID: old.ComponentID, ReleaseID: old.ID}}, []scenarioTargetNode{{NodeID: "node", ComponentID: next.ComponentID, ReleaseID: next.ID}})
			if variant != "valid" {
				if err == nil {
					t.Fatal("inexact evolution accepted")
				}
				return
			}
			if err != nil || len(steps) != 1 || steps[0].Action != domain.ActionInstall || steps[0].FromReleaseID != old.ID || steps[0].ToReleaseID != next.ID || ops[0].Change != "upgrade" {
				t.Fatalf("reuse=%+v ops=%+v err=%v", steps, ops, err)
			}
		})
	}
}
