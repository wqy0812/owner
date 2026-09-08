package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

func scenarioWithNumericParameters(t *testing.T, p *Platform, owner domain.User, revision domain.ScenarioRevision, configure bool) domain.ScenarioRevision {
	t.Helper()
	ctx := context.Background()
	db := testDatabase(p)
	release, err := db.GetComponentRelease(ctx, revision.Graph.Nodes[0].ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	release.ID, release.Version = "numeric-release", "numeric"
	release.PlaybookFiles, release.PlaybookWorkspaceRoot, release.PlaybookTreeSHA256 = nil, "", ""
	release.Parameters = []domain.ParameterDefinition{
		{Name: "count", Type: domain.ParameterTypeInteger, Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderScenarioOwner, Modifiable: true},
		{Name: "ratio", Type: domain.ParameterTypeNumber, Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderScenarioOwner, Modifiable: true},
		{Name: "settings", Type: domain.ParameterTypeObject, Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderScenarioOwner, Modifiable: true},
	}
	release.Actions = []domain.ActionDefinition{{ID: "numeric-install", Kind: domain.ActionInstall, Name: "Install", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}
	if configure {
		release.Actions = append(release.Actions, domain.ActionDefinition{ID: "numeric-configure", Kind: domain.ActionConfigure, Name: "Configure", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow})
	}
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, p.catalog.workspace.root, release.ID)
	graph := revision.Graph
	graph.Nodes[0].ReleaseID = release.ID
	graph.Nodes[0].ParameterValues = map[string]any{"count": 3, "ratio": 0.5, "settings": map[string]any{"limits": []any{3, map[string]any{"weight": 0.5}}}}
	revision, err = p.scenarios.SaveGraph(ctx, owner, revision.ID, graph)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestScenarioUpgradePreviewPersistedNumbers(t *testing.T) {
	for _, configure := range []bool{true, false} {
		t.Run(map[bool]string{true: "with configure", false: "without configure"}[configure], func(t *testing.T) {
			p, db, owner, first, env, _ := scenarioExecutionFixture(t)
			ctx := context.Background()
			first = scenarioWithNumericParameters(t, p, owner, first, configure)
			runScenarioProtocol(t, p, owner, first.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
			first, err := p.releases.PublishScenario(ctx, owner, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			formalEnv := copyScenarioTestEnvironment(t, p, env, "numeric-formal-env")
			installed := runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
			// Read the baseline through the store, including the snapshot decoder.
			persisted, err := db.GetRun(ctx, installed.ID)
			if err != nil {
				t.Fatal(err)
			}
			nodes, err := scenarioSnapshotNodes(persisted.Snapshot)
			if err != nil || len(nodes) != 1 || nodes[0].Variables["count"] != json.Number("3") {
				t.Fatalf("persisted numeric baseline=%+v err=%v", nodes, err)
			}
			cloneInput := ScenarioCloneRequest{SourceRevisionID: first.ID, SourceRunID: installed.ID}
			clonePlan, err := p.scenarios.PreviewClone(ctx, owner, first.ScenarioID, cloneInput)
			if err != nil {
				t.Fatal(err)
			}
			cloneInput.ExpectedPlanDigest = clonePlan.PlanDigest
			next, err := p.scenarios.CloneRevision(ctx, owner, first.ScenarioID, cloneInput)
			if err != nil {
				t.Fatal(err)
			}
			input := ScenarioExecutionRequest{EnvironmentID: formalEnv.ID, ExecutionMode: domain.ScenarioExecutionUpgrade}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, next.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready || len(preview.Operations) != 1 || preview.Operations[0].Change != "unchanged" {
				t.Fatalf("unchanged numeric baseline: ready=%v operations=%+v issues=%+v err=%v", preview.Ready, preview.Operations, preview.Issues, err)
			}
			for _, step := range preview.Steps {
				if step.Phase == "execute" {
					t.Fatalf("unchanged node executes a component action: %+v", step)
				}
			}
			if !configure {
				return
			}
			for _, values := range []map[string]any{
				{"count": 4, "ratio": 0.5, "settings": map[string]any{"limits": []any{3, map[string]any{"weight": 0.5}}}},
				{"count": 3, "ratio": 0.5, "settings": map[string]any{"limits": []any{3, map[string]any{"weight": 0.75}}}},
			} {
				graph := next.Graph
				graph.Nodes[0].ParameterValues = values
				next, err = p.scenarios.SaveGraph(ctx, owner, next.ID, graph)
				if err != nil {
					t.Fatal(err)
				}
				preview, err = p.execution.PreviewScenarioExecution(ctx, owner, next.ID, input, domain.RunScenarioTest)
				if err != nil || !preview.Ready || len(preview.Operations) != 1 || preview.Operations[0].Change != "configure" {
					t.Fatalf("changed numeric parameters did not configure: preview=%+v err=%v", preview, err)
				}
				changes := 0
				for _, step := range preview.Steps {
					if step.Phase == "execute" {
						changes++
						if step.Action != domain.ActionConfigure {
							t.Fatalf("incorrect change action: %s", step.Action)
						}
						for key, value := range values {
							if !domain.ParameterValuesEqual(step.Variables[key], value) {
								t.Fatalf("incorrect configure parameter %s: got %v, want %v", key, step.Variables[key], value)
							}
						}
					}
				}
				if changes != 1 {
					t.Fatalf("want one configure action, got %d", changes)
				}
			}
		})
	}
}

func TestScenarioBaselineVerifyPersistedNumericAcceptance(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	revision = scenarioWithNumericParameters(t, p, owner, revision, false)
	_, err := p.scenarios.SaveAcceptance(ctx, owner, revision.ID, ScenarioAcceptanceInput{
		ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: revision.AcceptanceJobs,
		Parameters: []domain.ParameterDefinition{
			{Name: "expected_count", Description: "Expected instance count", Type: domain.ParameterTypeInteger, Required: true, Visibility: domain.ParameterPublic, Enum: []any{3}},
			{Name: "expected_ratio", Description: "Expected ratio", Type: domain.ParameterTypeNumber, Required: true, Visibility: domain.ParameterPublic, Enum: []any{0.5}},
		},
		Bindings: []domain.ScenarioParameterBinding{
			{Parameter: "expected_count", Source: "node", NodeID: "runtime", SourceParameter: "count"},
			{Parameter: "expected_ratio", Source: "node", NodeID: "runtime", SourceParameter: "ratio"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err = db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	installed := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	persisted, err := db.GetRun(ctx, installed.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := scenarioSnapshotNodes(persisted.Snapshot)
	if err != nil || len(nodes) != 1 || nodes[0].Variables["count"] != json.Number("3") || nodes[0].Variables["ratio"] != json.Number("0.5") {
		t.Fatalf("persisted numeric parameters=%+v err=%v", nodes, err)
	}
	verified := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionBaselineVerify, domain.RunScenario)
	found := false
	for _, step := range verified.Snapshot.Plan.Steps {
		if step.SourceType == "scenario_acceptance" {
			found = true
			if step.Variables["expected_count"] != json.Number("3") || step.Variables["expected_ratio"] != json.Number("0.5") {
				t.Fatalf("historical acceptance parameters lost: %+v", step.Variables)
			}
		}
	}
	if !found {
		t.Fatal("baseline verification omitted acceptance")
	}
	for _, tc := range []struct {
		name, parameter, message string
		value                    any
	}{
		{"string", "count", "must be of type integer", "3"},
		{"fraction", "count", "must be of type integer", json.Number("3.5")},
		{"integer outside enum", "count", "not one of the allowed values", json.Number("4")},
		{"number outside enum", "ratio", "not one of the allowed values", json.Number("0.75")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := cloneMap(nodes[0].Variables)
			values[tc.parameter] = tc.value
			_, err := resolveAcceptanceParameters(revision, *env.Revision, map[string]map[string]any{"runtime": values})
			if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid historical binding returned %v, want %s", err, tc.message)
			}
		})
	}
}
