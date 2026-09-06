package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/store"
)

// Opt-in browser data belongs only to the temporary API started by the E2E
// script. The production seeder continues to create an empty business catalog.
func TestLiveAPIComponentFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_LIVE_API_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_LIVE_API_FIXTURE for isolated browser data")
	}
	if !filepath.IsAbs(destination) {
		t.Fatal("fixture destination must be absolute")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(destination, "playbooks"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(destination, "platform.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := (seed.Seeder{Store: db}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"browser-upstream", "browser-consumer"} {
		if err := db.CreateComponent(ctx, domain.Component{ID: id, Slug: id, Name: id, OwnerID: seed.ComponentOwnerRuntimeID, Layer: domain.LayerRuntimeState, Tags: []string{}, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	upstream := domain.ComponentRelease{ID: "browser-upstream-r1", ComponentID: "browser-upstream", Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
		Parameters: []domain.ParameterDefinition{{Name: "runtime_version", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "1.0.0"}},
	}
	if err := db.CreateComponentRelease(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	consumer := domain.ComponentRelease{ID: "browser-consumer-r1", ComponentID: "browser-consumer", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now,
		Parameters:   []domain.ParameterDefinition{{Name: "required_runtime", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
		Dependencies: []domain.ComponentDependency{{ID: "browser-runtime-dependency", ReleaseID: "browser-consumer-r1", UpstreamComponentID: upstream.ComponentID, UpstreamReleaseID: upstream.ID, Purpose: "Browser parameter lineage", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "runtime_version", TargetParameter: "required_runtime"}}}},
	}
	if err := db.CreateComponentRelease(ctx, consumer); err != nil {
		t.Fatal(err)
	}
}
