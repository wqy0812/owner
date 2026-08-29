package store

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestDeprecateScenarioRevisionAdvancesPublicationGeneration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	owner := domain.User{ID: "scenario-owner", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	revision := domain.ScenarioRevision{ID: "scenario-r1", ScenarioID: "scenario", Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}, CreatedAt: now}
	scenario := domain.Scenario{ID: "scenario", Slug: "scenario", Name: "Scenario", OwnerID: owner.ID, CurrentRevisionID: revision.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `UPDATE scenario_revisions SET status='released',released_at=? WHERE id=?`, timeText(now), revision.ID); err != nil {
		t.Fatal(err)
	}
	var beforeGlobal, beforeRevision int64
	if err := database.DB().QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&beforeGlobal); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRowContext(ctx, `SELECT publication_generation FROM scenario_revisions WHERE id=?`, revision.ID).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	if err := database.DeprecateScenarioRevision(ctx, revision.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	var afterGlobal, afterRevision int64
	if err := database.DB().QueryRowContext(ctx, `SELECT status,publication_generation FROM scenario_revisions WHERE id=?`, revision.ID).Scan(&status, &afterRevision); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&afterGlobal); err != nil {
		t.Fatal(err)
	}
	if status != "deprecated" || afterGlobal != beforeGlobal+1 || afterRevision != beforeRevision+1 {
		t.Fatalf("status=%s global=%d->%d revision=%d->%d", status, beforeGlobal, afterGlobal, beforeRevision, afterRevision)
	}
}
