package store

import (
	"context"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestSharedCandidateSummariesStayLightAndPrivate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"shared", "private", "released"} {
		if err := s.CreateComponent(ctx, componentFixture(id, "component-owner-a")); err != nil {
			t.Fatal(err)
		}
		r := releaseFixture(id+"-release", id, "1.0", domain.ReleaseDraft)
		r.Candidate = id == "shared"
		if id == "released" {
			r.Status = domain.ReleaseReleased
		}
		if err := s.CreateComponentRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	viewer := domain.User{ID: "scenario-owner-a", Role: domain.RoleScenarioOwner}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := &countedCatalogQuery{queryer: tx}
	summaries, err := listComponentSummaries(ctx, q, viewer)
	if err != nil || q.count != 2 || len(summaries) != 2 {
		t.Fatalf("summary queries=%d rows=%+v err=%v", q.count, summaries, err)
	}
	for _, summary := range summaries {
		if summary.ID == "private" || summary.ReleaseCount != 1 {
			t.Fatalf("incorrect candidate visibility: %+v", summary)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.WorkbenchMetadata(ctx, viewer, []domain.RunReadModel{{ComponentReleaseID: "shared-release"}, {ComponentReleaseID: "private-release"}})
	if err != nil || len(metadata.Releases) != 1 || metadata.Releases["shared-release"].Version != "1.0" || len(metadata.Releases["shared-release"].Actions) != 0 {
		t.Fatalf("candidate workbench metadata=%+v err=%v", metadata, err)
	}
}
