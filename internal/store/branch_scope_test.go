package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestReleaseBranchScopeLockedAtEveryVersionWrite(t *testing.T) {
	a, _ := catalogStorePair(t)
	ctx := context.Background()
	c := componentFixture("scope-component", "component-owner-a")
	if err := a.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("scope-r1", c.ID, "1", domain.ReleaseDraft)
	if err := a.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, _ = a.GetComponentRelease(ctx, r.ID)
	r.EnvironmentConstraints = map[string]any{"architecture": []string{"amd64", "amd64"}}
	if err := a.UpdateDraftRelease(ctx, r); err != nil {
		t.Fatalf("equivalent scope rejected: %v", err)
	}
	r.EnvironmentConstraints = map[string]any{}
	if err := a.UpdateDraftRelease(ctx, r); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("widening accepted: %v", err)
	}
	r.ID, r.Version = "scope-r2", "2"
	r.Actions = nil
	if err := a.CreateComponentRelease(ctx, r); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("new version changed branch: %v", err)
	}
	if _, err := a.GetComponentRelease(ctx, r.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("partial version committed")
	}
	if _, err := a.DB().ExecContext(ctx, `UPDATE component_release_lines SET environment_constraints_json='{}' WHERE id=?`, r.LineID); err == nil {
		t.Fatal("branch scope was mutable")
	}
	r.LineID, r.LineName = "independent-branch", "Independent"
	if err := a.CreateComponentRelease(ctx, r); err != nil {
		t.Fatalf("new branch rejected: %v", err)
	}
}

func TestScenarioMetadataCannotChangeBranchScope(t *testing.T) {
	db, scenario, _ := scenarioLifecycleFixture(t)
	scenario.Name = "Changed metadata"
	scenario.EnvironmentConstraints = map[string]any{"architecture": []string{"amd64"}}
	if err := db.UpdateScenario(context.Background(), scenario); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("metadata bypassed scope lock: %v", err)
	}
	saved, err := db.GetScenario(context.Background(), scenario.ID, false)
	if err != nil || saved.Name == scenario.Name {
		t.Fatalf("partial metadata committed: %+v %v", saved, err)
	}
	scenario.EnvironmentConstraints = nil
	if err = db.UpdateScenario(context.Background(), scenario); err != nil {
		t.Fatalf("ordinary metadata rejected: %v", err)
	}
}
