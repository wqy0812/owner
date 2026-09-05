package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestComponentUsageExactHistoryAndPrivateSummaries(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	up2 := releaseFixture("history-release-2", "history-component", "2.0", domain.ReleaseDraft)
	up2.LineID = "second-line"
	up2.LineName = "second"
	if err := s.CreateComponentRelease(ctx, up2); err != nil {
		t.Fatal(err)
	}
	downstream := componentFixture("consumer", "component-owner-b")
	if err := s.CreateComponent(ctx, downstream); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("consumer-release", downstream.ID, "1.0", domain.ReleaseDraft)
	r.Dependencies = []domain.ComponentDependency{{ID: "dep-usage", UpstreamComponentID: "history-component", UpstreamReleaseID: "history-release", Purpose: "needed", Kind: domain.DependencyConfiguration, ParameterMappings: []domain.ParameterMapping{{}}}}
	if err := s.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	sc := domain.Scenario{ID: "usage-scenario", Slug: "usage-scenario", Name: "Private scenario", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "usage-revision", ScenarioID: sc.ID, Revision: 1, Status: domain.RevisionDraft, CreatedAt: testNow, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "a", ReleaseID: "history-release"}, {ID: "b", ReleaseID: "history-release"}}}}
	if err := s.CreateScenario(ctx, sc, revision); err != nil {
		t.Fatal(err)
	}
	viewer := domain.User{ID: "component-owner-a", Role: domain.RoleComponentOwner}
	out, err := s.ComponentUsage(ctx, viewer, "history-component", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if out.ComponentCount != 1 || out.ScenarioCount != 1 || len(out.Scenarios) != 1 || out.Scenarios[0].References != 2 || out.Scenarios[0].CanViewDetails || out.Components[0].CanViewDetails {
		t.Fatalf("unexpected private usage: %+v", out)
	}
	out, err = s.ComponentUsage(ctx, viewer, "history-component", up2.ID, false)
	if err != nil || out.ComponentCount != 0 || out.ScenarioCount != 0 {
		t.Fatal(out, err)
	}
	if _, err = s.ComponentUsage(ctx, viewer, "history-component", r.ID, false); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = s.ComponentUsage(ctx, domain.User{ID: "component-owner-b", Role: domain.RoleComponentOwner}, "history-component", "", false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE component_releases SET status='deprecated' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.ComponentUsage(ctx, viewer, "history-component", "", false)
	if err != nil || out.ComponentCount != 0 {
		t.Fatal(out, err)
	}
	out, err = s.ComponentUsage(ctx, viewer, "history-component", "", true)
	if err != nil || out.ComponentCount != 1 {
		t.Fatal(out, err)
	}
}
