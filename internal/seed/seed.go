package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

const (
	ComponentOwnerRuntimeID = "component-alice"
	ComponentOwnerK8sID     = "component-bob"
	ScenarioOwnerID         = "scenario-carol"
	EnvironmentOwnerID      = "environment-dave"
)

type Seeder struct {
	Store *store.Store
	Now   func() time.Time
}

func (s Seeder) Run(ctx context.Context) error {
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
		ID: "audit-demo-seeded", ActorID: "system", Action: "demo.seeded",
		ResourceType: "platform", ResourceID: "newplatform-demo",
		Metadata: map[string]any{"openFuyaoSnapshot": "examples/ansible/openfuyao"}, CreatedAt: now,
	}); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Components(ctx, now); err != nil {
		return err
	}
	if err := s.upgradeKubernetes1175ActionTags(ctx); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Scenario(ctx, now); err != nil {
		return err
	}
	if err := s.seedKubernetes1175Environment(ctx, now); err != nil {
		return err
	}
	if err := s.upgradeKubernetes1175RuntimeContract(ctx); err != nil {
		return err
	}
	return s.appendAuditIfMissing(ctx, domain.AuditEvent{
		ID: "audit-k8s-1.17.5-demo-seeded", ActorID: "system", Action: "demo.kubernetes_1_17_5.seeded",
		ResourceType: "scenario", ResourceID: "scenario-k8s-1.17.5",
		Metadata: map[string]any{"snapshot": "examples/ansible/k8s-1.17.5-cluster", "verified": false}, CreatedAt: now,
	})
}

func (s Seeder) upgradeKubernetes1175ActionTags(ctx context.Context) error {
	// An early version of this fixture persisted nil Tags as JSON null. Restrict
	// the repair to the four fixed seed action IDs and empty legacy values so an
	// explicitly customized action is never rewritten by startup seeding.
	actionIDs := []string{
		"action-k8s-1-17-5-cert-install",
		"action-k8s-1-17-5-etcd-install",
		"action-k8s-1-17-5-master-install",
		"action-k8s-1-17-5-node-install",
	}
	for _, actionID := range actionIDs {
		if _, err := s.Store.DB().ExecContext(ctx, `UPDATE action_definitions SET tags_json='["install"]' WHERE id=? AND kind='install' AND (tags_json='null' OR tags_json='[]')`, actionID); err != nil {
			return fmt.Errorf("upgrade Kubernetes 1.17.5 action tags for %s: %w", actionID, err)
		}
	}
	return nil
}

func (s Seeder) upgradeKubernetes1175RuntimeContract(ctx context.Context) error {
	// These fields were added after the first 1.17.5 fixture was seeded. Add
	// only missing keys on the fixed seed resources; explicitly reviewed values
	// and unrelated user parameters remain untouched.
	runtimeProperties := map[string]any{
		"K8S1175_DOCKER_RUNTIME_VERIFIED": map[string]any{"type": "boolean", "default": false},
		"K8S1175_DOCKER_VERSION":          map[string]any{"type": "string", "default": "18.09.7"},
	}
	for _, releaseID := range []string{
		"release-k8s-1.17.5-cert",
		"release-k8s-1.17.5-etcd",
		"release-k8s-1.17.5-master",
		"release-k8s-1.17.5-node",
	} {
		release, err := s.Store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return fmt.Errorf("read Kubernetes 1.17.5 runtime contract for %s: %w", releaseID, err)
		}
		if release.ParameterSchema == nil {
			release.ParameterSchema = map[string]any{}
		}
		properties, _ := release.ParameterSchema["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
			release.ParameterSchema["properties"] = properties
		}
		changed := false
		for key, definition := range runtimeProperties {
			if _, exists := properties[key]; !exists {
				properties[key] = definition
				changed = true
			}
		}
		if changed {
			encoded, err := json.Marshal(release.ParameterSchema)
			if err != nil {
				return err
			}
			if _, err := s.Store.DB().ExecContext(ctx, `UPDATE component_releases SET parameter_schema_json=? WHERE id=?`, string(encoded), releaseID); err != nil {
				return fmt.Errorf("upgrade Kubernetes 1.17.5 release schema for %s: %w", releaseID, err)
			}
		}
	}

	revision, err := s.Store.GetEnvironmentRevision(ctx, "environment-k8s-1.17.5-template-r1")
	if err != nil {
		return fmt.Errorf("read Kubernetes 1.17.5 environment runtime contract: %w", err)
	}
	if revision.Parameters == nil {
		revision.Parameters = map[string]any{}
	}
	changed := false
	if _, exists := revision.Parameters["K8S1175_DOCKER_RUNTIME_VERIFIED"]; !exists {
		revision.Parameters["K8S1175_DOCKER_RUNTIME_VERIFIED"] = false
		changed = true
	}
	if _, exists := revision.Parameters["K8S1175_DOCKER_VERSION"]; !exists {
		revision.Parameters["K8S1175_DOCKER_VERSION"] = "18.09.7"
		changed = true
	}
	if changed {
		encoded, err := json.Marshal(revision.Parameters)
		if err != nil {
			return err
		}
		if _, err := s.Store.DB().ExecContext(ctx, `UPDATE environment_revisions SET parameters_json=? WHERE id=?`, string(encoded), revision.ID); err != nil {
			return fmt.Errorf("upgrade Kubernetes 1.17.5 environment parameters: %w", err)
		}
	}
	return nil
}

func (s Seeder) seedUsers(ctx context.Context, now time.Time) error {
	for _, user := range demoUsers(now) {
		if _, err := s.Store.GetUser(ctx, user.ID); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("check seed user %s: %w", user.ID, err)
		}
		if err := s.Store.UpsertUser(ctx, user); err != nil {
			return fmt.Errorf("seed user %s: %w", user.ID, err)
		}
	}
	return nil
}

func demoUsers(now time.Time) []domain.User {
	return []domain.User{
		{ID: ComponentOwnerRuntimeID, Name: "林晓 · Runtime", Role: domain.RoleComponentOwner, CreatedAt: now},
		{ID: ComponentOwnerK8sID, Name: "周工 · Kubernetes", Role: domain.RoleComponentOwner, CreatedAt: now},
		{ID: ScenarioOwnerID, Name: "陈晨 · 集群交付", Role: domain.RoleScenarioOwner, CreatedAt: now},
		{ID: EnvironmentOwnerID, Name: "王维 · 基础设施", Role: domain.RoleEnvironmentOwner, CreatedAt: now},
	}
}

type seededComponent struct {
	component domain.Component
	releases  []domain.ComponentRelease
}

func (s Seeder) seedComponents(ctx context.Context, now time.Time) error {
	constraints := map[string]any{"architecture": []any{"amd64"}, "ipFamily": []any{"ipv4"}, "operatingSystem": []any{"Kylin V10"}}
	plainRelease := func(id, componentID, version string) domain.ComponentRelease {
		releaseType := domain.ReleaseAtomic
		if componentID == "component-kubernetes" {
			releaseType = domain.ReleaseBundle
		}
		return domain.ComponentRelease{
			ID: id, ComponentID: componentID, Version: version, Type: releaseType, Status: domain.ReleaseReleased,
			Verified: true, RiskLevel: domain.RiskLow, EnvironmentConstraints: constraints, ParameterSchema: map[string]any{}, CreatedAt: now, ReleasedAt: ptr(now),
		}
	}
	components := []seededComponent{
		{component: component("component-containerd", "containerd", "containerd", "容器运行时，提供 CRI 能力。", ComponentOwnerRuntimeID, now), releases: []domain.ComponentRelease{plainRelease("release-containerd-2.1.1", "component-containerd", "v2.1.1")}},
		{component: component("component-etcd", "etcd", "etcd", "Kubernetes 控制面状态存储。", ComponentOwnerRuntimeID, now), releases: []domain.ComponentRelease{plainRelease("release-etcd-3.6.7", "component-etcd", "v3.6.7-of.1")}},
		{component: component("component-kubernetes", "kubernetes", "Kubernetes", "OpenFuyao 管理集群 Kubernetes 发行版。", ComponentOwnerK8sID, now), releases: []domain.ComponentRelease{plainRelease("release-kubernetes-1.34.3", "component-kubernetes", "v1.34.3-of.1")}},
		{component: component("component-calico", "calico", "Calico", "集群 CNI 网络组件。", ComponentOwnerK8sID, now), releases: []domain.ComponentRelease{plainRelease("release-calico-3.27.3", "component-calico", "v3.27.3-icbc")}},
		{component: component("component-coredns", "coredns", "CoreDNS", "集群 DNS 组件。", ComponentOwnerK8sID, now), releases: []domain.ComponentRelease{plainRelease("release-coredns-1.12.2", "component-coredns", "v1.12.2-of.1")}},
		{component: component("component-kube-proxy", "kube-proxy", "kube-proxy", "节点 Service 网络代理。", ComponentOwnerK8sID, now), releases: []domain.ComponentRelease{plainRelease("release-kube-proxy-1.34.3", "component-kube-proxy", "v1.34.3-of.1.icbc.harbor")}},
	}

	stage := func(id, slug, name, owner, group string, dependencies []domain.ComponentDependency) seededComponent {
		releaseID := "release-" + slug + "-25.12"
		release := domain.ComponentRelease{
			ID: releaseID, ComponentID: id, Version: "v25.12", Type: domain.ReleaseAtomic, Status: domain.ReleaseReleased,
			ReleaseNotes: "来自 OpenFuyao 管理集群搭建作业快照；依赖内部介质和合格主机。", Verified: false,
			RiskLevel: domain.RiskDestructive, EnvironmentConstraints: constraints,
			ParameterSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Dependencies:    dependencies,
			Actions: []domain.ActionDefinition{{
				ID: "action-" + slug + "-build", ReleaseID: releaseID, Name: "OpenFuyao " + slug + " (includes recovery)", Kind: domain.ActionInstall,
				Playbook: "openfuyao/component-" + slug + ".platform.yml", Tags: []string{"init", "image_plugin", "rcv", "ins"}, Limit: group, HostGroup: group,
				TimeoutSeconds: 3600, RiskLevel: domain.RiskDestructive, Destructive: true,
			}}, CreatedAt: now, ReleasedAt: ptr(now),
		}
		for i := range release.Dependencies {
			release.Dependencies[i].ID = "dependency-" + slug + "-" + fmt.Sprint(i+1)
			release.Dependencies[i].ReleaseID = releaseID
		}
		return seededComponent{component: component(id, slug, name, "OpenFuyao v25.12 搭建阶段（真实作业快照）。", owner, now), releases: []domain.ComponentRelease{release}}
	}
	cert := stage("component-bke-cert", "bke-cert", "BKE Certificates", ComponentOwnerRuntimeID, "bootstrap_host", nil)
	bootstrap := stage("component-bke-bootstrap", "bke-bootstrap", "BKE Bootstrap", ComponentOwnerRuntimeID, "bootstrap_host", []domain.ComponentDependency{{UpstreamComponentID: cert.component.ID, UpstreamReleaseID: cert.releases[0].ID, Purpose: "certificate bootstrap"}})
	common := stage("component-bke-common", "bke-common", "BKE Common", ComponentOwnerRuntimeID, "management_cluster_k8smaster", []domain.ComponentDependency{{UpstreamComponentID: bootstrap.component.ID, UpstreamReleaseID: bootstrap.releases[0].ID, Purpose: "bootstrap endpoint"}})
	addon := stage("component-bke-addon", "bke-addon", "BKE Addons", ComponentOwnerK8sID, "management_cluster_k8smaster", []domain.ComponentDependency{{UpstreamComponentID: common.component.ID, UpstreamReleaseID: common.releases[0].ID, Purpose: "cluster prerequisites"}})
	master := stage("component-bke-master", "bke-master", "BKE Management Cluster", ComponentOwnerK8sID, "management_cluster_k8smaster", []domain.ComponentDependency{{UpstreamComponentID: addon.component.ID, UpstreamReleaseID: addon.releases[0].ID, Purpose: "management addons"}})
	addon.releases[0].Type = domain.ReleaseBundle
	components = append(components, cert, bootstrap, common, addon, master)

	for _, item := range components {
		if err := s.createComponentIfMissing(ctx, item.component); err != nil {
			return fmt.Errorf("seed component %s: %w", item.component.ID, err)
		}
		for _, release := range item.releases {
			if err := s.createReleaseIfMissing(ctx, release); err != nil {
				return fmt.Errorf("seed release %s: %w", release.ID, err)
			}
		}
	}
	return nil
}

func component(id, slug, name, description, owner string, now time.Time) domain.Component {
	layer, category, kind, requiredness := componentClassification(id)
	return domain.Component{
		ID: id, Slug: slug, Name: name, Description: description,
		Layer: layer, Category: category, Kind: kind, Requiredness: requiredness,
		OwnerID: owner, CreatedAt: now, UpdatedAt: now,
	}
}

func componentClassification(id string) (domain.ComponentLayer, domain.ComponentCategory, domain.ComponentKind, domain.ComponentRequiredness) {
	switch id {
	case "component-bke-cert", "component-k8s-1.17.5-cert":
		return domain.LayerHostFoundation, domain.CategorySecurity, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-bke-bootstrap":
		return domain.LayerHostFoundation, domain.CategoryBootstrap, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-containerd":
		return domain.LayerRuntimeState, domain.CategoryRuntime, domain.ComponentSoftware, domain.RequiredProfile
	case "component-etcd":
		return domain.LayerRuntimeState, domain.CategoryStateStore, domain.ComponentSoftware, domain.RequiredCore
	case "component-k8s-1.17.5-etcd":
		return domain.LayerRuntimeState, domain.CategoryStateStore, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-kubernetes":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentSoftwareBundle, domain.RequiredCore
	case "component-kube-proxy":
		return domain.LayerOrchestrationCore, domain.CategoryNetwork, domain.ComponentSoftware, domain.RequiredProfile
	case "component-k8s-1.17.5-master":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-k8s-1.17.5-node":
		return domain.LayerOrchestrationCore, domain.CategoryWorker, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-calico":
		return domain.LayerClusterService, domain.CategoryNetwork, domain.ComponentSoftware, domain.RequiredProfile
	case "component-coredns":
		return domain.LayerClusterService, domain.CategoryDNS, domain.ComponentSoftware, domain.RequiredCore
	case "component-bke-common", "component-bke-master":
		return domain.LayerPlatformExtension, domain.CategoryPlatform, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-bke-addon":
		return domain.LayerPlatformExtension, domain.CategoryPlatform, domain.ComponentSoftwareBundle, domain.RequiredProfile
	default:
		panic("missing seed component classification: " + id)
	}
}

func (s Seeder) seedKubernetes1175Components(ctx context.Context, now time.Time) error {
	constraints := map[string]any{
		"architecture":    []any{"amd64"},
		"operatingSystem": []any{"SUSE"},
		"ipFamily":        []any{"IPv4"},
	}
	parameterSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"K8S_VERSION":                     map[string]any{"type": "string", "enum": []any{"v1.17.5"}, "default": "v1.17.5"},
			"K8S1175_ARTIFACTS_VERIFIED":      map[string]any{"type": "boolean", "default": false},
			"K8S1175_DOCKER_RUNTIME_VERIFIED": map[string]any{"type": "boolean", "default": false},
			"K8S1175_DOCKER_VERSION":          map[string]any{"type": "string", "default": "18.09.7"},
			"K8SMASTER_1175_CERT":             map[string]any{"type": "string", "default": "file-station/kubernetes-1.17.5/kubernetes-server-linux-amd64.tar.gz"},
			"K8SNODE_1175_CERT":               map[string]any{"type": "string", "default": "file-station/kubernetes-1.17.5/kubernetes-node-linux-amd64.tar.gz"},
			"IHS_IP":                          map[string]any{"type": "string", "default": "192.0.2.80"},
			"IHS_PORT":                        map[string]any{"type": "integer", "default": 8080},
			"ENABLE_VM_CHECK":                 map[string]any{"type": "boolean", "default": false},
		},
	}
	type stage struct {
		componentID, slug, name, description   string
		releaseID, playbook, hostGroup         string
		upstreamComponentID, upstreamReleaseID string
		purpose                                string
	}
	stages := []stage{
		{
			componentID: "component-k8s-1.17.5-cert", slug: "k8s-1-17-5-cert", name: "Kubernetes 1.17.5 Certificates",
			description: "Kubernetes 1.17.5 集群证书阶段安全适配快照。",
			releaseID:   "release-k8s-1.17.5-cert", playbook: "k8s-1.17.5-cluster/cert_1175.yml", hostGroup: "k8s_cert_controller",
		},
		{
			componentID: "component-k8s-1.17.5-etcd", slug: "k8s-1-17-5-etcd", name: "Kubernetes 1.17.5 Etcd",
			description: "Kubernetes 1.17.5 三节点 etcd 阶段安全适配快照。",
			releaseID:   "release-k8s-1.17.5-etcd", playbook: "k8s-1.17.5-cluster/etcd_serverless.yml", hostGroup: "k8setcd",
			upstreamComponentID: "component-k8s-1.17.5-cert", upstreamReleaseID: "release-k8s-1.17.5-cert", purpose: "cluster certificates",
		},
		{
			componentID: "component-k8s-1.17.5-master", slug: "k8s-1-17-5-master", name: "Kubernetes 1.17.5 Control Plane",
			description: "Kubernetes 1.17.5 三节点控制面阶段安全适配快照。",
			releaseID:   "release-k8s-1.17.5-master", playbook: "k8s-1.17.5-cluster/master_1175.yml", hostGroup: "k8smaster",
			upstreamComponentID: "component-k8s-1.17.5-etcd", upstreamReleaseID: "release-k8s-1.17.5-etcd", purpose: "etcd control-plane state",
		},
		{
			componentID: "component-k8s-1.17.5-node", slug: "k8s-1-17-5-node", name: "Kubernetes 1.17.5 Worker",
			description: "Kubernetes 1.17.5 工作节点阶段安全适配快照。",
			releaseID:   "release-k8s-1.17.5-node", playbook: "k8s-1.17.5-cluster/node_1175.yml", hostGroup: "k8snode",
			upstreamComponentID: "component-k8s-1.17.5-master", upstreamReleaseID: "release-k8s-1.17.5-master", purpose: "available control plane",
		},
	}
	for _, item := range stages {
		if err := s.createComponentIfMissing(ctx, component(item.componentID, item.slug, item.name, item.description, ComponentOwnerK8sID, now)); err != nil {
			return fmt.Errorf("seed Kubernetes 1.17.5 component %s: %w", item.componentID, err)
		}
		release := domain.ComponentRelease{
			ID: item.releaseID, ComponentID: item.componentID, Version: "v1.17.5", Type: domain.ReleaseAtomic,
			Status: domain.ReleaseReleased, ReleaseNotes: "源作业的 demo 安全适配快照；介质尚未验真，执行前必须审批。",
			Verified: false, RiskLevel: domain.RiskDestructive, EnvironmentConstraints: constraints,
			ParameterSchema: parameterSchema, CreatedAt: now, ReleasedAt: ptr(now),
			Actions: []domain.ActionDefinition{{
				ID: "action-" + item.slug + "-install", ReleaseID: item.releaseID, Name: "Install " + item.name,
				Kind: domain.ActionInstall, Playbook: item.playbook, Tags: []string{"install"}, Limit: item.hostGroup, HostGroup: item.hostGroup,
				TimeoutSeconds: 3600, RiskLevel: domain.RiskDestructive, Destructive: true,
			}},
		}
		if item.upstreamComponentID != "" {
			release.Dependencies = []domain.ComponentDependency{{
				ID: "dependency-" + item.slug, ReleaseID: item.releaseID,
				UpstreamComponentID: item.upstreamComponentID, UpstreamReleaseID: item.upstreamReleaseID, Purpose: item.purpose,
			}}
		}
		if err := s.createReleaseIfMissing(ctx, release); err != nil {
			return fmt.Errorf("seed Kubernetes 1.17.5 release %s: %w", item.releaseID, err)
		}
	}
	return nil
}

func (s Seeder) seedKubernetes1175Scenario(ctx context.Context, now time.Time) error {
	nodes := []domain.ScenarioNode{
		kubernetes1175ScenarioNode("k8s1175-cert", "Certificates", "release-k8s-1.17.5-cert", "k8s_cert_controller", 40, 80),
		kubernetes1175ScenarioNode("k8s1175-etcd", "Etcd", "release-k8s-1.17.5-etcd", "k8setcd", 300, 80),
		kubernetes1175ScenarioNode("k8s1175-master", "Control Plane", "release-k8s-1.17.5-master", "k8smaster", 560, 80),
		kubernetes1175ScenarioNode("k8s1175-node", "Worker", "release-k8s-1.17.5-node", "k8snode", 820, 80),
	}
	edges := []domain.ScenarioEdge{
		{ID: "k8s1175-edge-cert-etcd", Source: nodes[0].ID, Target: nodes[1].ID},
		{ID: "k8s1175-edge-etcd-master", Source: nodes[1].ID, Target: nodes[2].ID},
		{ID: "k8s1175-edge-master-node", Source: nodes[2].ID, Target: nodes[3].ID},
	}
	scenario := domain.Scenario{
		ID: "scenario-k8s-1.17.5", Slug: "kubernetes-1-17-5-cluster-build", Name: "Kubernetes 1.17.5 Cluster Build",
		Description: "证书、etcd、控制面、工作节点四步集群搭建；真实执行具有破坏性并且必须审批。",
		OwnerID:     ScenarioOwnerID, CreatedAt: now, UpdatedAt: now,
	}
	revision := domain.ScenarioRevision{
		ID: "scenario-k8s-1.17.5-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph:           domain.ScenarioGraph{Nodes: nodes, Edges: edges},
		ExecutionPolicy: map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention", "destructive": true},
		CreatedAt:       now,
	}
	if _, err := s.createScenarioIfMissing(ctx, scenario, revision); err != nil {
		return fmt.Errorf("seed Kubernetes 1.17.5 scenario: %w", err)
	}
	return nil
}

func kubernetes1175ScenarioNode(id, name, releaseID, group string, x, y float64) domain.ScenarioNode {
	return domain.ScenarioNode{
		ID: id, Name: name, ReleaseID: releaseID, Action: domain.ActionInstall, HostGroup: group,
		Values: map[string]any{}, Bindings: map[string]string{}, RunInputs: []string{},
		Position: domain.GraphPosition{X: x, Y: y}, Destructive: true,
	}
}

func (s Seeder) seedKubernetes1175Environment(ctx context.Context, now time.Time) error {
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{
		map[string]any{"name": "cert-controller", "address": "127.0.0.1", "groups": []any{"k8s_cert_controller"}},
		map[string]any{"name": "control-1", "address": "192.0.2.11", "groups": []any{"k8setcd", "k8smaster", "k8s_F5"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "control-2", "address": "192.0.2.12", "groups": []any{"k8setcd", "k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "control-3", "address": "192.0.2.13", "groups": []any{"k8setcd", "k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "worker-1", "address": "192.0.2.21", "groups": []any{"k8snode"}, "port": 22, "user": "sysop"},
	}})
	environment := domain.Environment{
		ID: "environment-k8s-1.17.5-template", Name: "Kubernetes 1.17.5 SUSE Template",
		Description: "仅使用 TEST-NET 和 localhost 的脱敏模板；介质验真标志默认为 false，不能直接用于生产。",
		OwnerID:     EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now,
	}
	revision := domain.EnvironmentRevision{
		ID: "environment-k8s-1.17.5-template-r1", EnvironmentID: environment.ID, Revision: 1,
		Facts:     map[string]any{"architecture": "amd64", "os": "SUSE", "network": "IPv4", "templateOnly": true},
		Inventory: inventory,
		Parameters: map[string]any{
			"K8S_VERSION":                     "v1.17.5",
			"K8S1175_ARTIFACTS_VERIFIED":      false,
			"K8S1175_DOCKER_RUNTIME_VERIFIED": false,
			"K8S1175_DOCKER_VERSION":          "18.09.7",
			"K8SMASTER_1175_CERT":             "file-station/kubernetes-1.17.5/kubernetes-server-linux-amd64.tar.gz",
			"K8SNODE_1175_CERT":               "file-station/kubernetes-1.17.5/kubernetes-node-linux-amd64.tar.gz",
			"IHS_IP":                          "192.0.2.80",
			"IHS_PORT":                        8080,
			"ENABLE_VM_CHECK":                 false,
		},
		CredentialRefs: []domain.CredentialRef{{
			Name: "K8S_ENCRYPTION_KEY", Kind: "envVarRef", Reference: "NEWPLATFORM_K8S1175_ENCRYPTION_KEY", Configured: true,
		}},
		MaxConcurrent: 1, CreatedAt: now,
	}
	if err := s.createEnvironmentIfMissing(ctx, environment, revision); err != nil {
		return fmt.Errorf("seed Kubernetes 1.17.5 environment: %w", err)
	}
	return nil
}

func (s Seeder) seedScenarios(ctx context.Context, now time.Time) error {
	openNodes := []domain.ScenarioNode{
		scenarioNode("open-cert", "Certificates", "release-bke-cert-25.12", "bootstrap_host", 40, 80),
		scenarioNode("open-bootstrap", "Bootstrap", "release-bke-bootstrap-25.12", "bootstrap_host", 300, 80),
		scenarioNode("open-common", "Common", "release-bke-common-25.12", "management_cluster_k8smaster", 560, 80),
		scenarioNode("open-addon", "Addons", "release-bke-addon-25.12", "management_cluster_k8smaster", 820, 80),
		scenarioNode("open-master", "Master", "release-bke-master-25.12", "management_cluster_k8smaster", 1080, 80),
	}
	openEdges := make([]domain.ScenarioEdge, 0, len(openNodes)-1)
	for i := 0; i < len(openNodes)-1; i++ {
		openEdges = append(openEdges, domain.ScenarioEdge{ID: fmt.Sprintf("open-edge-%d", i+1), Source: openNodes[i].ID, Target: openNodes[i+1].ID})
	}
	openScenario := domain.Scenario{ID: "scenario-openfuyao", Slug: "openfuyao-management-cluster", Name: "OpenFuyao Management Cluster Build", Description: "真实 OpenFuyao 两阶段管理集群搭建作业；包含 rcv，执行前必须审批。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	openRevision := domain.ScenarioRevision{ID: "scenario-openfuyao-r1", ScenarioID: openScenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: openNodes, Edges: openEdges}, ExecutionPolicy: map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention", "destructive": true}, CreatedAt: now}
	if _, err := s.createScenarioIfMissing(ctx, openScenario, openRevision); err != nil {
		return fmt.Errorf("seed OpenFuyao scenario: %w", err)
	}
	return nil
}

func scenarioNode(id, name, releaseID, group string, x, y float64) domain.ScenarioNode {
	return domain.ScenarioNode{ID: id, Name: name, ReleaseID: releaseID, Action: domain.ActionInstall, HostGroup: group, Values: map[string]any{}, Bindings: map[string]string{}, RunInputs: []string{}, Position: domain.GraphPosition{X: x, Y: y}}
}

func (s Seeder) seedEnvironments(ctx context.Context, now time.Time) error {
	openInventory, _ := json.Marshal(map[string]any{"hosts": []any{
		map[string]any{"name": "bootstrap-1", "address": "192.0.2.10", "groups": []any{"bootstrap_host"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "manager-1", "address": "192.0.2.20", "groups": []any{"management_cluster_k8smaster"}, "port": 22, "user": "sysop"},
	}})
	open := domain.Environment{ID: "environment-openfuyao-template", Name: "OpenFuyao Preflight Template", Description: "使用 TEST-NET 地址的脱敏模板；不会连接真实基础设施。", OwnerID: EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	openRevision := domain.EnvironmentRevision{ID: "environment-openfuyao-template-r1", EnvironmentID: open.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64", "os": "Kylin V10", "network": "IPv4", "templateOnly": true}, Inventory: openInventory, Parameters: map[string]any{"clusterVersion": "v25.12", "kubernetesVersion": "v1.34.3-of.1", "etcdVersion": "v3.6.7-of.1", "containerdVersion": "v2.1.1", "calicoVersion": "v3.27.3-icbc", "coreDNSVersion": "v1.12.2-of.1", "kubeProxyVersion": "v1.34.3-of.1.icbc.harbor"}, CredentialRefs: []domain.CredentialRef{}, MaxConcurrent: 1, CreatedAt: now}
	return s.createEnvironmentIfMissing(ctx, open, openRevision)
}

func (s Seeder) createComponentIfMissing(ctx context.Context, value domain.Component) error {
	if _, err := s.Store.GetComponent(ctx, value.ID, false); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.Store.CreateComponent(ctx, value)
}

func (s Seeder) createReleaseIfMissing(ctx context.Context, value domain.ComponentRelease) error {
	if _, err := s.Store.GetComponentRelease(ctx, value.ID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.Store.CreateComponentRelease(ctx, value)
}

func (s Seeder) createScenarioIfMissing(ctx context.Context, scenario domain.Scenario, revision domain.ScenarioRevision) (bool, error) {
	if existing, err := s.Store.GetScenario(ctx, scenario.ID, false); err == nil {
		return false, s.createScenarioRevisionIfMissing(ctx, revision, existing.CurrentRevisionID == "")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	return true, s.Store.CreateScenario(ctx, scenario, revision)
}

func (s Seeder) createScenarioRevisionIfMissing(ctx context.Context, value domain.ScenarioRevision, makeCurrent bool) error {
	if _, err := s.Store.GetScenarioRevision(ctx, value.ID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if makeCurrent {
		return s.Store.CreateScenarioRevision(ctx, value)
	}
	graph, err := json.Marshal(value.Graph)
	if err != nil {
		return err
	}
	policy, err := json.Marshal(value.ExecutionPolicy)
	if err != nil {
		return err
	}
	_, err = s.Store.DB().ExecContext(ctx, `INSERT INTO scenario_revisions(id,scenario_id,revision,status,graph_json,execution_policy_json,created_at,test_passed_at,released_at,deprecated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.ScenarioID, value.Revision, value.Status, string(graph), string(policy), seedTime(value.CreatedAt), seedPtrTime(value.TestPassedAt), seedPtrTime(value.ReleasedAt), seedPtrTime(value.DeprecatedAt))
	return err
}

func (s Seeder) createEnvironmentIfMissing(ctx context.Context, environment domain.Environment, revision domain.EnvironmentRevision) error {
	if existing, err := s.Store.GetEnvironment(ctx, environment.ID, false); err == nil {
		if _, err := s.Store.GetEnvironmentRevision(ctx, revision.ID); err == nil {
			return nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if existing.CurrentRevisionID == "" {
			return s.Store.CreateEnvironmentRevision(ctx, revision)
		}
		facts, err := json.Marshal(revision.Facts)
		if err != nil {
			return err
		}
		parameters, err := json.Marshal(revision.Parameters)
		if err != nil {
			return err
		}
		credentialRefs, err := json.Marshal(revision.CredentialRefs)
		if err != nil {
			return err
		}
		inventory := string(revision.Inventory)
		if inventory == "" {
			inventory = "{}"
		}
		maxConcurrent := revision.MaxConcurrent
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		_, err = s.Store.DB().ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,parameters_json,credential_refs_json,max_concurrent,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			revision.ID, revision.EnvironmentID, revision.Revision, string(facts), inventory, string(parameters), string(credentialRefs), maxConcurrent, seedTime(revision.CreatedAt))
		return err
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.Store.CreateEnvironment(ctx, environment, revision)
}

func (s Seeder) appendAuditIfMissing(ctx context.Context, value domain.AuditEvent) error {
	var found int
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE id=?`, value.ID).Scan(&found); err != nil {
		return fmt.Errorf("check seed audit %s: %w", value.ID, err)
	}
	if found > 0 {
		return nil
	}
	return s.Store.AppendAudit(ctx, value)
}

func seedTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func seedPtrTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return seedTime(*value)
}

func ptr(value time.Time) *time.Time { return &value }
