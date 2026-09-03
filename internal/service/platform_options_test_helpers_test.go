package service

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/store"
)

func seedPlatformOptionsForServiceTest(t *testing.T, database *store.Store) {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := (seed.Seeder{Store: database, Now: func() time.Time { return now }}).SeedUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsurePlatformOption(context.Background(), "hostGroup", domain.PlatformOption{
		ID: "platform-option-test-nodes", Value: "test_nodes", Label: "Test nodes", SortOrder: 2000,
		CreatedBy: seed.PlatformAdminID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func completeServiceTestFacts() map[string]any {
	return map[string]any{
		"architecture": "amd64", "operatingSystem": "Ubuntu", "operatingSystemVersion": "24.04",
		"containerRuntime": "docker", "containerRuntimeVersion": "docker@24.0.9", "ipFamily": "IPv4",
	}
}
