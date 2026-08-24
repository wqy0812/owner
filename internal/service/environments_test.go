package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func maintenanceTestPlatform(t *testing.T) (*Platform, domain.User, domain.Environment) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	owner := domain.User{ID: "environment-owner", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: time.Now().UTC()}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	inventory, _ := json.Marshal(InventoryDocument{Hosts: []InventoryHost{{Name: "node-1", Address: "127.0.0.1", Port: 22, Groups: []string{"all"}}}})
	revision := domain.EnvironmentRevision{ID: "environment-r1", EnvironmentID: "environment-1", Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: inventory, Variables: map[string]string{"IMAGE_REGISTRY": "registry.invalid:5000"}, CredentialRefs: []domain.CredentialRef{}, MaxConcurrent: 1, CreatedBy: owner.ID, ChangeReason: "创建环境", CreatedAt: time.Now().UTC()}
	environment := domain.Environment{ID: "environment-1", Name: "Lab", OwnerID: owner.ID, CurrentRevisionID: revision.ID, Revision: &revision, CreatedAt: revision.CreatedAt, UpdatedAt: revision.CreatedAt}
	if err := database.CreateEnvironment(ctx, environment, revision); err != nil {
		t.Fatal(err)
	}
	return NewPlatform(database, nil, nil), owner, environment
}

func TestEnvironmentHealthCheckPersistsReachability(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	platform.ConfigureEnvironmentHealthDialer(func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "127.0.0.1:22" {
			left, right := net.Pipe()
			_ = right.Close()
			return left, nil
		}
		return nil, errors.New("unreachable")
	})
	check, err := platform.CheckEnvironmentHealth(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 2 || !check.Results[0].Reachable || check.Results[1].Reachable {
		t.Fatalf("health check=%+v", check)
	}
	stored, err := platform.Store().LatestEnvironmentHealthCheck(context.Background(), environment.ID)
	if err != nil || stored.ID != check.ID || stored.EnvironmentRevisionID != environment.CurrentRevisionID {
		t.Fatalf("stored health=%+v err=%v", stored, err)
	}
}

func TestRestoreEnvironmentRevisionCreatesNewRevisionWithReason(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	updated, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-2", Address: "10.0.0.2", Port: 22, Groups: []string{"all"}}}, "替换测试节点")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == nil || updated.Revision.Revision != 2 || updated.Revision.ChangeReason != "替换测试节点" {
		t.Fatalf("updated revision=%+v", updated.Revision)
	}
	restored, err := platform.RestoreEnvironmentRevision(context.Background(), owner, environment.ID, environment.CurrentRevisionID, "回退错误节点配置")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision == nil || restored.Revision.Revision != 3 || restored.Revision.ChangeReason != "回退错误节点配置" {
		t.Fatalf("restored revision=%+v", restored.Revision)
	}
	var inventory InventoryDocument
	if err := json.Unmarshal(restored.Revision.Inventory, &inventory); err != nil || len(inventory.Hosts) != 1 || inventory.Hosts[0].Name != "node-1" {
		t.Fatalf("restored inventory=%+v err=%v", inventory, err)
	}
}
