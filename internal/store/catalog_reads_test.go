package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

type definitionsOnlyQuery struct {
	queryer
	t     *testing.T
	count int
}

func (q *definitionsOnlyQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.count++
	for _, table := range []string{"component_releases", "action_definitions", "scenario_revisions", "environment_revisions"} {
		if strings.Contains(query, table) {
			q.t.Fatalf("dictionary read scanned history table %s", table)
		}
	}
	return q.queryer.QueryContext(ctx, query, args...)
}

func TestCatalogDefinitionsExcludeHistoryAndPreserveManagementUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	definition := domain.EnvironmentParameterDefinition{ID: "region", Key: "region", Label: "Region", Type: domain.ParameterTypeString, Enum: []any{"north", "south"}, DefaultValue: "north", CreatedBy: "component-owner-a", CreatedAt: testNow}
	c := componentFixture("catalog-reader", "component-owner-a")
	if err := s.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("catalog-reader-r1", c.ID, "1.0.0", domain.ReleaseDraft)
	r.Parameters = []domain.ParameterDefinition{{Name: "region", Type: domain.ParameterTypeString, Enum: definition.Enum, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}}}
	if err := s.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	// Dictionary reads and management reads must work without nested pooled
	// connections; history usage must remain present only in management results.
	s.DB().SetMaxOpenConns(1)
	q := &definitionsOnlyQuery{queryer: s.DB(), t: t}
	snapshot, err := loadCatalogDefinitions(ctx, q)
	if err != nil || q.count != 3 {
		t.Fatalf("dictionary queries=%d err=%v", q.count, err)
	}
	if len(snapshot.Releases) != 0 {
		t.Fatal("dictionary query loaded releases")
	}
	publicSnapshot, err := s.ReadCatalogDefinitions(ctx)
	if err != nil || !reflect.DeepEqual(snapshot, publicSnapshot) {
		t.Fatalf("snapshot mismatch: %v", err)
	}
	management, err := s.ListPlatformOptionCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range management {
		if management[i].Usage.ComponentReleases != 1 {
			t.Fatalf("missing category reference count: %+v", management[i])
		}
		management[i].Usage = domain.PlatformOptionUsage{}
		for j := range management[i].Options {
			option := &management[i].Options[j]
			if (option.Value == "workers" || option.Value == "amd64") && option.Usage.ComponentReleases != 1 {
				t.Fatalf("missing option reference count: %+v", option)
			}
			option.Usage = domain.PlatformOptionUsage{}
		}
	}
	expected, err := domain.NewCatalogOptions(management)
	if err != nil || !reflect.DeepEqual(snapshot.Options, expected) {
		t.Fatalf("definition metadata differs from management metadata: %v", err)
	}
	options, err := s.ReadCatalogOptions(ctx)
	if err != nil || !reflect.DeepEqual(options, expected) {
		t.Fatalf("option-only lookup differs: %v", err)
	}
	if err := s.DeletePlatformOption(ctx, "option-workers", domain.AuditEvent{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("referenced option deletion=%v", err)
	}
}
