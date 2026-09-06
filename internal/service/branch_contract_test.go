package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestScopedContractPatchKeepsOtherSectionAndRejectsStaleSave(t *testing.T) {
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	upstreamComponent := domain.Component{ID: "upstream", Slug: "upstream", Name: "Upstream", Layer: domain.LayerHostFoundation, OwnerID: owner.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.CreateComponent(ctx, upstreamComponent); err != nil {
		t.Fatal(err)
	}
	param := domain.ParameterDefinition{Name: "root", Description: "Prepared path", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "/tmp/example"}
	upstream := domain.ComponentRelease{ID: "upstream-r1", ComponentID: upstreamComponent.ID, Version: "1", Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow, Compatibility: domain.CompatibilityNotApplicable, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{param}, CreatedAt: time.Now()}
	if err := db.CreateComponentRelease(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "scoped-r1", ComponentID: "component-1", Version: "1", ReleaseNotes: "Original notes", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, Compatibility: domain.CompatibilityNotApplicable, EnvironmentConstraints: map[string]any{}, CreatedAt: time.Now()}
	if err := db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	gen := r.PublicationGeneration
	params := []domain.ParameterDefinition{param}
	saved, err := p.Catalog().PatchReleaseContract(ctx, owner, r.ID, ReleaseContractPatch{Section: "parameters", ExpectedGeneration: &gen, Parameters: &params})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReleaseNotes != r.ReleaseNotes || len(saved.Dependencies) != 0 {
		t.Fatalf("unrelated content changed: %+v", saved)
	}
	deps := []domain.ComponentDependency{{UpstreamComponentID: upstream.ComponentID, UpstreamReleaseID: upstream.ID, Purpose: "Requires prepared root", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "prepared_root"}}}}
	mapped := param
	mapped.Name = "prepared_root"
	mapped.ValueProvider = domain.ParameterProviderUpstreamMapping
	mapped.FixedValue = nil
	patch := ReleaseContractPatch{Section: "dependencies", ExpectedGeneration: &gen, Dependencies: &deps, NewParameters: []domain.ParameterDefinition{mapped}}
	if _, err := p.Catalog().PatchReleaseContract(ctx, owner, r.ID, patch); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	gen = saved.PublicationGeneration
	saved, err = p.Catalog().PatchReleaseContract(ctx, owner, r.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Parameters) != 2 || saved.Parameters[0].Name != "root" || len(saved.Dependencies) != 1 {
		t.Fatalf("atomic reference: %+v", saved)
	}
	gen = saved.PublicationGeneration
	empty := []domain.ComponentDependency{}
	_, err = p.Catalog().PatchReleaseContract(ctx, owner, r.ID, ReleaseContractPatch{Section: "dependencies", ExpectedGeneration: &gen, Dependencies: &empty})
	if err == nil {
		t.Fatal("orphan mapping parameter saved")
	}
	after, _ := db.GetComponentRelease(ctx, r.ID)
	if !reflect.DeepEqual(after.Dependencies, saved.Dependencies) || after.PublicationGeneration != saved.PublicationGeneration {
		t.Fatal("failed save partially mutated contract")
	}
	changed := append([]domain.ParameterDefinition(nil), saved.Parameters...)
	changed[1].Type = domain.ParameterTypeInteger
	if _, err := p.Catalog().PatchReleaseContract(ctx, owner, r.ID, ReleaseContractPatch{Section: "parameters", ExpectedGeneration: &gen, Parameters: &changed}); err == nil {
		t.Fatal("mapping type mismatch saved")
	}
	removed, err := p.Catalog().PatchReleaseContract(ctx, owner, r.ID, ReleaseContractPatch{Section: "dependencies", ExpectedGeneration: &gen, Dependencies: &empty, RemoveParameters: []string{"prepared_root"}})
	if err != nil || len(removed.Parameters) != 1 || len(removed.Dependencies) != 0 {
		t.Fatalf("explicit removal: %+v %v", removed, err)
	}
}

func TestScenarioForkScopeIsBoundToPreviewAndBlocksIncompatibleExecution(t *testing.T) {
	p, db, owner, source, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, source.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	source, err := p.releases.PublishScenario(ctx, owner, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := ScenarioForkRequest{SourceRevisionID: source.ID, Name: "Different scope", Slug: "different-scope", EnvironmentConstraints: map[string]any{"architecture": []string{"amd64"}}}
	preview, err := p.scenarios.PreviewFork(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	input.EnvironmentConstraints = map[string]any{"architecture": []string{"arm64"}}
	if _, err = p.scenarios.Fork(ctx, owner, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale target preview: %v", err)
	}
	preview, err = p.scenarios.PreviewFork(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	fork, err := p.scenarios.Fork(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := db.GetScenarioRevision(ctx, fork.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if !domain.SameEnvironmentConstraints(rev.EnvironmentConstraints, input.EnvironmentConstraints) || rev.Status != domain.RevisionDraft || rev.TestPassedAt != nil || rev.SourceRunID != "" {
		t.Fatalf("branch inherited evidence or wrong scope: %+v", rev)
	}
	if plan, previewErr := p.Execution().PreviewScenarioExecution(ctx, owner, rev.ID, ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall}, domain.RunScenarioTest); previewErr == nil && plan.Ready {
		t.Fatal("incompatible target environment accepted")
	}
	expected := domain.ScenarioRevisionSpecDigest(rev)
	rev.EnvironmentConstraints = source.EnvironmentConstraints
	if err = db.SaveScenarioRevisionDefinition(ctx, rev, expected); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("branch scope changed: %v", err)
	}
}
