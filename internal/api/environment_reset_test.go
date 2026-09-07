package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
)

// Install shared services in their own recorded host groups, alongside the two
// cluster installation Runs. The reset must never execute or remove these items.
func addSharedResetRecords(t *testing.T, f *apiFixture) []domain.EnvironmentComponentInstallation {
	t.Helper()
	ctx := context.Background()
	revision, err := f.database.GetEnvironmentRevision(ctx, "environment-test-r1")
	if err != nil {
		t.Fatal(err)
	}
	var inventory service.InventoryDocument
	if err := json.Unmarshal(revision.Inventory, &inventory); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	for i, group := range []string{"file_station", "image_registry"} {
		if err := f.database.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{ID: "reset-group-" + group, Value: group, Label: group, CreatedBy: seed.PlatformAdminID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		address := "192.0.2.50"
		if i == 1 {
			address = "192.0.2.51"
		}
		inventory.Hosts = append(inventory.Hosts, service.InventoryHost{Name: group, Address: address, Groups: []string{group}})
	}
	revision.ID, revision.Revision, revision.CreatedAt = "environment-test-r2-shared", 2, now
	revision.Variables = map[string]string{"FILE_STATION": "192.0.2.50:8080", "IMAGE_REGISTRY": "192.0.2.51:5000"}
	revision.Inventory, _ = json.Marshal(inventory)
	if err := f.database.CreateEnvironmentRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	items := []domain.EnvironmentComponentInstallation{}
	for _, group := range []string{"file_station", "image_registry"} {
		backup := domain.BackupMetadata{EnvironmentID: revision.EnvironmentID, ComponentID: "component-test-runtime", ReleaseID: "release-test-runtime-1.0.0", ActionID: "action-test-runtime-install-1.0", InstallRunID: "run-shared-" + group, NodeID: group, CapturedAt: now, PlaybookSHA256: "shared-service-evidence"}
		source := domain.Run{ID: backup.InstallRunID, Kind: domain.RunComponentTest, Status: domain.RunSucceeded, RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: revision.EnvironmentID, EnvironmentRevisionID: revision.ID, ComponentReleaseID: backup.ReleaseID, CreatedAt: now, FinishedAt: &now,
			InputSnapshot: map[string]any{"steps": []any{map[string]any{"id": group, "sourceNodeId": group, "componentId": backup.ComponentID, "releaseId": backup.ReleaseID, "actionId": backup.ActionID, "action": "install", "limit": group}}},
		}
		if err := f.database.CreateRun(ctx, source, nil); err != nil {
			t.Fatal(err)
		}
		item := domain.EnvironmentComponentInstallation{EnvironmentID: revision.EnvironmentID, ComponentID: backup.ComponentID, ReleaseID: backup.ReleaseID, NodeID: group, InstallRunID: source.ID, BackupRef: "/shared-backups/" + group, Backup: backup, TestOnly: true, InstalledAt: now}
		if err := f.database.UpsertEnvironmentComponentInstallation(ctx, item); err != nil {
			t.Fatal(err)
		}
		stored, err := f.database.GetEnvironmentComponentInstallationForNode(ctx, item.EnvironmentID, item.ComponentID, item.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, stored)
	}
	return items
}
