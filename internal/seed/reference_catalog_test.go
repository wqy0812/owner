package seed

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"context"
	"errors"
	"testing"
	"time"
)

// Reference snapshots are only inspected by tests, never seeded by the server.
func runReferenceCatalogFixture(s Seeder, ctx context.Context) error {
	if s.Store == nil {
		return errors.New("seed store is required")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	if err := s.seedUsers(ctx, now); err != nil {
		return err
	}
	if err := seedHistoricalPlatformCatalog(s, ctx, now); err != nil {
		return err
	}
	if err := s.seedComponents(ctx, now); err != nil {
		return err
	}
	if err := s.seedScenarios(ctx, now); err != nil {
		return err
	}
	if err := s.seedEnvironments(ctx, now); err != nil {
		return err
	}
	if err := s.appendAuditIfMissing(ctx, domain.AuditEvent{
		ID: seedAuditID, ActorID: "system", Action: seedAuditAction,
		ResourceType: "platform", ResourceID: seedAuditResourceID,
		Metadata: map[string]any{"openFuyaoSnapshot": "examples/ansible/openfuyao"}, CreatedAt: now,
	}); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Catalog(ctx, now); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Scenarios(ctx, now); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Environment(ctx, now); err != nil {
		return err
	}
	return s.appendAuditIfMissing(ctx, domain.AuditEvent{
		ID: kubernetes1175SeedAuditID, ActorID: "system", Action: kubernetes1175SeedAuditAction,
		ResourceType: "scenario", ResourceID: "scenario-k8s-1.17.5",
		Metadata: map[string]any{"snapshot": "examples/ansible/k8s-1.17.5-cluster", "model": "minimal-components", "evidence": "not_seeded"}, CreatedAt: now,
	})
}

// Preserve the independent dimensions of these historical reference snapshots.
// Their mixed distro/version values cannot be assigned a parent by inference.
func seedHistoricalPlatformCatalog(s Seeder, ctx context.Context, now time.Time) error {
	if err := s.seedPlatformCatalog(ctx, now); err != nil {
		return err
	}
	if _, err := s.Store.DB().ExecContext(ctx, `UPDATE platform_options SET parent_option_id=NULL WHERE category_id='platform-category-operating-system-version'`); err != nil {
		return err
	}
	_, err := s.Store.DB().ExecContext(ctx, `UPDATE platform_option_categories SET parent_category_id=NULL WHERE id='platform-category-operating-system-version'`)
	return err
}

func TestFreshRoleContractInitializesOnlyGovernedFoundation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Seeder{Store: db}
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"components", "component_releases", "scenarios", "environments", "runs", "run_jobs", "action_execution_receipts"} {
		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
