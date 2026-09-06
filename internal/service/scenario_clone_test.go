package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestScenarioCloneRejectsTestPassedRevisionAndPreservesSource(t *testing.T) {
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
	platform := newTestPlatform(t, database, nil, nil)
	_, err = platform.scenarios.PreviewClone(ctx, owner, scenario.ID, ScenarioCloneRequest{SourceRevisionID: revision.ID})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("test-only source accepted: %v", err)
	}
	_, err = platform.scenarios.CloneRevision(ctx, owner, scenario.ID, ScenarioCloneRequest{SourceRevisionID: revision.ID, ExpectedPlanDigest: "stale"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("test-only clone accepted: %v", err)
	}
	preserved, err := database.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Status != domain.RevisionTestPassed || preserved.TestPassedAt == nil || preserved.AbandonedAt != nil {
		t.Fatalf("source revision changed=%+v", preserved)
	}
}
