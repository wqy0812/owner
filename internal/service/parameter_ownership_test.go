package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestReleaseReviewIncludesParameterlessReleaseAndInvalidatesOnContractChange(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	owner := domain.User{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner}
	admin := domain.User{ID: seed.PlatformAdminID, Name: "Platform Admin", Role: domain.RolePlatformAdmin}
	now := time.Now().UTC()
	release := domain.ComponentRelease{
		ID: "release-review", ComponentID: "component-1", LineID: "line-review", LineName: "Review",
		Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now,
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	pending, err := platform.Catalog().SubmitReleaseReview(ctx, owner, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Review.Status != domain.ReleaseReviewPending || pending.Review.ContractDigest == "" {
		t.Fatalf("submitted review=%+v", pending.Review)
	}
	if _, err := platform.Catalog().DecideReleaseReview(ctx, owner, release.ID, true, "", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("component owner review decision error=%v", err)
	}
	preview, err := platform.Catalog().PreviewReleaseReview(ctx, admin, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := platform.Catalog().DecideReleaseReview(ctx, admin, release.ID, true, "contract checked", preview.PreviewDigest)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Review.Status != domain.ReleaseReviewApproved || approved.Review.ReviewedBy != admin.ID {
		t.Fatalf("approved review=%+v", approved.Review)
	}
	changed, err := platform.UpdateReleaseContract(ctx, owner, release.ID, []domain.ParameterDefinition{{
		Name: "installRoot", Description: "install root", Type: domain.ParameterTypeString,
		Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "/opt/runtime",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Review.Status != domain.ReleaseReviewNotSubmitted || changed.Review.ContractDigest != "" || changed.Candidate {
		t.Fatalf("contract change retained review=%+v candidate=%t", changed.Review, changed.Candidate)
	}
}

func TestScenarioParameterOverviewGroupsRepeatedNodesAndGraphSaveIsAtomic(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	now := time.Now().UTC()
	release := domain.ComponentRelease{
		ID: "release-scenario-parameters", ComponentID: "component-1", LineID: "line-scenario", LineName: "Scenario parameters",
		Version: "2.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now, ReleasedAt: &now,
		Parameters: []domain.ParameterDefinition{{
			Name: "cpu", Description: "container CPU", Type: domain.ParameterTypeInteger, Required: true,
			Visibility: domain.ParameterPublic, Modifiable: true, ValueProvider: domain.ParameterProviderScenarioOwner, SuggestedValue: 4,
		}},
		Actions: []domain.ActionDefinition{{ID: "install-scenario", Name: "Install", Kind: domain.ActionInstall, Playbook: "install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}},
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	owner := domain.User{ID: "scenario-parameter-owner", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	scenario, err := platform.CreateScenario(ctx, owner, domain.Scenario{Name: "Repeated runtime", Slug: "repeated-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "runtime-a", Name: "Runtime A", ReleaseID: release.ID, Action: domain.ActionInstall, ParameterValues: map[string]any{"cpu": 2}, Position: domain.GraphPosition{X: 10, Y: 10}},
		{ID: "runtime-b", Name: "Runtime B", ReleaseID: release.ID, Action: domain.ActionInstall, ParameterValues: map[string]any{"cpu": 8}, Position: domain.GraphPosition{X: 300, Y: 10}},
	}, Edges: []domain.ScenarioEdge{}}
	saved, err := platform.SaveScenarioGraph(ctx, owner, scenario.CurrentRevisionID, graph)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Graph.Nodes[0].HostGroup != "test_nodes" || saved.Graph.Nodes[1].HostGroup != "test_nodes" {
		t.Fatalf("derived host groups=%+v", saved.Graph.Nodes)
	}
	overview, err := platform.ScenarioParameterOverview(ctx, owner, scenario.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Editable || len(overview.Components) != 1 || len(overview.Components[0].Releases) != 1 || len(overview.Components[0].Releases[0].Nodes) != 2 {
		t.Fatalf("overview grouping=%+v", overview)
	}
	nodes := overview.Components[0].Releases[0].Nodes
	if nodes[0].ParameterValues["cpu"] == nodes[1].ParameterValues["cpu"] || nodes[0].Completed != 1 || nodes[1].Completed != 1 {
		t.Fatalf("repeated node values=%+v", nodes)
	}
	invalid := graph
	invalid.Nodes = append([]domain.ScenarioNode(nil), graph.Nodes...)
	invalid.Nodes[0].ParameterValues = map[string]any{"cpu": 3, "environmentOnly": "forbidden"}
	if _, err := platform.SaveScenarioGraph(ctx, owner, scenario.CurrentRevisionID, invalid); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown scenario parameter error=%v", err)
	}
	retained, err := database.GetScenarioRevision(ctx, scenario.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := retained.Graph.Nodes[0].ParameterValues["environmentOnly"]; exists || retained.Graph.Nodes[0].ParameterValues["cpu"] != float64(2) && retained.Graph.Nodes[0].ParameterValues["cpu"] != 2 {
		t.Fatalf("invalid graph partially persisted=%+v", retained.Graph.Nodes[0].ParameterValues)
	}
}

func TestEnvironmentOwnerWritesOnlyGovernedEnvironmentParameterFields(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	admin := domain.User{ID: seed.PlatformAdminID, Name: "Platform Admin", Role: domain.RolePlatformAdmin}
	definition := domain.EnvironmentParameterDefinition{ID: "legacy-region", CreatedBy: admin.ID, Key: "legacy_region", Label: "Deployment region", Description: "target deployment region", Type: domain.ParameterTypeString, Enum: []any{"cn", "us"}, CreatedAt: time.Now().UTC()}

	now := time.Now().UTC()
	release := domain.ComponentRelease{
		ID: "release-environment-parameter", ComponentID: "component-1", LineID: "line-environment", LineName: "Environment",
		Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now, ReleasedAt: &now,
		Parameters: []domain.ParameterDefinition{{
			Name: "region", Description: definition.Description, Type: definition.Type, Required: true,
			Visibility: domain.ParameterInternal, Modifiable: true, ValueProvider: domain.ParameterProviderEnvironmentOwner,
			EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}, Enum: definition.Enum,
		}},
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	owner := domain.User{ID: "environment-parameter-owner", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	environment, err := platform.CreateEnvironment(ctx, owner, domain.Environment{Name: "Parameter environment"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	key := domain.EnvironmentParameterValueKey(release.ID, release.Parameters[0])
	updated, err := platform.UpdateEnvironmentParameters(ctx, owner, environment.ID, map[string]any{key: "cn"}, "configure region")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == nil || updated.Revision.Parameters[key] != "cn" {
		t.Fatalf("updated parameters=%+v", updated.Revision)
	}
	deprecatedAt := now.Add(time.Minute)
	if err := database.DeprecateComponentRelease(ctx, release.ID, deprecatedAt); err != nil {
		t.Fatal(err)
	}
	fields, err := platform.EnvironmentParameterFields(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].ValueKey != key || len(fields[0].Bindings) != 1 || fields[0].Bindings[0].ReleaseID != release.ID {
		t.Fatalf("deprecated release environment fields=%+v", fields)
	}
	updated, err = platform.UpdateEnvironmentParameters(ctx, owner, environment.ID, map[string]any{key: "us"}, "maintain retained release parameter")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == nil || updated.Revision.Parameters[key] != "us" {
		t.Fatalf("deprecated release parameters=%+v", updated.Revision)
	}
	if _, err := platform.UpdateEnvironmentParameters(ctx, owner, environment.ID, map[string]any{"arbitrary": true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("arbitrary environment field error=%v", err)
	}
	scenarioOwner := domain.User{ID: "scenario-owner", Role: domain.RoleScenarioOwner}
	if _, err := platform.UpdateEnvironmentParameters(ctx, scenarioOwner, environment.ID, map[string]any{key: "us"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("scenario owner environment write error=%v", err)
	}
}

func TestRemovedDraftEnvironmentParameterCanBeClearedWithoutChangingHistory(t *testing.T) {
	ctx := context.Background()
	p, db := readinessTestPlatform(t)
	parameter := domain.ParameterDefinition{Name: "capacity", Description: "Capacity", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}}
	r := domain.ComponentRelease{ID: "review-draft", ComponentID: "component-1", LineName: "Review", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, Parameters: []domain.ParameterDefinition{parameter}, CreatedAt: time.Now().UTC()}
	if err := db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	owner := domain.User{ID: "review-environment-owner", Name: "Review environment owner", Role: domain.RoleEnvironmentOwner, CreatedAt: time.Now().UTC()}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	env, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "Review environment"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	key := domain.EnvironmentParameterValueKey(r.ID, parameter)
	env, err = p.UpdateEnvironmentParameters(ctx, owner, env.ID, map[string]any{key: "small"})
	if err != nil {
		t.Fatal(err)
	}
	componentOwner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	if _, err := p.UpdateReleaseContract(ctx, componentOwner, r.ID, []domain.ParameterDefinition{}, nil); err != nil {
		t.Fatal(err)
	}
	fields, err := p.EnvironmentParameterFields(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if field.ValueKey == key {
			t.Fatal("removed field unexpectedly still visible")
		}
	}
	retainedRevisionID := env.Revision.ID
	_, err = p.UpdateEnvironmentParameters(ctx, owner, env.ID, env.Revision.Parameters)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown fields must still be rejected: %v", err)
	}
	cleaned, err := p.UpdateEnvironmentParameters(ctx, owner, env.ID, map[string]any{}, "Remove retired field")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned.Revision.Parameters) != 0 {
		t.Fatalf("stale values remain: %v", cleaned.Revision.Parameters)
	}
	retained, err := db.GetEnvironmentRevision(ctx, retainedRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.Parameters[key] != "small" {
		t.Fatalf("historical values changed: %v", retained.Parameters)
	}
}

func TestRetiredGlobalBindingsCannotBeEditedClonedOrImported(t *testing.T) {
	ctx := context.Background()
	p, db := readinessTestPlatform(t)
	owner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	param := domain.ParameterDefinition{Name: "root", Description: "Root", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: "global"}}
	r := domain.ComponentRelease{ID: "legacy-draft", ComponentID: "component-1", LineID: "legacy-line", LineName: "Legacy", Version: "legacy-1", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, Parameters: []domain.ParameterDefinition{param}, CreatedAt: now}
	valid := r
	valid.Parameters = nil
	if err := db.CreateComponentRelease(ctx, valid); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r.Parameters)
	if _, err := db.DB().Exec("UPDATE component_releases SET parameters_json=? WHERE id=?", string(raw), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpdateReleaseContract(ctx, owner, r.ID, r.Parameters, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("draft edit: %v", err)
	}
	if _, err := db.DB().Exec(`UPDATE component_releases SET status='released',released_at=? WHERE id=?`, now.Format(time.RFC3339Nano), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PreviewReleaseDraft(ctx, owner, r.ComponentID, ReleaseDraftRequest{Mode: ReleaseDraftNewLine, LineName: "Next", Version: "2", ReleaseNotes: "Clone", TemplateSourceReleaseID: r.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("clone: %v", err)
	}
	entry := ComponentImportEntry{}
	entry.Component.Name, entry.Component.Slug, entry.Component.Layer, entry.Component.Tags = "Import", "retired-import", domain.LayerRuntimeState, []string{"runtime"}
	entry.Release.Version, entry.Release.LineName, entry.Release.ReleaseNotes, entry.Release.Parameters = "1", "Import", "Import", []domain.ParameterDefinition{param}
	if _, err := p.PreviewComponentImport(ctx, owner, ComponentImportRequest{Entries: []ComponentImportEntry{entry}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("import: %v", err)
	}
	retained, err := db.GetComponentRelease(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveOwnParameters(retained, domain.ScenarioNode{}, domain.EnvironmentRevision{Parameters: map[string]any{"global:retired": "/retained"}}, false)
	if !errors.Is(err, domain.ErrInvalid) || len(resolved) != 0 {
		t.Fatalf("unsupported binding resolution: %v %v", resolved, err)
	}
}
