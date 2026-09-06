package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

// This fixture is opt-in and only writes a new temporary directory. It never
// connects to the test cluster or produces real execution evidence.
func TestRunLogsBrowserFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_UX_BROWSER_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_UX_BROWSER_FIXTURE for isolated browser QA")
	}
	if !filepath.IsAbs(destination) {
		t.Fatal("fixture destination must be absolute")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	_, db, owner, revision, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// A historical version is display-only fixture data for checking that
	// adaptation fields stay disabled while switching revisions.
	if _, err := db.DB().ExecContext(ctx, `UPDATE scenario_revisions SET revision=2 WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `INSERT INTO scenario_revisions(id,scenario_id,revision,status,environment_constraints_json,graph_json,created_at,released_at)
		SELECT 'browser-historical-revision',scenario_id,1,'released','{"architecture":["amd64"]}',graph_json,created_at,created_at FROM scenario_revisions WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	params := []domain.ParameterDefinition{
		{Name: "pki_directory_with_a_long_parameter_name_for_desktop_layout", Description: "集群证书目录，用于检查长参数名、多条引用与完整参数说明的展开布局", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "/etc/kubernetes/pki"},
		{Name: "api_options", Description: "API 配置选项", Type: domain.ParameterTypeObject, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: map[string]any{"server": "https://example.test:6443", "enabled": true}},
		{Name: "internal_value", Description: "仅用于组件内部", Type: domain.ParameterTypeBoolean, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: false},
	}
	raw, _ := json.Marshal(params)
	if _, err := db.DB().ExecContext(ctx, `UPDATE component_releases SET parameters_json=?,environment_constraints_json='{"architecture":["amd64"]}' WHERE id='scenario-release'`, string(raw)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("consumer-%d", i)
		if err := db.CreateComponent(ctx, domain.Component{ID: id, Name: fmt.Sprintf("下游组件 %d", i+1), Slug: id, OwnerID: "component-owner", Layer: domain.LayerRuntimeState, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		release := domain.ComponentRelease{ID: id + "-release", ComponentID: id, Version: "1.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, Dependencies: []domain.ComponentDependency{{ID: id + "-dependency", ReleaseID: id + "-release", UpstreamComponentID: "component-1", UpstreamReleaseID: "scenario-release", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: params[0].Name, TargetParameter: "pki_dir"}}}}}
		if err := db.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	run := domain.Run{ID: "run-browser-diagnostics", Kind: domain.RunScenario, Status: domain.RunFailed, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, ScenarioRevisionID: revision.ID, CreatedAt: now, FinishedAt: &now, InputSnapshot: map[string]any{"steps": []any{map[string]any{"nodeId": "locked-check", "componentName": "CoreDNS", "phase": "post"}}}}
	if err := db.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRunStep(ctx, domain.RunStep{ID: "step-failed", RunID: run.ID, NodeID: "locked-check", Name: "CoreDNS check", Status: domain.RunFailed, StartedAt: &now, FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	appendDiagnosticLog(t, db, run, "event", diagnosticEvent("master-4"))
	appendDiagnosticLog(t, db, run, "event", diagnosticEvent("master-5"))
	for i := 0; i < 320; i++ {
		appendDiagnosticLog(t, db, run, "stdout", fmt.Sprintf("host-%d : ok=1 changed=0 unreachable=0 failed=1", i))
	}
	if err := os.CopyFS(filepath.Join(destination, "playbooks"), os.DirFS(runner.root)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, "VACUUM INTO ?", filepath.Join(destination, "platform.db")); err != nil {
		t.Fatal(err)
	}
}
