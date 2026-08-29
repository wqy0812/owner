package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type fixedCatalogBackupHealth struct {
	health domain.CatalogBackupHealth
	err    error
}

type fixedConnectivityRunner struct {
	result   ansiblerunner.Result
	err      error
	requests []ActionRequest
}

func (r *fixedConnectivityRunner) Run(_ context.Context, request ActionRequest) (ActionResult, error) {
	r.requests = append(r.requests, request)
	return r.result, r.err
}

func (f *fixedCatalogBackupHealth) CatalogBackupHealth(context.Context) (domain.CatalogBackupHealth, error) {
	return f.health, f.err
}

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
	revision := domain.EnvironmentRevision{ID: "environment-r1", EnvironmentID: "environment-1", Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: inventory, Variables: map[string]string{"IMAGE_REGISTRY": "registry.invalid:5000"}, CredentialRefs: []domain.CredentialRef{}, CreatedBy: owner.ID, ChangeReason: "创建环境", CreatedAt: time.Now().UTC()}
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
	stored, err := platform.Environments().LatestHealthCheck(context.Background(), environment.ID)
	if err != nil || stored.ID != check.ID || stored.EnvironmentRevisionID != environment.CurrentRevisionID {
		t.Fatalf("stored health=%+v err=%v", stored, err)
	}
}

func TestEnvironmentConnectivityCombinesTCPAndSSHChecks(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	updated, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Port: 2222, Groups: []string{"all"}}}, "使用远端节点")
	if err != nil {
		t.Fatal(err)
	}
	platform.ConfigureEnvironmentHealthDialer(func(_ context.Context, _, _ string) (net.Conn, error) {
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	})
	runner := &fixedConnectivityRunner{result: ansiblerunner.Result{Recap: map[string]ansiblerunner.HostRecap{"node-1": {OK: 1}}}}
	platform.ConfigureConnectivityRunner(runner)

	check, err := platform.CheckEnvironmentConnectivity(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.TCP.Status != "healthy" || check.SSH.Status != "healthy" || len(check.SSH.Results) != 1 || check.SSH.Results[0].Status != "passed" {
		t.Fatalf("connectivity check=%+v", check)
	}
	if check.TCP.EnvironmentRevisionID != updated.CurrentRevisionID || check.SSH.EnvironmentRevisionID != updated.CurrentRevisionID {
		t.Fatalf("revision tcp=%s ssh=%s want=%s", check.TCP.EnvironmentRevisionID, check.SSH.EnvironmentRevisionID, updated.CurrentRevisionID)
	}
	if len(runner.requests) != 1 || runner.requests[0].Playbook != environmentSSHCheckPlaybook || !strings.Contains(string(runner.requests[0].Inventory), "ansible_port=2222") {
		t.Fatalf("runner requests=%+v", runner.requests)
	}
	stored, err := platform.Environments().LatestSSHCheck(context.Background(), environment.ID)
	if err != nil || stored.ID != check.SSH.ID {
		t.Fatalf("stored ssh=%+v err=%v", stored, err)
	}
}

func TestEnvironmentConnectivityKeepsOneRevisionWhenUpdateRacesTCPCheck(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	checkedRevision, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Port: 2222, Groups: []string{"all"}}}, "使用远端节点")
	if err != nil {
		t.Fatal(err)
	}
	var updateOnce sync.Once
	var updateErr error
	platform.ConfigureEnvironmentHealthDialer(func(_ context.Context, _, _ string) (net.Conn, error) {
		updateOnce.Do(func() {
			_, updateErr = platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-2", Address: "192.0.2.11", User: "root", Port: 22, Groups: []string{"all"}}}, "检查期间更新 Revision")
		})
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	})
	runner := &fixedConnectivityRunner{result: ansiblerunner.Result{Recap: map[string]ansiblerunner.HostRecap{"node-1": {OK: 1}}}}
	platform.ConfigureConnectivityRunner(runner)

	check, err := platform.CheckEnvironmentConnectivity(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	current, err := platform.store.GetEnvironment(context.Background(), environment.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentRevisionID == checkedRevision.CurrentRevisionID {
		t.Fatal("test did not create a concurrent Environment Revision")
	}
	if check.TCP.EnvironmentRevisionID != checkedRevision.CurrentRevisionID || check.SSH.EnvironmentRevisionID != checkedRevision.CurrentRevisionID {
		t.Fatalf("revision tcp=%s ssh=%s want=%s current=%s", check.TCP.EnvironmentRevisionID, check.SSH.EnvironmentRevisionID, checkedRevision.CurrentRevisionID, current.CurrentRevisionID)
	}
	if len(runner.requests) != 1 || !strings.Contains(string(runner.requests[0].Inventory), "node-1") || strings.Contains(string(runner.requests[0].Inventory), "node-2") {
		t.Fatalf("SSH check did not retain the initially loaded inventory: requests=%+v", runner.requests)
	}
}

func TestEnvironmentSSHCheckClassifiesAuthenticationFailure(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	platform.ConfigureConnectivityRunner(&fixedConnectivityRunner{result: ansiblerunner.Result{
		Recap: map[string]ansiblerunner.HostRecap{"node-1": {Unreachable: 1}},
		Logs:  []ansiblerunner.LogEvent{{Line: `fatal: [node-1]: UNREACHABLE! => {"msg":"Permission denied (publickey,password)"}`}},
	}, err: errors.New("ansible execute failed")})

	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ssh_authentication_failed" || check.Results[0].Message != "SSH 用户或凭据认证失败" {
		t.Fatalf("ssh check=%+v", check)
	}
}

func TestEnvironmentSSHCheckClassifiesBuiltinAssetTampering(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	platform.ConfigureConnectivityRunner(&fixedConnectivityRunner{err: ansiblerunner.ErrArtifactChanged})
	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ansible_unavailable" || !strings.Contains(check.Results[0].Message, "完整性") {
		t.Fatalf("tampered built-in asset check=%+v", check)
	}
}

func TestEnvironmentSSHCheckReportsMissingCredentialWithoutResolvingUnrelatedRefs(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	runner := &fixedConnectivityRunner{result: ansiblerunner.Result{Recap: map[string]ansiblerunner.HostRecap{"node-1": {OK: 1}}}}
	platform.ConfigureConnectivityRunner(runner)
	if _, err := platform.UpdateCredentialRefs(context.Background(), owner, environment.ID, []domain.CredentialRef{{Name: "K8S_ENCRYPTION_KEY", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING_UNRELATED"}}, "配置无关凭据"); err != nil {
		t.Fatal(err)
	}
	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil || check.Status != "healthy" || len(runner.requests) != 1 {
		t.Fatalf("unrelated credential check=%+v requests=%d err=%v", check, len(runner.requests), err)
	}

	if _, err := platform.UpdateCredentialRefs(context.Background(), owner, environment.ID, []domain.CredentialRef{{Name: "ansible_ssh_pass", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING_SSH"}}, "配置 SSH 凭据"); err != nil {
		t.Fatal(err)
	}
	check, err = platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ssh_credential_unconfigured" || len(runner.requests) != 1 {
		t.Fatalf("missing SSH credential check=%+v requests=%d", check, len(runner.requests))
	}
}

func TestEnvironmentOwnerWorkbenchRequiresCurrentSSHCheck(t *testing.T) {
	_, owner, environment := maintenanceTestPlatform(t)
	environment.Revision.Variables = map[string]string{"IMAGE_REGISTRY": "registry.example:5000", "FILE_STATION": "files.example:8080"}
	environment.HealthCheck = &domain.EnvironmentHealthCheck{EnvironmentRevisionID: environment.CurrentRevisionID, Status: "healthy", Results: []domain.EnvironmentEndpointCheck{}, CheckedAt: time.Now().UTC()}
	items := environmentOwnerWork(owner, []domain.Environment{environment})
	if len(items) != 1 || !workItemHasReason(items[0], "environment.ssh_missing") {
		t.Fatalf("missing SSH work items=%+v", items)
	}
	environment.SSHCheck = &domain.EnvironmentSSHCheck{
		EnvironmentRevisionID: environment.CurrentRevisionID, Status: "degraded", CheckedAt: time.Now().UTC(),
		Results: []domain.EnvironmentSSHHostCheck{{Kind: "host", Name: "node-1", Status: "unreachable"}},
	}
	items = environmentOwnerWork(owner, []domain.Environment{environment})
	if len(items) != 1 || !workItemHasReason(items[0], "environment.ssh_degraded") || workItemHasReason(items[0], "environment.ssh_missing") {
		t.Fatalf("degraded SSH work items=%+v", items)
	}
}

func workItemHasReason(item domain.WorkItem, code string) bool {
	for _, reason := range item.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func TestEnvironmentOwnerWorkbenchShowsCatalogBackupWarnings(t *testing.T) {
	platform, owner, _ := maintenanceTestPlatform(t)
	provider := &fixedCatalogBackupHealth{}
	platform.ConfigurePublicationBackupHealth(provider)
	workbench, err := platform.Workbench(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	item := workItemByID(workbench.Items, "catalog_backup:health")
	if item == nil || item.Status != domain.WorkStatusActionRequired || len(item.Reasons) != 1 || item.Reasons[0].Code != "catalog_backup.repository_unconfigured" {
		t.Fatalf("unconfigured backup work item=%+v", item)
	}
	now := time.Now().UTC()
	provider.health = domain.CatalogBackupHealth{Configured: true, Behind: true, CurrentGeneration: 8, BackedUpGeneration: 6, LastError: "git push failed", LastErrorAt: &now}
	workbench, err = platform.Workbench(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	item = workItemByID(workbench.Items, "catalog_backup:health")
	if item == nil || item.Priority != domain.WorkPriorityCritical || len(item.Reasons) != 2 {
		t.Fatalf("failed backup work item=%+v", item)
	}
}

func workItemByID(items []domain.WorkItem, id string) *domain.WorkItem {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
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
