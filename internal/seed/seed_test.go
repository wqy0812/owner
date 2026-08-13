package seed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestSeederIsIdempotentAndRegistersClassifiedModel(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seeder := Seeder{Store: database, Now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }}
	if err := seeder.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := seeder.Run(ctx); err != nil {
		t.Fatalf("idempotent seed: %v", err)
	}
	users, _ := database.ListUsers(ctx)
	if len(users) != 4 {
		t.Fatalf("users=%d, want four demo personas", len(users))
	}
	viewer, _ := database.GetUser(ctx, ComponentOwnerRuntimeID)
	components, err := database.ListComponents(ctx, viewer)
	if err != nil || len(components) != 15 {
		t.Fatalf("components=%d err=%v", len(components), err)
	}
	for _, component := range components {
		if err := domain.ValidateComponentClassification(component); err != nil {
			t.Fatalf("component %s classification: %v", component.ID, err)
		}
	}
	for id, wantLayer := range map[string]domain.ComponentLayer{
		"component-bke-cert": domain.LayerHostFoundation, "component-k8s-1.17.5-cert": domain.LayerHostFoundation,
		"component-containerd": domain.LayerRuntimeState, "component-etcd": domain.LayerRuntimeState,
		"component-kubernetes": domain.LayerOrchestrationCore, "component-kube-proxy": domain.LayerOrchestrationCore,
		"component-calico": domain.LayerClusterService, "component-coredns": domain.LayerClusterService,
		"component-bke-common": domain.LayerPlatformExtension, "component-bke-master": domain.LayerPlatformExtension,
	} {
		component, getErr := database.GetComponent(ctx, id, false)
		if getErr != nil || component.Layer != wantLayer {
			t.Fatalf("component %s layer=%s want=%s err=%v", id, component.Layer, wantLayer, getErr)
		}
	}
	open, err := database.GetScenarioRevision(ctx, "scenario-openfuyao-r1")
	if err != nil || len(open.Graph.Nodes) != 5 || len(open.Graph.Edges) != 4 {
		t.Fatalf("OpenFuyao graph nodes=%d edges=%d err=%v", len(open.Graph.Nodes), len(open.Graph.Edges), err)
	}
	stagePlaybooks := map[string]bool{}
	for _, node := range open.Graph.Nodes {
		release, releaseErr := database.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil || len(release.Actions) != 1 || !release.Actions[0].NeedsApproval() {
			t.Fatalf("OpenFuyao node %s is not destructive: release=%+v err=%v", node.ID, release, releaseErr)
		}
		playbook := release.Actions[0].Playbook
		if stagePlaybooks[playbook] || !strings.HasSuffix(playbook, ".platform.yml") {
			t.Fatalf("OpenFuyao node %s does not use a unique stage adapter: %s", node.ID, playbook)
		}
		stagePlaybooks[playbook] = true
	}
	environments, err := database.ListEnvironments(ctx)
	if err != nil || len(environments) != 2 {
		t.Fatalf("environments=%d err=%v", len(environments), err)
	}
	assertTableCount(t, database, "component_releases", 15)
	assertTableCount(t, database, "component_dependencies", 7)
	assertTableCount(t, database, "action_definitions", 9)
	assertTableCount(t, database, "scenarios", 2)
	assertTableCount(t, database, "scenario_revisions", 2)
	assertTableCount(t, database, "environment_revisions", 2)
	assertTableCount(t, database, "audit_events", 2)
}

func TestKubernetes1175SeedRegistersLinearDestructiveJob(t *testing.T) {
	ctx, database := seededDatabase(t)

	type expectedStage struct {
		releaseID, upstreamID, playbook, group string
	}
	stages := []expectedStage{
		{releaseID: "release-k8s-1.17.5-cert", playbook: "k8s-1.17.5-cluster/cert_1175.yml", group: "k8s_cert_controller"},
		{releaseID: "release-k8s-1.17.5-etcd", upstreamID: "release-k8s-1.17.5-cert", playbook: "k8s-1.17.5-cluster/etcd_serverless.yml", group: "k8setcd"},
		{releaseID: "release-k8s-1.17.5-master", upstreamID: "release-k8s-1.17.5-etcd", playbook: "k8s-1.17.5-cluster/master_1175.yml", group: "k8smaster"},
		{releaseID: "release-k8s-1.17.5-node", upstreamID: "release-k8s-1.17.5-master", playbook: "k8s-1.17.5-cluster/node_1175.yml", group: "k8snode"},
	}
	for _, stage := range stages {
		release, err := database.GetComponentRelease(ctx, stage.releaseID)
		if err != nil {
			t.Fatalf("get %s: %v", stage.releaseID, err)
		}
		if release.Version != "v1.17.5" || release.Type != domain.ReleaseAtomic || release.Status != domain.ReleaseReleased || release.Verified {
			t.Fatalf("unexpected release contract for %s: %+v", stage.releaseID, release)
		}
		if release.RiskLevel != domain.RiskDestructive || len(release.Actions) != 1 {
			t.Fatalf("unexpected actions for %s: %+v", stage.releaseID, release.Actions)
		}
		action := release.Actions[0]
		if action.Kind != domain.ActionInstall || action.Playbook != stage.playbook || action.Limit != stage.group || action.HostGroup != stage.group || len(action.Tags) != 1 || action.Tags[0] != "install" {
			t.Fatalf("unexpected action for %s: %+v", stage.releaseID, action)
		}
		if !action.Destructive || !action.NeedsApproval() || action.RiskLevel != domain.RiskDestructive {
			t.Fatalf("action does not require approval for %s: %+v", stage.releaseID, action)
		}
		if filepath.IsAbs(action.Playbook) || strings.Contains(action.Playbook, "..") || !strings.HasPrefix(action.Playbook, "k8s-1.17.5-cluster/") {
			t.Fatalf("unsafe playbook path for %s: %q", stage.releaseID, action.Playbook)
		}
		if stage.upstreamID == "" {
			if len(release.Dependencies) != 0 {
				t.Fatalf("certificate stage unexpectedly has dependencies: %+v", release.Dependencies)
			}
		} else if len(release.Dependencies) != 1 || release.Dependencies[0].UpstreamReleaseID != stage.upstreamID {
			t.Fatalf("dependency for %s=%+v, want upstream %s", stage.releaseID, release.Dependencies, stage.upstreamID)
		}
		properties, _ := release.ParameterSchema["properties"].(map[string]any)
		if _, leaked := properties["K8S_ENCRYPTION_KEY"]; leaked {
			t.Fatalf("sensitive encryption key was persisted in schema for %s", stage.releaseID)
		}
	}

	revision, err := database.GetScenarioRevision(ctx, "scenario-k8s-1.17.5-r1")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != domain.RevisionDraft || len(revision.Graph.Nodes) != 4 || len(revision.Graph.Edges) != 3 {
		t.Fatalf("scenario revision=%+v", revision)
	}
	if issues := domain.ValidateGraph(revision.Graph); len(issues) != 0 {
		t.Fatalf("scenario graph issues=%+v", issues)
	}
	for index, stage := range stages {
		node := revision.Graph.Nodes[index]
		if node.ReleaseID != stage.releaseID || node.HostGroup != stage.group || node.Action != domain.ActionInstall || !node.Destructive {
			t.Fatalf("node %d=%+v", index, node)
		}
		if index > 0 {
			edge := revision.Graph.Edges[index-1]
			if edge.Source != revision.Graph.Nodes[index-1].ID || edge.Target != node.ID {
				t.Fatalf("edge %d=%+v does not preserve stage order", index-1, edge)
			}
		}
	}
}

func TestKubernetes1175EnvironmentIsSanitizedAndComplete(t *testing.T) {
	ctx, database := seededDatabase(t)
	environment, err := database.GetEnvironment(ctx, "environment-k8s-1.17.5-template", false)
	if err != nil || environment.Revision == nil {
		t.Fatalf("environment=%+v err=%v", environment, err)
	}
	revision := environment.Revision
	if revision.Facts["architecture"] != "amd64" || revision.Facts["os"] != "SUSE" || revision.Facts["network"] != "IPv4" {
		t.Fatalf("facts=%+v", revision.Facts)
	}
	var inventory struct {
		Hosts []struct {
			Name    string   `json:"name"`
			Address string   `json:"address"`
			Groups  []string `json:"groups"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(revision.Inventory, &inventory); err != nil {
		t.Fatal(err)
	}
	groupCounts := map[string]int{}
	for _, host := range inventory.Hosts {
		if host.Address != "127.0.0.1" && !strings.HasPrefix(host.Address, "192.0.2.") {
			t.Fatalf("inventory contains a non-TEST-NET address: %+v", host)
		}
		for _, group := range host.Groups {
			groupCounts[group]++
		}
	}
	for group, minimum := range map[string]int{"k8s_cert_controller": 1, "k8setcd": 3, "k8smaster": 3, "k8snode": 1, "k8s_F5": 1} {
		if groupCounts[group] < minimum {
			t.Fatalf("group %s has %d hosts, want at least %d", group, groupCounts[group], minimum)
		}
	}
	if revision.Parameters["K8S_VERSION"] != "v1.17.5" || revision.Parameters["K8S1175_ARTIFACTS_VERIFIED"] != false || revision.Parameters["K8S1175_DOCKER_RUNTIME_VERIFIED"] != false || revision.Parameters["K8S1175_DOCKER_VERSION"] != "18.09.7" || revision.Parameters["ENABLE_VM_CHECK"] != false {
		t.Fatalf("unsafe version or verification parameters: %+v", revision.Parameters)
	}
	for _, key := range []string{"K8SMASTER_1175_CERT", "K8SNODE_1175_CERT"} {
		value, _ := revision.Parameters[key].(string)
		if !strings.Contains(value, "1.17.5") || strings.Contains(value, "://") || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
			t.Fatalf("unsafe or ambiguous media path %s=%q", key, value)
		}
	}
	if _, leaked := revision.Parameters["K8S_ENCRYPTION_KEY"]; leaked {
		t.Fatal("encryption key must not be stored in ordinary parameters")
	}
	if len(revision.CredentialRefs) != 1 {
		t.Fatalf("credential refs=%+v", revision.CredentialRefs)
	}
	credential := revision.CredentialRefs[0]
	if credential.Name != "K8S_ENCRYPTION_KEY" || credential.Kind != "envVarRef" || credential.Reference != "NEWPLATFORM_K8S1175_ENCRYPTION_KEY" {
		t.Fatalf("credential ref=%+v", credential)
	}
}

func TestKubernetes1175SnapshotIsSanitizedAndComplete(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	snapshotRoot := filepath.Join(repositoryRoot, "examples", "ansible", "k8s-1.17.5-cluster")
	required := []string{
		"cert_1175.yml", "etcd_serverless.yml", "master_1175.yml", "node_1175.yml",
		"preflight_1175.yml", "docker_runtime_preflight.yml", "varutil.yml", "vm_check.yml", "vm_check2.yml", "f5_check.yml",
		filepath.Join("roles", "k8s1175_cert", "tasks", "main.yml"),
		filepath.Join("roles", "k8setcd_serverless", "tasks", "main.yml"),
		filepath.Join("roles", "k8s1175master", "tasks", "main.yml"),
		filepath.Join("roles", "k8s1175node", "tasks", "main.yml"),
		filepath.Join("roles", "k8s1175master", "templates", "encryption-config.yaml.j2"),
	}
	for _, relative := range required {
		if _, err := os.Stat(filepath.Join(snapshotRoot, relative)); err != nil {
			t.Fatalf("required Kubernetes 1.17.5 snapshot file %s: %v", relative, err)
		}
	}

	forbiddenNames := map[string]bool{
		"ca-key.pem": true, "ca.pem": true, "cb-ca.pem": true,
		"encryption-config.yaml": true, "jupyterhub.yaml": true, "hosts": true,
		"node_1175_clean.yml": true,
	}
	err := filepath.WalkDir(snapshotRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(snapshotRoot, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "group_vars" {
				t.Errorf("forbidden environment variables directory was copied: %s", relative)
			}
			return nil
		}
		if forbiddenNames[entry.Name()] {
			t.Errorf("forbidden source or secret-bearing file was copied: %s", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(contents)
		if strings.Contains(text, "PRIVATE KEY-----") {
			t.Errorf("private-key material found in snapshot: %s", relative)
		}
		if entry.Name() != "SOURCE.md" {
			if strings.Contains(text, "K8SMASTER_1173_CERT") || strings.Contains(text, "K8SNODE_1173_CERT") {
				t.Errorf("unverified legacy 1.17.3 artifact contract remains in %s", relative)
			}
			if strings.Contains(text, "/approot1/paas/admin/app/ansible/project/k8s_cluster/") {
				t.Errorf("snapshot helper still reads source-repository resources: %s", relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSeederAddsKubernetes1175ToExistingDatabaseWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	seeder := Seeder{Store: database, Now: func() time.Time { return now }}
	if err := seeder.seedUsers(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := seeder.seedComponents(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := seeder.seedScenarios(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := seeder.seedEnvironments(ctx, now); err != nil {
		t.Fatal(err)
	}
	containerd, _ := database.GetComponent(ctx, "component-containerd", false)
	containerd.Name = "用户自定义 Runtime"
	containerd.Description = "do not overwrite"
	containerd.UpdatedAt = now.Add(time.Hour)
	if err := database.UpdateComponent(ctx, containerd); err != nil {
		t.Fatal(err)
	}
	if err := database.AppendAudit(ctx, domain.AuditEvent{
		ID: "audit-demo-seeded", ActorID: "system", Action: "user.preserved", ResourceType: "platform", ResourceID: "newplatform-demo", Metadata: map[string]any{"preserved": true}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := seeder.Run(ctx); err != nil {
		t.Fatalf("incremental seed: %v", err)
	}
	assertTableCount(t, database, "components", 15)
	assertTableCount(t, database, "component_releases", 15)
	assertTableCount(t, database, "scenarios", 2)
	assertTableCount(t, database, "environments", 2)
	if _, err := database.GetComponentRelease(ctx, "release-k8s-1.17.5-node"); err != nil {
		t.Fatalf("Kubernetes 1.17.5 seed was not added: %v", err)
	}
	preserved, err := database.GetComponent(ctx, containerd.ID, false)
	if err != nil || preserved.Name != containerd.Name || preserved.Description != containerd.Description {
		t.Fatalf("existing component was overwritten: %+v err=%v", preserved, err)
	}
	audits, err := database.ListAudit(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	seenBaseAudit := 0
	for _, event := range audits {
		if event.ID == "audit-demo-seeded" {
			seenBaseAudit++
			if event.Action != "user.preserved" {
				t.Fatalf("existing append-only audit was replaced: %+v", event)
			}
		}
	}
	if seenBaseAudit != 1 {
		t.Fatalf("base audit count=%d", seenBaseAudit)
	}
	if err := seeder.Run(ctx); err != nil {
		t.Fatalf("repeat incremental seed: %v", err)
	}
	assertTableCount(t, database, "components", 15)
	assertTableCount(t, database, "component_releases", 15)
	assertTableCount(t, database, "scenarios", 2)
	assertTableCount(t, database, "environments", 2)
	assertTableCount(t, database, "audit_events", 2)
}

func TestSeederRepairsOnlyEmptyKubernetes1175ActionTags(t *testing.T) {
	ctx, database := seededDatabase(t)
	if _, err := database.DB().ExecContext(ctx, `UPDATE action_definitions SET tags_json='null' WHERE id='action-k8s-1-17-5-cert-install'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `UPDATE action_definitions SET tags_json='["operator-reviewed"]' WHERE id='action-k8s-1-17-5-etcd-install'`); err != nil {
		t.Fatal(err)
	}

	seeder := Seeder{Store: database, Now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }}
	if err := seeder.Run(ctx); err != nil {
		t.Fatal(err)
	}
	cert, err := database.GetComponentRelease(ctx, "release-k8s-1.17.5-cert")
	if err != nil || len(cert.Actions) != 1 || len(cert.Actions[0].Tags) != 1 || cert.Actions[0].Tags[0] != "install" {
		t.Fatalf("legacy empty tags were not repaired: release=%+v err=%v", cert, err)
	}
	etcd, err := database.GetComponentRelease(ctx, "release-k8s-1.17.5-etcd")
	if err != nil || len(etcd.Actions) != 1 || len(etcd.Actions[0].Tags) != 1 || etcd.Actions[0].Tags[0] != "operator-reviewed" {
		t.Fatalf("explicit action tags were overwritten: release=%+v err=%v", etcd, err)
	}
}

func TestSeederAddsMissingKubernetes1175RuntimeContractWithoutOverwriting(t *testing.T) {
	ctx, database := seededDatabase(t)
	legacySchema := `{"type":"object","properties":{"operatorField":{"type":"string"},"K8S1175_DOCKER_VERSION":{"type":"string","default":"operator-reviewed"}}}`
	if _, err := database.DB().ExecContext(ctx, `UPDATE component_releases SET parameter_schema_json=? WHERE id='release-k8s-1.17.5-master'`, legacySchema); err != nil {
		t.Fatal(err)
	}
	legacyParameters := `{"operatorField":"preserve","K8S1175_DOCKER_VERSION":"operator-reviewed"}`
	if _, err := database.DB().ExecContext(ctx, `UPDATE environment_revisions SET parameters_json=? WHERE id='environment-k8s-1.17.5-template-r1'`, legacyParameters); err != nil {
		t.Fatal(err)
	}

	seeder := Seeder{Store: database, Now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }}
	if err := seeder.Run(ctx); err != nil {
		t.Fatal(err)
	}
	release, err := database.GetComponentRelease(ctx, "release-k8s-1.17.5-master")
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := release.ParameterSchema["properties"].(map[string]any)
	version, _ := properties["K8S1175_DOCKER_VERSION"].(map[string]any)
	if _, ok := properties["K8S1175_DOCKER_RUNTIME_VERIFIED"]; !ok || version["default"] != "operator-reviewed" || properties["operatorField"] == nil {
		t.Fatalf("release runtime contract was not merged safely: %+v", properties)
	}
	revision, err := database.GetEnvironmentRevision(ctx, "environment-k8s-1.17.5-template-r1")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Parameters["K8S1175_DOCKER_RUNTIME_VERIFIED"] != false || revision.Parameters["K8S1175_DOCKER_VERSION"] != "operator-reviewed" || revision.Parameters["operatorField"] != "preserve" {
		t.Fatalf("environment runtime contract was not merged safely: %+v", revision.Parameters)
	}
}

func seededDatabase(t *testing.T) (context.Context, *store.Store) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	seeder := Seeder{Store: database, Now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }}
	if err := seeder.Run(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, database
}

func assertTableCount(t *testing.T, database *store.Store, table string, want int) {
	t.Helper()
	var count int
	if err := database.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s=%d, want %d", table, count, want)
	}
}

func TestOpenFuyaoSnapshotAndJobReferences(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	snapshotRoot := filepath.Join(repositoryRoot, "examples", "ansible", "openfuyao")
	count := 0
	err := filepath.WalkDir(snapshotRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() != "SOURCE.md" && !strings.HasPrefix(entry.Name(), "component-bke-") {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 105 {
		t.Fatalf("OpenFuyao original files=%d, want 105", count)
	}
	for _, path := range []string{
		filepath.Join(snapshotRoot, "build_manager_cluster.yml"),
		filepath.Join(snapshotRoot, "component-bke-cert.platform.yml"),
		filepath.Join(snapshotRoot, "component-bke-bootstrap.platform.yml"),
		filepath.Join(snapshotRoot, "component-bke-common.platform.yml"),
		filepath.Join(snapshotRoot, "component-bke-addon.platform.yml"),
		filepath.Join(snapshotRoot, "component-bke-master.platform.yml"),
		filepath.Join(repositoryRoot, "examples", "ansible", "k8s-1.17.5-cluster", "cert_1175.yml"),
		filepath.Join(repositoryRoot, "examples", "ansible", "k8s-1.17.5-cluster", "etcd_serverless.yml"),
		filepath.Join(repositoryRoot, "examples", "ansible", "k8s-1.17.5-cluster", "master_1175.yml"),
		filepath.Join(repositoryRoot, "examples", "ansible", "k8s-1.17.5-cluster", "node_1175.yml"),
	} {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Fatalf("job reference is missing: %s", path)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
