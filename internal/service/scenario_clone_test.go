package service

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestScenarioCloneAllowsTestPassedRevisionAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	owner := domain.User{ID: "scenario-owner", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	revision := domain.ScenarioRevision{
		ID: "scenario-r1", ScenarioID: "scenario-1", Revision: 1, Status: domain.RevisionTestPassed,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}, CreatedAt: now, TestPassedAt: &now,
	}
	scenario := domain.Scenario{
		ID: "scenario-1", Slug: "scenario-1", Name: "Scenario", OwnerID: owner.ID,
		CurrentRevisionID: revision.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	platform := NewPlatform(database, nil, nil)
	plan, err := platform.PreviewScenarioClone(ctx, owner, scenario.ID, ScenarioCloneRequest{SourceRevisionID: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	created, err := platform.CloneScenarioRevision(ctx, owner, scenario.ID, ScenarioCloneRequest{
		SourceRevisionID: revision.ID, ExpectedPlanDigest: plan.PlanDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 2 || created.Status != domain.RevisionDraft || created.TestPassedAt != nil {
		t.Fatalf("created revision=%+v", created)
	}
	preserved, err := database.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Status != domain.RevisionDeprecated || preserved.TestPassedAt == nil || preserved.AbandonedAt == nil {
		t.Fatalf("source revision changed=%+v", preserved)
	}
}
