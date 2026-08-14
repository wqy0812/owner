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

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

type seedRunner struct{}

func (seedRunner) Run(context.Context, ansiblerunner.Request) (ansiblerunner.Result, error) {
	return ansiblerunner.Result{}, nil
}

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
	if err != nil || len(components) != 34 {
		t.Fatalf("components=%d err=%v", len(components), err)
	}
	for _, component := range components {
		if err := domain.ValidateComponentClassification(component); err != nil {
			t.Fatalf("component %s classification: %v", component.ID, err)
		}
	}
	for id, wantLayer := range map[string]domain.ComponentLayer{
		"component-bke-cert": domain.LayerHostFoundation, "component-cluster-pki": domain.LayerHostFoundation,
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
	for _, node := range open.Graph.Nodes {
		release, releaseErr := database.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil || len(release.Actions) == 0 || !release.Actions[0].NeedsApproval() {
			t.Fatalf("OpenFuyao node %s is not destructive: release=%+v err=%v", node.ID, release, releaseErr)
		}
		install := release.Actions[0]
		for _, action := range release.Actions {
			if action.Kind == domain.ActionInstall {
				install = action
				break
			}
		}
		if len(install.RequiredCredentials) == 0 || !strings.HasSuffix(install.Playbook, ".platform.yml") {
			t.Fatalf("OpenFuyao node %s has an incomplete action contract: %+v", node.ID, install)
		}
	}
	environments, err := database.ListEnvironments(ctx)
	if err != nil || len(environments) != 2 {
		t.Fatalf("environments=%d err=%v", len(environments), err)
	}
	assertTableCount(t, database, "component_releases", 37)
	assertTableCount(t, database, "component_dependencies", 47)
	assertTableCount(t, database, "action_definitions", 57)
	assertTableCount(t, database, "scenarios", 5)
	assertTableCount(t, database, "scenario_revisions", 5)
	assertTableCount(t, database, "environment_revisions", 2)
	assertTableCount(t, database, "audit_events", 2)
}

func TestOpenFuyaoSeedDefinesCompleteContractsAndThreeIndependentDAGs(t *testing.T) {
	ctx, database := seededDatabase(t)
	owner, _ := database.GetUser(ctx, ScenarioOwnerID)
	platform := service.NewPlatform(database, seedRunner{}, nil)
	defer platform.Close()
	environment, err := database.GetEnvironment(ctx, "environment-openfuyao-template", false)
	if err != nil || environment.Revision == nil || len(environment.Revision.CredentialRefs) != 5 {
		t.Fatalf("OpenFuyao environment contract=%+v err=%v", environment.Revision, err)
	}
	var inventory struct {
		Hosts []struct {
			Address string   `json:"address"`
			Groups  []string `json:"groups"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, host := range inventory.Hosts {
		if !strings.HasPrefix(host.Address, "192.0.2.") {
			t.Fatalf("OpenFuyao inventory contains non-TEST-NET address: %s", host.Address)
		}
		for _, group := range host.Groups {
			groups[group] = true
		}
	}
	for _, group := range []string{"bootstrap_host", "management_cluster_k8smaster", "work_cluster_k8smaster", "work_cluster_k8snode"} {
		if !groups[group] {
			t.Fatalf("OpenFuyao inventory is missing group %s", group)
		}
	}
	for _, section := range []string{"operation", "network", "versions", "artifact_sources", "certificates", "addon_params"} {
		if _, ok := environment.Revision.Parameters[section].(map[string]any); !ok {
			t.Fatalf("OpenFuyao environment is missing grouped parameter section %s", section)
		}
	}
	parametersJSON, _ := json.Marshal(environment.Revision.Parameters)
	for _, forbidden := range []string{"callback_url", "callback_token", "task_id", "ENV_DOCKER_SECRET_PASSWORD", "ENV_CHART_PULL_PASSWORD"} {
		if strings.Contains(string(parametersJSON), forbidden) {
			t.Fatalf("OpenFuyao environment parameters contain forbidden key %s", forbidden)
		}
	}

	scenarios := []struct {
		revisionID, role, clusterID string
		nodes, steps                int
	}{
		{"scenario-openfuyao-r1", "manager", "demo-management-cluster", 5, 6},
		{"scenario-openfuyao-work-cluster-r1", "work", "demo-work-cluster", 4, 5},
		{"scenario-openfuyao-work-nodes-r1", "work", "demo-work-cluster", 2, 2},
	}
	for _, expectation := range scenarios {
		revision, err := database.GetScenarioRevision(ctx, expectation.revisionID)
		if err != nil || len(revision.Graph.Nodes) != expectation.nodes || len(revision.Graph.Edges) != expectation.nodes-1 {
			t.Fatalf("scenario %s graph nodes=%d edges=%d err=%v", expectation.revisionID, len(revision.Graph.Nodes), len(revision.Graph.Edges), err)
		}
		if issues := domain.ValidateGraph(revision.Graph); len(issues) != 0 {
			t.Fatalf("scenario %s graph issues=%+v", expectation.revisionID, issues)
		}
		issues, err := platform.ValidateScenario(ctx, owner, expectation.revisionID)
		if err != nil || len(issues) != 0 {
			t.Fatalf("scenario %s dependency issues=%+v err=%v", expectation.revisionID, issues, err)
		}
		for _, node := range revision.Graph.Nodes {
			if len(node.RunInputs) != 0 {
				t.Fatalf("scenario %s node %s exposes unsafe run inputs: %v", expectation.revisionID, node.ID, node.RunInputs)
			}
		}

		run, err := platform.StartScenarioTest(ctx, owner, expectation.revisionID, "environment-openfuyao-template", nil)
		if err != nil || run.Status != domain.RunAwaitingApproval {
			t.Fatalf("scenario %s run=%+v err=%v", expectation.revisionID, run, err)
		}
		steps, ok := run.InputSnapshot["steps"].([]any)
		if !ok || len(steps) != expectation.steps {
			t.Fatalf("scenario %s locked steps=%#v", expectation.revisionID, run.InputSnapshot["steps"])
		}
		snapshotJSON, _ := json.Marshal(run.InputSnapshot)
		if strings.Contains(string(snapshotJSON), "NEWPLATFORM_OPENFUYAO_") {
			t.Fatalf("scenario %s snapshot retained a credential reference target", expectation.revisionID)
		}
		for _, raw := range steps {
			step := raw.(map[string]any)
			variables := step["variables"].(map[string]any)
			if variables["cluster_role"] != expectation.role || variables["cluster_id"] != expectation.clusterID {
				t.Fatalf("scenario %s variables role=%v cluster=%v", expectation.revisionID, variables["cluster_role"], variables["cluster_id"])
			}
			if variables["target_host_group"] == "" || variables["strategy"] != "StatelessFlatNetworkStrategy" {
				t.Fatalf("scenario %s incomplete locked variables=%#v", expectation.revisionID, variables)
			}
			for credentialName := range map[string]bool{"ansible_ssh_pass": true, "ENV_DOCKER_SECRET_PASSWORD": true, "ENV_CHART_PULL_PASSWORD": true} {
				if _, leaked := variables[credentialName]; leaked {
					t.Fatalf("scenario %s leaked credential %s into locked variables", expectation.revisionID, credentialName)
				}
			}
		}
	}

	enrollment, _ := database.GetScenarioRevision(ctx, "scenario-openfuyao-work-nodes-r1")
	if enrollment.Graph.Nodes[0].Action != domain.ActionVerify || enrollment.Graph.Nodes[0].ReleaseID != "release-bke-master-25.12" || enrollment.Graph.Nodes[1].ReleaseID != "release-bke-nodes-25.12" {
		t.Fatalf("work node enrollment boundary=%+v", enrollment.Graph.Nodes)
	}

	credentialNames := map[string]bool{
		"ansible_ssh_pass": true, "ENV_DOCKER_SECRET_USERNAME": true, "ENV_DOCKER_SECRET_PASSWORD": true,
		"ENV_CHART_PULL_USERNAME": true, "ENV_CHART_PULL_PASSWORD": true,
	}
	environmentOwner, _ := database.GetUser(ctx, EnvironmentOwnerID)
	componentDefaults := map[string]struct{ role, group, clusterID string }{
		"release-bke-cert-25.12":      {group: "bootstrap_host", clusterID: "demo-management-cluster"},
		"release-bke-bootstrap-25.12": {group: "bootstrap_host", clusterID: "demo-management-cluster"},
		"release-bke-common-25.12":    {group: "management_cluster_k8smaster"},
		"release-bke-addon-25.12":     {group: "management_cluster_k8smaster"},
		"release-bke-master-25.12":    {role: "manager", group: "management_cluster_k8smaster", clusterID: "demo-management-cluster"},
		"release-bke-nodes-25.12":     {role: "work", group: "work_cluster_k8snode", clusterID: "demo-work-cluster"},
	}
	for _, releaseID := range []string{"release-bke-cert-25.12", "release-bke-bootstrap-25.12", "release-bke-common-25.12", "release-bke-addon-25.12", "release-bke-master-25.12", "release-bke-nodes-25.12"} {
		release, err := database.GetComponentRelease(ctx, releaseID)
		if err != nil {
			t.Fatal(err)
		}
		properties, _ := release.ParameterSchema["properties"].(map[string]any)
		if len(properties) == 0 {
			t.Fatalf("release %s has empty schema", releaseID)
		}
		for name := range credentialNames {
			if _, leaked := properties[name]; leaked {
				t.Fatalf("credential %s leaked into release %s schema", name, releaseID)
			}
		}
		for _, action := range release.Actions {
			if len(action.RequiredCredentials) == 0 {
				t.Fatalf("release %s action %s has no credential contract", releaseID, action.ID)
			}
			for _, name := range action.RequiredCredentials {
				if !credentialNames[name] {
					t.Fatalf("release %s action %s declares unknown credential %s", releaseID, action.ID, name)
				}
			}
		}
		componentRun, err := platform.StartComponentTest(ctx, environmentOwner, releaseID, environment.ID, nil)
		if err != nil || componentRun.Status != domain.RunAwaitingApproval {
			t.Fatalf("component test %s run=%+v err=%v", releaseID, componentRun, err)
		}
		steps := componentRun.InputSnapshot["steps"].([]any)
		variables := steps[0].(map[string]any)["variables"].(map[string]any)
		defaults := componentDefaults[releaseID]
		if variables["target_host_group"] != defaults.group {
			t.Fatalf("component test %s target_host_group=%v want=%s", releaseID, variables["target_host_group"], defaults.group)
		}
		if defaults.role != "" && variables["cluster_role"] != defaults.role {
			t.Fatalf("component test %s cluster_role=%v want=%s", releaseID, variables["cluster_role"], defaults.role)
		}
		if defaults.clusterID != "" && variables["cluster_id"] != defaults.clusterID {
			t.Fatalf("component test %s cluster_id=%v want=%s", releaseID, variables["cluster_id"], defaults.clusterID)
		}
	}
}

func TestKubernetes1175SeedRegistersMinimalCatalogAndReusableDAGs(t *testing.T) {
	ctx, database := seededDatabase(t)
	for _, legacyID := range []string{"component-k8s-1.17.5-cert", "component-k8s-1.17.5-etcd", "component-k8s-1.17.5-master", "component-k8s-1.17.5-node"} {
		if _, err := database.GetComponent(ctx, legacyID, false); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("legacy component %s still exists: %v", legacyID, err)
		}
	}
	for _, spec := range kubernetes1175ReleaseSpecs {
		release, err := database.GetComponentRelease(ctx, spec.releaseID)
		if err != nil || release.Verified {
			t.Fatalf("new release %s must exist as unverified: %+v err=%v", spec.releaseID, release, err)
		}
		if strings.Contains(spec.releaseID, "source-6909da3") && release.Version != "source-6909da3" {
			t.Fatalf("unknown source version was fabricated for %s: %s", spec.releaseID, release.Version)
		}
	}
	for _, releaseID := range []string{
		"release-host-preflight-k8s-1.17.5", "release-host-bootstrap-k8s-1.17.5", "release-cluster-pki-k8s-1.17.5",
		"release-docker-18.09.7", "release-etcd-3.3.10", "release-kubernetes-distribution-1.17.5",
		"release-kubernetes-encryption-config-1.17.5", "release-kube-apiserver-1.17.5",
		"release-kube-controller-manager-1.17.5", "release-kube-scheduler-1.17.5",
		"release-kubernetes-bootstrap-rbac-1.17.5", "release-flannel-0.11.0", "release-kubelet-1.17.5",
		"release-kube-proxy-1.17.5", "release-coredns-1.3.1", "release-node-exporter-0.18.0",
		"release-metrics-server-0.3.1", "release-glusterfs-client-3.12.6", "release-amc-source-6909da3",
	} {
		release, err := database.GetComponentRelease(ctx, releaseID)
		if err != nil {
			t.Fatalf("get %s: %v", releaseID, err)
		}
		wantType := domain.ReleaseAtomic
		if releaseID == "release-kubernetes-distribution-1.17.5" {
			wantType = domain.ReleaseBundle
		}
		if release.Type != wantType || release.Status != domain.ReleaseReleased || release.Verified || len(release.Actions) != 2 {
			t.Fatalf("unexpected release contract for %s: %+v", releaseID, release)
		}
		primary, verify := release.Actions[0], release.Actions[1]
		if primary.Kind == domain.ActionPreflight {
			if primary.Destructive || primary.NeedsApproval() {
				t.Fatalf("host preflight unexpectedly destructive: %+v", primary)
			}
		} else if !primary.Destructive || !primary.NeedsApproval() {
			t.Fatalf("writing action does not require approval: %+v", primary)
		}
		if verify.Kind != domain.ActionVerify || verify.Destructive || verify.NeedsApproval() {
			t.Fatalf("invalid verify action for %s: %+v", releaseID, verify)
		}
		for _, action := range release.Actions {
			if filepath.IsAbs(action.Playbook) || strings.Contains(action.Playbook, "..") || !strings.HasPrefix(action.Playbook, "k8s-1.17.5-cluster/components/") {
				t.Fatalf("unsafe component entrypoint for %s: %q", releaseID, action.Playbook)
			}
		}
		properties, _ := release.ParameterSchema["properties"].(map[string]any)
		if _, leaked := properties["K8S_ENCRYPTION_KEY"]; leaked {
			t.Fatalf("sensitive encryption key was persisted in schema for %s", releaseID)
		}
	}

	for _, expectation := range []struct {
		id    string
		nodes int
	}{{"scenario-k8s-1.17.5-r1", 21}, {"scenario-k8s-1.17.5-extended-r1", 39}} {
		revision, err := database.GetScenarioRevision(ctx, expectation.id)
		if err != nil || revision.Status != domain.RevisionDraft || len(revision.Graph.Nodes) != expectation.nodes {
			t.Fatalf("scenario %s nodes=%d status=%s err=%v", expectation.id, len(revision.Graph.Nodes), revision.Status, err)
		}
		if issues := domain.ValidateGraph(revision.Graph); len(issues) != 0 {
			t.Fatalf("scenario %s graph issues=%+v", expectation.id, issues)
		}
		owner, _ := database.GetUser(ctx, ScenarioOwnerID)
		platform := service.NewPlatform(database, nil, nil)
		issues, validateErr := platform.ValidateScenario(ctx, owner, expectation.id)
		platform.Close()
		if validateErr != nil || len(issues) != 0 {
			t.Fatalf("scenario %s dependency validation issues=%+v err=%v", expectation.id, issues, validateErr)
		}
	}
	core, _ := database.GetScenarioRevision(ctx, "scenario-k8s-1.17.5-r1")
	usage := map[string]map[string]bool{}
	for _, node := range core.Graph.Nodes {
		if usage[node.ReleaseID] == nil {
			usage[node.ReleaseID] = map[string]bool{}
		}
		usage[node.ReleaseID][node.HostGroup] = true
	}
	for _, releaseID := range []string{"release-host-bootstrap-k8s-1.17.5", "release-docker-18.09.7", "release-kubernetes-distribution-1.17.5", "release-flannel-0.11.0", "release-kubelet-1.17.5", "release-kube-proxy-1.17.5"} {
		if len(usage[releaseID]) != 2 {
			t.Fatalf("release %s was not reused across control and worker host groups: %+v", releaseID, usage[releaseID])
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
	for _, key := range []string{"ETCD_MEDPATH_SERVERLESS", "FLANNEL_MEDPATH_SERVERLESS", "NODE_EXPORTER_MEDPATH_AMD"} {
		value, _ := revision.Parameters[key].(string)
		if value == "" || strings.Contains(value, "://") || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
			t.Fatalf("unsafe or missing media path %s=%q", key, value)
		}
	}
	for _, key := range []string{"K8SMASTER_1175_CERT_SHA256", "K8SNODE_1175_CERT_SHA256", "ETCD_MEDPATH_SERVERLESS_SHA256", "FLANNEL_MEDPATH_SERVERLESS_SHA256", "COREDNS_IMAGE_DIGEST", "METRICS_SERVER_IMAGE_DIGEST"} {
		if revision.Parameters[key] != "" {
			t.Fatalf("unknown verification value %s must remain empty, got %v", key, revision.Parameters[key])
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
		filepath.Join("components", "host-preflight.yml"), filepath.Join("components", "host-preflight-verify.yml"),
		filepath.Join("components", "kube-apiserver.yml"), filepath.Join("components", "kube-controller-manager.yml"),
		filepath.Join("components", "kube-scheduler.yml"), filepath.Join("components", "kubelet.yml"),
		filepath.Join("components", "kube-proxy.yml"), filepath.Join("components", "node-exporter.yml"),
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
	assertTableCount(t, database, "components", 34)
	assertTableCount(t, database, "component_releases", 37)
	assertTableCount(t, database, "scenarios", 5)
	assertTableCount(t, database, "environments", 2)
	if _, err := database.GetComponentRelease(ctx, "release-kubelet-1.17.5"); err != nil {
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
	assertTableCount(t, database, "components", 34)
	assertTableCount(t, database, "component_releases", 37)
	assertTableCount(t, database, "scenarios", 5)
	assertTableCount(t, database, "environments", 2)
	assertTableCount(t, database, "audit_events", 2)
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
		filepath.Join(snapshotRoot, "component-bke-master-verify.platform.yml"),
		filepath.Join(snapshotRoot, "component-bke-nodes.platform.yml"),
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
