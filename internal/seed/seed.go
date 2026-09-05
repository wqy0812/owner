package seed

import (
	"context"
	"crypto/sha256"
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
	PlatformAdminID         = "platform-admin"
	seedAuditID             = "audit-demo-seeded"
	platformCatalogAuditID  = "audit-platform-catalog-bootstrapped"
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
	if err := s.seedPlatformCatalog(ctx, now); err != nil {
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
		ID: seedAuditID, ActorID: "system", Action: "demo.seeded",
		ResourceType: "platform", ResourceID: "newplatform-demo",
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
		ID: "audit-k8s-1.17.5-demo-seeded", ActorID: "system", Action: "demo.kubernetes_1_17_5.seeded",
		ResourceType: "scenario", ResourceID: "scenario-k8s-1.17.5",
		Metadata: map[string]any{"snapshot": "examples/ansible/k8s-1.17.5-cluster", "model": "minimal-components", "evidence": "not_seeded"}, CreatedAt: now,
	})
}

// SeedUsers creates the fixed identities used by the role switcher and installs
// the platform-owned directories once on a new database. It intentionally
// leaves component, scenario, and environment catalogs empty so an operator can
// exercise the complete frontend authoring flow.
func (s Seeder) SeedUsers(ctx context.Context) error {
	if s.Store == nil {
		return errors.New("seed store is required")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	for _, user := range identityUsers(now) {
		if err := s.ensureDemoUser(ctx, user); err != nil {
			return err
		}
	}
	return s.seedPlatformCatalog(ctx, now)
}

func (s Seeder) seedUsers(ctx context.Context, now time.Time) error {
	for _, user := range demoUsers(now) {
		if err := s.ensureDemoUser(ctx, user); err != nil {
			return err
		}
	}
	return nil
}

func (s Seeder) ensureDemoUser(ctx context.Context, user domain.User) error {
	_, err := s.Store.GetUser(ctx, user.ID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("check seed user %s: %w", user.ID, err)
	}
	if err := s.Store.UpsertUser(ctx, user); err != nil {
		return fmt.Errorf("seed user %s: %w", user.ID, err)
	}
	return nil
}

func demoUsers(now time.Time) []domain.User {
	return []domain.User{
		{ID: ComponentOwnerRuntimeID, Name: "林晓", Role: domain.RoleComponentOwner, CreatedAt: now},
		{ID: ComponentOwnerK8sID, Name: "周工", Role: domain.RoleComponentOwner, CreatedAt: now},
		{ID: ScenarioOwnerID, Name: "陈晨", Role: domain.RoleScenarioOwner, CreatedAt: now},
		{ID: EnvironmentOwnerID, Name: "王维", Role: domain.RoleEnvironmentOwner, CreatedAt: now},
		{ID: PlatformAdminID, Name: "赵宁", Role: domain.RolePlatformAdmin, CreatedAt: now},
	}
}

func identityUsers(now time.Time) []domain.User {
	return []domain.User{
		{ID: ComponentOwnerRuntimeID, Name: "林晓", Role: domain.RoleComponentOwner, CreatedAt: now},
		{ID: ScenarioOwnerID, Name: "陈晨", Role: domain.RoleScenarioOwner, CreatedAt: now},
		{ID: EnvironmentOwnerID, Name: "王维", Role: domain.RoleEnvironmentOwner, CreatedAt: now},
		{ID: PlatformAdminID, Name: "赵宁", Role: domain.RolePlatformAdmin, CreatedAt: now},
	}
}

type seededPlatformCategory struct {
	id            string
	key           string
	label         string
	kind          domain.PlatformOptionCategoryKind
	required      bool
	parentKey     string
	options       []seededPlatformOption
	optionParents map[string]string
}

type seededPlatformOption struct {
	value string
	label string
}

func initialPlatformCategories() []seededPlatformCategory {
	return []seededPlatformCategory{
		{id: "platform-category-architecture", key: "architecture", label: "架构", kind: domain.PlatformOptionEnvironmentDimension, required: true, options: []seededPlatformOption{{"amd64", "x86/amd64"}, {"arm64", "ARM/arm64"}}},
		{id: "platform-category-operating-system", key: "operatingSystem", label: "操作系统", kind: domain.PlatformOptionEnvironmentDimension, required: true, options: []seededPlatformOption{{"Ubuntu", "Ubuntu"}, {"SUSE", "SUSE"}, {"Kylin", "Kylin"}}},
		{id: "platform-category-operating-system-version", key: "operatingSystemVersion", label: "操作系统版本", kind: domain.PlatformOptionEnvironmentDimension, required: true, options: []seededPlatformOption{{"18.04", "18.04"}, {"20.04", "20.04"}, {"22.04", "22.04"}, {"24.04", "24.04"}, {"18.04 / 24.04", "18.04 / 24.04（混合）"}}},
		{id: "platform-category-ip-family", key: "ipFamily", label: "IP 协议族", kind: domain.PlatformOptionEnvironmentDimension, required: true, options: []seededPlatformOption{{"IPv4", "IPv4"}, {"IPv6", "IPv6"}}},
		{id: "platform-category-hardware-profile", key: "hardwareProfile", label: "硬件类型", kind: domain.PlatformOptionEnvironmentDimension, options: []seededPlatformOption{{"general", "通用主机"}, {"gpu", "GPU"}, {"dpu", "DPU"}, {"bms", "BMS"}}},
		{id: "platform-category-deployment-mode", key: "deploymentMode", label: "部署形态", kind: domain.PlatformOptionEnvironmentDimension, options: []seededPlatformOption{{"standard", "standard"}, {"serverless", "serverless"}, {"ingress", "ingress"}}},
		{id: "platform-category-host-group", key: "hostGroup", label: "主机组", kind: domain.PlatformOptionHostGroup, options: []seededPlatformOption{{"all", "all"}}},
	}
}

func (s Seeder) seedPlatformCatalog(ctx context.Context, now time.Time) error {
	categoryIDs := map[string]string{}
	optionIDs := map[string]string{}
	categories := make([]domain.PlatformOptionCategory, 0, len(initialPlatformCategories()))
	options := []domain.PlatformOption{}
	for categoryIndex, spec := range initialPlatformCategories() {
		category := domain.PlatformOptionCategory{
			ID: spec.id, Key: spec.key, Label: spec.label, Kind: spec.kind,
			EnvironmentRequired: spec.required, SortOrder: categoryIndex,
			CreatedBy: PlatformAdminID, CreatedAt: now,
		}
		category.ParentCategoryID = categoryIDs[spec.parentKey]
		categories = append(categories, category)
		for optionIndex, optionSpec := range spec.options {
			optionID := "platform-option-" + stableSeedSuffix(spec.key+"\x00"+optionSpec.value)
			option := domain.PlatformOption{
				ID: optionID, CategoryID: spec.id,
				Value: optionSpec.value, Label: optionSpec.label, SortOrder: optionIndex,
				CreatedBy: PlatformAdminID, CreatedAt: now,
			}
			if parentValue := spec.optionParents[optionSpec.value]; parentValue != "" {
				option.ParentOptionID = optionIDs[spec.parentKey+"\x00"+parentValue]
			}
			options = append(options, option)
			optionIDs[spec.key+"\x00"+optionSpec.value] = optionID
		}
		categoryIDs[spec.key] = spec.id
	}
	variableDefinitions := []domain.EnvironmentVariableDefinition{
		{ID: "environment-variable-image-registry", Name: "IMAGE_REGISTRY", Label: "镜像仓库", Description: "镜像拉取和推送使用的 host:port", CreatedBy: PlatformAdminID, CreatedAt: now},
		{ID: "environment-variable-file-station", Name: "FILE_STATION", Label: "组件介质站", Description: "组件介质下载使用的 host:port", CreatedBy: PlatformAdminID, CreatedAt: now},
	}
	return s.Store.BootstrapPlatformCatalog(ctx, categories, options, variableDefinitions, domain.AuditEvent{
		ID: platformCatalogAuditID, ActorID: "system", Action: "platform_option_catalog.bootstrapped",
		ResourceType: "platform", ResourceID: "platform-option-catalog",
		Metadata: map[string]any{"mode": "initial-only"}, CreatedAt: now,
	})
}

func stableSeedSuffix(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:12])
}

type seededComponent struct {
	component domain.Component
	releases  []domain.ComponentRelease
}

func (s Seeder) seedComponents(ctx context.Context, now time.Time) error {
	constraints := map[string]any{"architecture": []any{"amd64"}, "ipFamily": []any{"IPv4"}, "operatingSystem": []any{"Kylin"}}
	plainRelease := func(id, componentID, version string) domain.ComponentRelease {
		return domain.ComponentRelease{
			ID: id, ComponentID: componentID, Version: version, Status: domain.ReleaseReleased,
			RiskLevel: domain.RiskLow, EnvironmentConstraints: constraints, Parameters: []domain.ParameterDefinition{}, CreatedAt: now, ReleasedAt: ptr(now),
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

	components = append(components, openFuyaoComponents(now, constraints)...)

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
	layer, tags := componentMetadata(id)
	return domain.Component{
		ID: id, Slug: slug, Name: name, Description: description,
		Layer: layer, Tags: tags,
		OwnerID: owner, CreatedAt: now, UpdatedAt: now,
	}
}

func componentMetadata(id string) (domain.ComponentLayer, []string) {
	switch id {
	case "component-bke-cert":
		return domain.LayerHostFoundation, []string{"security", "delivery"}
	case "component-bke-bootstrap":
		return domain.LayerHostFoundation, []string{"bootstrap", "delivery"}
	case "component-host-preflight":
		return domain.LayerHostFoundation, []string{"preflight", "core"}
	case "component-host-bootstrap":
		return domain.LayerHostFoundation, []string{"bootstrap", "configuration", "core"}
	case "component-cluster-pki":
		return domain.LayerHostFoundation, []string{"security", "artifacts", "core"}
	case "component-kubernetes-encryption-config":
		return domain.LayerHostFoundation, []string{"security", "configuration"}
	case "component-containerd":
		return domain.LayerRuntimeState, []string{"runtime"}
	case "component-docker":
		return domain.LayerRuntimeState, []string{"runtime"}
	case "component-etcd":
		return domain.LayerRuntimeState, []string{"state_store", "core"}
	case "component-kubernetes":
		return domain.LayerOrchestrationCore, []string{"control_plane", "core"}
	case "component-kubernetes-distribution":
		return domain.LayerOrchestrationCore, []string{"control_plane", "distribution", "core"}
	case "component-kube-apiserver", "component-kube-controller-manager", "component-kube-scheduler":
		return domain.LayerOrchestrationCore, []string{"control_plane", "core"}
	case "component-kubernetes-bootstrap-rbac":
		return domain.LayerOrchestrationCore, []string{"control_plane", "configuration", "core"}
	case "component-kubelet":
		return domain.LayerOrchestrationCore, []string{"worker", "core"}
	case "component-kube-proxy":
		return domain.LayerOrchestrationCore, []string{"network"}
	case "component-calico", "component-flannel":
		return domain.LayerClusterService, []string{"network"}
	case "component-coredns":
		return domain.LayerClusterService, []string{"dns", "core"}
	case "component-haproxy":
		return domain.LayerClusterService, []string{"ingress", "optional"}
	case "component-glusterfs-client":
		return domain.LayerClusterService, []string{"storage", "optional"}
	case "component-node-logging":
		return domain.LayerObservabilityManagement, []string{"node_management", "configuration", "optional"}
	case "component-blackbox-exporter", "component-node-exporter", "component-metrics-server":
		return domain.LayerObservabilityManagement, []string{"observability", "optional"}
	case "component-amc", "component-go-pprof-toolkit":
		return domain.LayerObservabilityManagement, []string{"node_management", "optional"}
	case "component-prometheus-access-bootstrap":
		return domain.LayerObservabilityManagement, []string{"observability", "configuration", "optional"}
	case "component-bke-common", "component-bke-master", "component-bke-nodes":
		return domain.LayerPlatformExtension, []string{"platform", "delivery"}
	case "component-bke-addon":
		return domain.LayerPlatformExtension, []string{"platform", "addon"}
	case "component-autoscaling-rbac":
		return domain.LayerPlatformExtension, []string{"autoscaling", "configuration", "optional"}
	default:
		return domain.LayerPlatformExtension, []string{"platform", "optional"}
	}
}

func (s Seeder) seedScenarios(ctx context.Context, now time.Time) error {
	return s.seedOpenFuyaoScenarios(ctx, now)
}

func scenarioNode(id, name, releaseID, group string, x, y float64) domain.ScenarioNode {
	return domain.ScenarioNode{ID: id, Name: name, ReleaseID: releaseID, Action: domain.ActionInstall, HostGroup: group, ParameterValues: map[string]any{}, Position: domain.GraphPosition{X: x, Y: y}}
}

func (s Seeder) seedEnvironments(ctx context.Context, now time.Time) error {
	return s.seedOpenFuyaoEnvironment(ctx, now)
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
	// Register fixture-owned directory entries before their first reference.
	// Identity-only startup never calls this path.
	for _, action := range value.Actions {
		if err := s.ensureSeedHostGroup(ctx, action.HostGroup, value.CreatedAt); err != nil {
			return err
		}
	}
	return s.Store.CreateComponentRelease(ctx, value)
}

func (s Seeder) ensureSeedHostGroup(ctx context.Context, group string, now time.Time) error {
	if group == "" {
		return nil
	}
	return s.Store.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{
		ID: "platform-option-" + stableSeedSuffix("hostGroup\x00"+group), Value: group, Label: group, SortOrder: 1000, CreatedBy: PlatformAdminID, CreatedAt: now,
	})
}

func (s Seeder) createScenarioIfMissing(ctx context.Context, scenario domain.Scenario, revision domain.ScenarioRevision) (bool, error) {
	graph, err := s.scenarioGraph(ctx, revision.Graph.Nodes, revision.Graph.Edges)
	if err != nil {
		return false, err
	}
	revision.Graph = graph
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
	_, err = s.Store.DB().ExecContext(ctx, `INSERT INTO scenario_revisions(id,scenario_id,revision,status,graph_json,created_at,test_passed_at,released_at,deprecated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		value.ID, value.ScenarioID, value.Revision, value.Status, string(graph), seedTime(value.CreatedAt), seedPtrTime(value.TestPassedAt), seedPtrTime(value.ReleasedAt), seedPtrTime(value.DeprecatedAt))
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
		variables, err := json.Marshal(revision.Variables)
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
		_, err = s.Store.DB().ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,created_by,change_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			revision.ID, revision.EnvironmentID, revision.Revision, string(facts), inventory, string(variables), string(credentialRefs), revision.CreatedBy, revision.ChangeReason, seedTime(revision.CreatedAt))
		return err
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	var inventory struct{ Hosts []struct{ Groups []string } }
	if err := json.Unmarshal(revision.Inventory, &inventory); err != nil {
		return err
	}
	for _, host := range inventory.Hosts {
		for _, group := range host.Groups {
			if err := s.ensureSeedHostGroup(ctx, group, revision.CreatedAt); err != nil {
				return err
			}
		}
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

// scenarioGraph builds new demo graphs from exact Release contracts and the
// additional ordering constraints explicitly declared by the demo scenario.
func (s Seeder) scenarioGraph(ctx context.Context, nodes []domain.ScenarioNode, ordering []domain.ScenarioEdge) (domain.ScenarioGraph, error) {
	graph := domain.ScenarioGraph{Nodes: nodes, Edges: []domain.ScenarioEdge{}}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		release, err := s.Store.GetComponentRelease(ctx, node.ReleaseID)
		if err != nil {
			return graph, err
		}
		if node.DependencySources == nil {
			node.DependencySources = map[string]string{}
		}
		for _, dep := range release.Dependencies {
			if node.Action == domain.ActionVerify && len(dep.ParameterMappings) == 0 {
				continue
			}
			selected := node.DependencySources[dep.ID]
			candidates := []string{}
			for _, other := range nodes {
				if other.ID != node.ID && other.ReleaseID == dep.UpstreamReleaseID {
					candidates = append(candidates, other.ID)
				}
			}
			if selected == "" && len(candidates) == 1 {
				selected = candidates[0]
			}
			if selected == "" {
				return graph, fmt.Errorf("seed node %s requires an explicit source for %s", node.ID, dep.ID)
			}
			node.DependencySources[dep.ID] = selected
			if dep.Kind == domain.DependencyConfiguration {
				continue
			}
			graph.Edges = append(graph.Edges, domain.ScenarioEdge{ID: fmt.Sprintf("dependency:%s:%s:%s", dep.ID, selected, node.ID), Source: selected, Target: node.ID, Kind: domain.ScenarioEdgeDependency, DependencyID: dep.ID})
		}
	}
	for _, edge := range ordering {
		if !seedReachable(graph.Edges, edge.Source, edge.Target) {
			graph.Edges = append(graph.Edges, edge)
		}
	}
	// Keep only additional sequence constraints not already implied by the final DAG.
	for i := 0; i < len(graph.Edges); {
		e := graph.Edges[i]
		rest := append(append([]domain.ScenarioEdge{}, graph.Edges[:i]...), graph.Edges[i+1:]...)
		if e.Kind == domain.ScenarioEdgeSequence && seedReachable(rest, e.Source, e.Target) {
			graph.Edges = rest
		} else {
			i++
		}
	}
	return graph, nil
}
func seedReachable(edges []domain.ScenarioEdge, source, target string) bool {
	queue := []string{source}
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, e := range edges {
			if e.Source == current {
				queue = append(queue, e.Target)
			}
		}
	}
	return false
}
