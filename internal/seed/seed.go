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
		Metadata: map[string]any{"snapshot": "examples/ansible/k8s-1.17.5-cluster", "model": "minimal-components", "verified": false}, CreatedAt: now,
	})
}

// SeedUsers creates only the fixed demo identities used by the role switcher.
// It intentionally leaves the component, scenario, and environment catalogs
// empty so an operator can exercise the complete frontend authoring flow.
func (s Seeder) SeedUsers(ctx context.Context) error {
	if s.Store == nil {
		return errors.New("seed store is required")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.seedUsers(ctx, now)
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
			Verified: true, RiskLevel: domain.RiskLow, EnvironmentConstraints: constraints, Parameters: []domain.ParameterDefinition{}, CreatedAt: now, ReleasedAt: ptr(now),
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
	layer, category, kind, requiredness := componentClassification(id)
	return domain.Component{
		ID: id, Slug: slug, Name: name, Description: description,
		Layer: layer, Category: category, Kind: kind, Requiredness: requiredness,
		OwnerID: owner, CreatedAt: now, UpdatedAt: now,
	}
}

func componentClassification(id string) (domain.ComponentLayer, domain.ComponentCategory, domain.ComponentKind, domain.ComponentRequiredness) {
	switch id {
	case "component-bke-cert":
		return domain.LayerHostFoundation, domain.CategorySecurity, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-bke-bootstrap":
		return domain.LayerHostFoundation, domain.CategoryBootstrap, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-host-preflight":
		return domain.LayerHostFoundation, domain.CategoryPreflight, domain.ComponentDeliveryStage, domain.RequiredCore
	case "component-host-bootstrap":
		return domain.LayerHostFoundation, domain.CategoryBootstrap, domain.ComponentConfiguration, domain.RequiredCore
	case "component-cluster-pki":
		return domain.LayerHostFoundation, domain.CategorySecurity, domain.ComponentArtifactSet, domain.RequiredCore
	case "component-kubernetes-encryption-config":
		return domain.LayerHostFoundation, domain.CategorySecurity, domain.ComponentConfiguration, domain.RequiredProfile
	case "component-containerd":
		return domain.LayerRuntimeState, domain.CategoryRuntime, domain.ComponentSoftware, domain.RequiredProfile
	case "component-docker":
		return domain.LayerRuntimeState, domain.CategoryRuntime, domain.ComponentSoftware, domain.RequiredProfile
	case "component-etcd":
		return domain.LayerRuntimeState, domain.CategoryStateStore, domain.ComponentSoftware, domain.RequiredCore
	case "component-kubernetes":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentSoftwareBundle, domain.RequiredCore
	case "component-kubernetes-distribution":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentSoftwareBundle, domain.RequiredCore
	case "component-kube-apiserver", "component-kube-controller-manager", "component-kube-scheduler":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentSoftware, domain.RequiredCore
	case "component-kubernetes-bootstrap-rbac":
		return domain.LayerOrchestrationCore, domain.CategoryControlPlane, domain.ComponentConfiguration, domain.RequiredCore
	case "component-kubelet":
		return domain.LayerOrchestrationCore, domain.CategoryWorker, domain.ComponentSoftware, domain.RequiredCore
	case "component-kube-proxy":
		return domain.LayerOrchestrationCore, domain.CategoryNetwork, domain.ComponentSoftware, domain.RequiredProfile
	case "component-calico", "component-flannel":
		return domain.LayerClusterService, domain.CategoryNetwork, domain.ComponentSoftware, domain.RequiredProfile
	case "component-coredns":
		return domain.LayerClusterService, domain.CategoryDNS, domain.ComponentSoftware, domain.RequiredCore
	case "component-haproxy":
		return domain.LayerClusterService, domain.CategoryIngress, domain.ComponentSoftware, domain.RequiredOptional
	case "component-glusterfs-client":
		return domain.LayerClusterService, domain.CategoryStorage, domain.ComponentSoftware, domain.RequiredOptional
	case "component-node-logging":
		return domain.LayerObservabilityManagement, domain.CategoryNodeManagement, domain.ComponentConfiguration, domain.RequiredOptional
	case "component-blackbox-exporter", "component-node-exporter", "component-metrics-server":
		return domain.LayerObservabilityManagement, domain.CategoryObservability, domain.ComponentSoftware, domain.RequiredOptional
	case "component-amc", "component-go-pprof-toolkit":
		return domain.LayerObservabilityManagement, domain.CategoryNodeManagement, domain.ComponentSoftware, domain.RequiredOptional
	case "component-prometheus-access-bootstrap":
		return domain.LayerObservabilityManagement, domain.CategoryObservability, domain.ComponentConfiguration, domain.RequiredOptional
	case "component-bke-common", "component-bke-master", "component-bke-nodes":
		return domain.LayerPlatformExtension, domain.CategoryPlatform, domain.ComponentDeliveryStage, domain.RequiredProfile
	case "component-bke-addon":
		return domain.LayerPlatformExtension, domain.CategoryPlatform, domain.ComponentSoftwareBundle, domain.RequiredProfile
	case "component-autoscaling-rbac":
		return domain.LayerPlatformExtension, domain.CategoryAutoscaling, domain.ComponentConfiguration, domain.RequiredOptional
	default:
		// Unknown component ID — assign a safe default classification
		// rather than crashing the process.
		return domain.LayerPlatformExtension, domain.CategoryPlatform, domain.ComponentSoftware, domain.RequiredOptional
	}
}

func (s Seeder) seedScenarios(ctx context.Context, now time.Time) error {
	return s.seedOpenFuyaoScenarios(ctx, now)
}

func scenarioNode(id, name, releaseID, group string, x, y float64) domain.ScenarioNode {
	return domain.ScenarioNode{ID: id, Name: name, ReleaseID: releaseID, Action: domain.ActionInstall, HostGroup: group, Values: map[string]any{}, RunInputs: []string{}, Position: domain.GraphPosition{X: x, Y: y}}
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
		maxConcurrent := revision.MaxConcurrent
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
		_, err = s.Store.DB().ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,max_concurrent,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			revision.ID, revision.EnvironmentID, revision.Revision, string(facts), inventory, string(variables), string(credentialRefs), maxConcurrent, seedTime(revision.CreatedAt))
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
