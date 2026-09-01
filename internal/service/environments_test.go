package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/sshcheck"
	"codex/platform-demo/internal/store"
)

type fixedCatalogBackupHealth struct {
	health domain.CatalogBackupHealth
	err    error
}

type fixedSSHChecker struct {
	err      error
	requests []sshcheck.Request
}

type blockingSSHChecker struct {
	mu       sync.Mutex
	current  int
	maximum  int
	reached  chan struct{}
	release  chan struct{}
	reachOne sync.Once
}

func (c *blockingSSHChecker) Check(context.Context, sshcheck.Request) error {
	c.mu.Lock()
	c.current++
	if c.current > c.maximum {
		c.maximum = c.current
	}
	if c.current == 8 {
		c.reachOne.Do(func() { close(c.reached) })
	}
	c.mu.Unlock()
	<-c.release
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return nil
}

func (r *fixedSSHChecker) Check(_ context.Context, request sshcheck.Request) error {
	r.requests = append(r.requests, request)
	return r.err
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

func configureTestSSHPassword(t *testing.T, platform *Platform, owner domain.User, environmentID string) domain.Environment {
	t.Helper()
	t.Setenv("CLUSTERFORGE_TEST_SSH_PASSWORD", "secret")
	updated, err := platform.UpdateCredentialRefs(context.Background(), owner, environmentID, []domain.CredentialRef{{Name: "ssh_password", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_SSH_PASSWORD"}}, "配置 SSH 检查凭据")
	if err != nil {
		t.Fatal(err)
	}
	return updated
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
	updated = configureTestSSHPassword(t, platform, owner, environment.ID)
	checker := &fixedSSHChecker{}
	platform.ConfigureEnvironmentSSHChecker(checker, "/tmp/test-known-hosts")

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
	if len(checker.requests) != 1 || checker.requests[0].Address != "192.0.2.10:2222" || checker.requests[0].User != "root" || checker.requests[0].Password != "secret" {
		t.Fatalf("SSH requests=%+v", checker.requests)
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
	checkedRevision = configureTestSSHPassword(t, platform, owner, environment.ID)
	checker := &fixedSSHChecker{}
	platform.ConfigureEnvironmentSSHChecker(checker, "/tmp/test-known-hosts")

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
	if len(checker.requests) != 1 || checker.requests[0].Address != "192.0.2.10:2222" {
		t.Fatalf("SSH check did not retain the initially loaded inventory: requests=%+v", checker.requests)
	}
}

func TestEnvironmentSSHCheckClassifiesAuthenticationFailure(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	configureTestSSHPassword(t, platform, owner, environment.ID)
	platform.ConfigureEnvironmentSSHChecker(&fixedSSHChecker{err: &sshcheck.CheckError{Kind: sshcheck.ErrorAuthentication, Err: errors.New("rejected")}}, "/tmp/test-known-hosts")

	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ssh_authentication_failed" || check.Results[0].Message != "SSH 用户或凭据认证失败" {
		t.Fatalf("ssh check=%+v", check)
	}
}

func TestEnvironmentSSHCheckClassifiesInvalidPrivateKey(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	configureTestSSHPassword(t, platform, owner, environment.ID)
	platform.ConfigureEnvironmentSSHChecker(&fixedSSHChecker{err: &sshcheck.CheckError{Kind: sshcheck.ErrorPrivateKey, Err: errors.New("invalid key")}}, "/tmp/test-known-hosts")
	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ssh_private_key_invalid" {
		t.Fatalf("private key check=%+v", check)
	}
}

func TestClassifyGoSSHFailureCoversStablePublicErrorCodes(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    string
		errorCode string
	}{
		{name: "known hosts", err: &sshcheck.CheckError{Kind: sshcheck.ErrorKnownHosts, Err: errors.New("missing")}, status: "failed", errorCode: "ssh_known_hosts_unconfigured"},
		{name: "host key", err: &sshcheck.CheckError{Kind: sshcheck.ErrorHostKey, Err: errors.New("changed")}, status: "unreachable", errorCode: "ssh_host_key_failed"},
		{name: "connection refused", err: &sshcheck.CheckError{Kind: sshcheck.ErrorNetwork, Err: errors.New("connection refused")}, status: "unreachable", errorCode: "ssh_connection_refused"},
		{name: "network", err: &sshcheck.CheckError{Kind: sshcheck.ErrorNetwork, Err: errors.New("no route")}, status: "unreachable", errorCode: "ssh_network_unreachable"},
		{name: "timeout", err: &sshcheck.CheckError{Kind: sshcheck.ErrorTimeout, Err: context.DeadlineExceeded}, status: "unreachable", errorCode: "ssh_check_timeout"},
		{name: "command", err: &sshcheck.CheckError{Kind: sshcheck.ErrorCommand, Err: errors.New("exit 1")}, status: "failed", errorCode: "ssh_command_failed"},
		{name: "handshake", err: &sshcheck.CheckError{Kind: sshcheck.ErrorHandshake, Err: errors.New("protocol")}, status: "failed", errorCode: "ssh_handshake_failed"},
		{name: "untyped", err: errors.New("unknown"), status: "failed", errorCode: "ssh_handshake_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, code, message := classifyGoSSHFailure(test.err)
			if status != test.status || code != test.errorCode || message == "" {
				t.Fatalf("classification=(%q,%q,%q), want status=%q code=%q", status, code, message, test.status, test.errorCode)
			}
		})
	}
}

func TestEnvironmentSSHCheckReportsMissingCredentialWithoutResolvingUnrelatedRefs(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", User: "root", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	checker := &fixedSSHChecker{}
	platform.ConfigureEnvironmentSSHChecker(checker, "/tmp/test-known-hosts")
	if _, err := platform.UpdateCredentialRefs(context.Background(), owner, environment.ID, []domain.CredentialRef{{Name: "K8S_ENCRYPTION_KEY", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING_UNRELATED"}}, "配置无关凭据"); err != nil {
		t.Fatal(err)
	}
	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil || check.Status != "degraded" || check.Results[0].ErrorCode != "ssh_credential_missing" || len(checker.requests) != 0 {
		t.Fatalf("unrelated credential check=%+v requests=%d err=%v", check, len(checker.requests), err)
	}

	if _, err := platform.UpdateCredentialRefs(context.Background(), owner, environment.ID, []domain.CredentialRef{{Name: "ansible_ssh_pass", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING_SSH"}}, "配置 SSH 凭据"); err != nil {
		t.Fatal(err)
	}
	check, err = platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != "degraded" || len(check.Results) != 1 || check.Results[0].ErrorCode != "ssh_credential_unconfigured" || len(checker.requests) != 0 {
		t.Fatalf("missing SSH credential check=%+v requests=%d", check, len(checker.requests))
	}

	t.Setenv("CLUSTERFORGE_TEST_LEGACY_SSH_PASSWORD", "legacy-secret")
	if _, err := platform.UpdateCredentialRefs(context.Background(), owner, environment.ID, []domain.CredentialRef{{Name: "ansible_ssh_pass", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_LEGACY_SSH_PASSWORD"}}, "配置旧名称 SSH 凭据"); err != nil {
		t.Fatal(err)
	}
	check, err = platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil || check.Status != "healthy" || len(checker.requests) != 1 || checker.requests[0].Password != "legacy-secret" {
		t.Fatalf("legacy SSH credential check=%+v requests=%+v err=%v", check, checker.requests, err)
	}
}

func TestEnvironmentSSHCredentialNeutralNameTakesPrecedence(t *testing.T) {
	t.Setenv("CLUSTERFORGE_TEST_NEUTRAL_SSH_PASSWORD", "neutral-secret")
	credentials, code, message := resolveEnvironmentSSHCredentials([]domain.CredentialRef{
		{Name: "ansible_ssh_pass", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING_LEGACY"},
		{Name: "ssh_password", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_NEUTRAL_SSH_PASSWORD"},
		{Name: "ansible_private_key_file", Kind: "sshKeyPath", Reference: "/legacy/key"},
		{Name: "ssh_private_key", Kind: "sshKeyPath", Reference: "/neutral/key"},
	})
	if code != "" || message != "" || credentials.password != "neutral-secret" || credentials.privateKeyPath != "/neutral/key" {
		t.Fatalf("credentials=%+v code=%s message=%s", credentials, code, message)
	}
}

func TestEnvironmentSSHCredentialAcceptsLegacyPrivateKeyEnvReference(t *testing.T) {
	t.Setenv("CLUSTERFORGE_TEST_LEGACY_SSH_KEY_PATH", "/legacy/key")
	credentials, code, message := resolveEnvironmentSSHCredentials([]domain.CredentialRef{{
		Name: "ansible_private_key_file", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_LEGACY_SSH_KEY_PATH",
	}})
	if code != "" || message != "" || credentials.privateKeyPath != "/legacy/key" {
		t.Fatalf("credentials=%+v code=%s message=%s", credentials, code, message)
	}
}

func TestEnvironmentSSHCheckRequiresInventoryUser(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-1", Address: "192.0.2.10", Groups: []string{"all"}}}, "使用远端节点"); err != nil {
		t.Fatal(err)
	}
	configureTestSSHPassword(t, platform, owner, environment.ID)
	checker := &fixedSSHChecker{}
	platform.ConfigureEnvironmentSSHChecker(checker, "/tmp/test-known-hosts")
	check, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
	if err != nil || check.Status != "degraded" || check.Results[0].ErrorCode != "ssh_user_missing" || len(checker.requests) != 0 {
		t.Fatalf("SSH user check=%+v requests=%+v err=%v", check, checker.requests, err)
	}
}

func TestEnvironmentSSHCheckLimitsConcurrencyToEightHosts(t *testing.T) {
	platform, owner, environment := maintenanceTestPlatform(t)
	hosts := make([]InventoryHost, 9)
	for index := range hosts {
		hosts[index] = InventoryHost{Name: fmt.Sprintf("node-%d", index+1), Address: fmt.Sprintf("192.0.2.%d", index+1), User: "root", Groups: []string{"all"}}
	}
	if _, err := platform.UpdateInventory(context.Background(), owner, environment.ID, hosts, "配置并发检查节点"); err != nil {
		t.Fatal(err)
	}
	configureTestSSHPassword(t, platform, owner, environment.ID)
	checker := &blockingSSHChecker{reached: make(chan struct{}), release: make(chan struct{})}
	platform.ConfigureEnvironmentSSHChecker(checker, "/tmp/test-known-hosts")
	result := make(chan error, 1)
	go func() {
		_, err := platform.CheckEnvironmentSSH(context.Background(), owner, environment.ID)
		result <- err
	}()
	select {
	case <-checker.reached:
	case <-time.After(time.Second):
		close(checker.release)
		t.Fatal("eight concurrent SSH checks did not start")
	}
	checker.mu.Lock()
	maximum := checker.maximum
	checker.mu.Unlock()
	if maximum != 8 {
		t.Fatalf("maximum concurrency=%d want=8", maximum)
	}
	close(checker.release)
	if err := <-result; err != nil {
		t.Fatal(err)
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
	updated, err := platform.UpdateInventory(context.Background(), owner, environment.ID, []InventoryHost{{Name: "node-2", Address: "192.0.2.2", Port: 22, Groups: []string{"all"}}}, "替换测试节点")
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
