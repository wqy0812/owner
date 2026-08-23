package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type kubernetes1175ReleaseSpec struct {
	componentID  string
	slug         string
	name         string
	description  string
	releaseID    string
	version      string
	action       domain.ActionKind
	hostGroup    string
	dependencies []kubernetes1175DependencySpec
}

type kubernetes1175DependencySpec struct {
	componentID string
	releaseID   string
	purpose     string
}

var kubernetes1175ReleaseSpecs = []kubernetes1175ReleaseSpec{
	{componentID: "component-host-preflight", slug: "host-preflight", name: "Host Preflight", description: "验证 SUSE 主机、网络和集群拓扑，不写入远端主机。", releaseID: "release-host-preflight-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionPreflight, hostGroup: "all"},
	{componentID: "component-host-bootstrap", slug: "host-bootstrap", name: "Host Bootstrap", description: "配置 Kubernetes 主机所需用户、目录和基础系统参数。", releaseID: "release-host-bootstrap-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-preflight", "release-host-preflight-k8s-1.17.5", "host and topology preflight"}}},
	{componentID: "component-cluster-pki", slug: "cluster-pki", name: "Cluster PKI", description: "生成并分发 Kubernetes 1.17.5 集群证书制品。", releaseID: "release-cluster-pki-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionInstall, hostGroup: "k8s_cert_controller", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared control-plane hosts"}}},
	{componentID: "component-docker", slug: "docker", name: "Docker", description: "Kubernetes 1.17.5 使用的 Docker CRI 运行时。", releaseID: "release-docker-18.09.7", version: "18.09.7", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-etcd", slug: "etcd", name: "etcd", description: "Kubernetes 1.17.5 控制面状态存储 Release。", releaseID: "release-etcd-3.3.10", version: "3.3.10", action: domain.ActionInstall, hostGroup: "k8setcd", dependencies: []kubernetes1175DependencySpec{{"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "etcd certificates"}}},
	{componentID: "component-kubernetes-distribution", slug: "kubernetes-distribution", name: "Kubernetes Distribution", description: "只安装 Kubernetes 1.17.5 公共二进制和静态文件。", releaseID: "release-kubernetes-distribution-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "cluster trust material"}}},
	{componentID: "component-kubernetes-encryption-config", slug: "kubernetes-encryption-config", name: "Kubernetes Encryption Configuration", description: "配置 kube-apiserver 静态数据加密。", releaseID: "release-kubernetes-encryption-config-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "installed Kubernetes files"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "cluster trust material"}}},
	{componentID: "component-kube-apiserver", slug: "kube-apiserver", name: "kube-apiserver", description: "独立安装并启动 Kubernetes API Server。", releaseID: "release-kube-apiserver-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "control-plane binaries"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "API certificates"}, {"component-kubernetes-encryption-config", "release-kubernetes-encryption-config-1.17.5", "encryption configuration"}, {"component-etcd", "release-etcd-3.3.10", "control-plane state"}}},
	{componentID: "component-kube-controller-manager", slug: "kube-controller-manager", name: "kube-controller-manager", description: "独立安装并启动 Kubernetes Controller Manager。", releaseID: "release-kube-controller-manager-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}, {"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "control-plane binaries"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "controller credentials"}}},
	{componentID: "component-kube-scheduler", slug: "kube-scheduler", name: "kube-scheduler", description: "独立安装并启动 Kubernetes Scheduler。", releaseID: "release-kube-scheduler-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}, {"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "control-plane binaries"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "scheduler credentials"}}},
	{componentID: "component-kubernetes-bootstrap-rbac", slug: "kubernetes-bootstrap-rbac", name: "Kubernetes Bootstrap RBAC", description: "配置 kubelet TLS bootstrap 所需 RBAC。", releaseID: "release-kubernetes-bootstrap-rbac-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}}},
	{componentID: "component-flannel", slug: "flannel", name: "Flannel", description: "Kubernetes 1.17.5 集群 Pod 网络。", releaseID: "release-flannel-0.11.0", version: "0.11.0", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-docker", "release-docker-18.09.7", "container runtime"}, {"component-etcd", "release-etcd-3.3.10", "network state"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "etcd client certificate"}}},
	{componentID: "component-kubelet", slug: "kubelet", name: "kubelet", description: "独立安装并启动 Kubernetes 节点代理。", releaseID: "release-kubelet-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-docker", "release-docker-18.09.7", "container runtime"}, {"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "node binaries"}, {"component-flannel", "release-flannel-0.11.0", "pod network"}, {"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}, {"component-kubernetes-bootstrap-rbac", "release-kubernetes-bootstrap-rbac-1.17.5", "TLS bootstrap authorization"}}},
	{componentID: "component-kube-proxy", slug: "kube-proxy", name: "kube-proxy", description: "Kubernetes 1.17.5 节点 Service 网络代理 Release。", releaseID: "release-kube-proxy-1.17.5", version: "1.17.5", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kubelet", "release-kubelet-1.17.5", "running node service"}, {"component-kubernetes-distribution", "release-kubernetes-distribution-1.17.5", "node binaries"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "proxy credentials"}}},
	{componentID: "component-coredns", slug: "coredns", name: "CoreDNS", description: "Kubernetes 1.17.5 集群 DNS Release。", releaseID: "release-coredns-1.3.1", version: "1.3.1", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}, {"component-flannel", "release-flannel-0.11.0", "pod network"}, {"component-kubelet", "release-kubelet-1.17.5", "schedulable nodes"}}},

	{componentID: "component-node-logging", slug: "node-logging", name: "Node Logging", description: "配置节点日志目录和轮转策略。", releaseID: "release-node-logging-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-haproxy", slug: "haproxy", name: "HAProxy", description: "源快照中的控制面入口代理，未确认上游软件版本。", releaseID: "release-haproxy-source-6909da3", version: "source-6909da3", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "API endpoints"}, {"component-cluster-pki", "release-cluster-pki-k8s-1.17.5", "TLS material"}}},
	{componentID: "component-blackbox-exporter", slug: "blackbox-exporter", name: "Blackbox Exporter", description: "源快照中的探测 Exporter，未确认上游软件版本。", releaseID: "release-blackbox-exporter-source-6909da3", version: "source-6909da3", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-node-exporter", slug: "node-exporter", name: "Node Exporter", description: "节点操作系统指标 Exporter。", releaseID: "release-node-exporter-0.18.0", version: "0.18.0", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-metrics-server", slug: "metrics-server", name: "Metrics Server", description: "Kubernetes 资源指标 API。", releaseID: "release-metrics-server-0.3.1", version: "0.3.1", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}}},
	{componentID: "component-amc", slug: "amc", name: "AMC", description: "源快照中的 AMC 节点能力，未确认软件版本。", releaseID: "release-amc-source-6909da3", version: "source-6909da3", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kubelet", "release-kubelet-1.17.5", "running nodes"}}},
	{componentID: "component-glusterfs-client", slug: "glusterfs-client", name: "GlusterFS Client", description: "节点 GlusterFS 客户端。", releaseID: "release-glusterfs-client-3.12.6", version: "3.12.6", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-go-pprof-toolkit", slug: "go-pprof-toolkit", name: "Go/pprof Toolkit", description: "源快照中的 Go 与 pprof 运维工具，未确认软件版本。", releaseID: "release-go-pprof-toolkit-source-6909da3", version: "source-6909da3", action: domain.ActionInstall, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-host-bootstrap", "release-host-bootstrap-k8s-1.17.5", "prepared hosts"}}},
	{componentID: "component-prometheus-access-bootstrap", slug: "prometheus-access-bootstrap", name: "Prometheus Access Bootstrap", description: "配置 Prometheus 访问 Kubernetes API 的凭据和授权。", releaseID: "release-prometheus-access-bootstrap-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}}},
	{componentID: "component-autoscaling-rbac", slug: "autoscaling-rbac", name: "Autoscaling RBAC", description: "配置集群自动伸缩所需 RBAC。", releaseID: "release-autoscaling-rbac-k8s-1.17.5", version: "v1.17.5-r1", action: domain.ActionConfigure, hostGroup: "k8smaster", dependencies: []kubernetes1175DependencySpec{{"component-kube-apiserver", "release-kube-apiserver-1.17.5", "available API server"}}},
}

func kubernetes1175Parameters() []domain.ParameterDefinition {
	return []domain.ParameterDefinition{
		{Name: "K8S_VERSION", Description: "Kubernetes 发行版本", Type: domain.ParameterTypeString, Required: true, DefaultValue: "v1.17.5", Visibility: domain.ParameterInternal, Enum: []any{"v1.17.5"}},
		{Name: "K8S1175_ARTIFACTS_VERIFIED", Description: "核心制品是否已验真", Type: domain.ParameterTypeBoolean, Required: true, DefaultValue: false, Visibility: domain.ParameterInternal},
		{Name: "K8S1175_OPTIONAL_ARTIFACTS_VERIFIED", Description: "可选制品是否已验真", Type: domain.ParameterTypeBoolean, Required: true, DefaultValue: false, Visibility: domain.ParameterInternal},
		{Name: "K8S1175_DOCKER_RUNTIME_VERIFIED", Description: "Docker 运行时是否已验真", Type: domain.ParameterTypeBoolean, Required: true, DefaultValue: false, Visibility: domain.ParameterInternal},
		{Name: "K8S1175_DOCKER_VERSION", Description: "Docker 版本", Type: domain.ParameterTypeString, Required: true, DefaultValue: "18.09.7", Visibility: domain.ParameterInternal, Enum: []any{"18.09.7"}},
		{Name: "IHS_IP", Description: "制品仓库地址", Type: domain.ParameterTypeString, Required: true, DefaultValue: "192.0.2.80", Visibility: domain.ParameterInternal},
		{Name: "IHS_PORT", Description: "制品仓库端口", Type: domain.ParameterTypeInteger, Required: true, DefaultValue: 8080, Visibility: domain.ParameterInternal},
		{Name: "ENABLE_VM_CHECK", Description: "是否启用虚机检查", Type: domain.ParameterTypeBoolean, Required: true, DefaultValue: false, Visibility: domain.ParameterInternal},
	}
}

func kubernetes1175ReleaseParameters(componentID string) []domain.ParameterDefinition {
	parameters := kubernetes1175Parameters()
	switch componentID {
	case "component-kubelet":
		parameters = append(parameters, domain.ParameterDefinition{
			Name: "kubeInstallRoot", Description: "kubelet 安装根目录", Type: domain.ParameterTypeString,
			Required: true, DefaultValue: "/approot1/paas/kube", Visibility: domain.ParameterPublic,
			MinLength: 1,
		})
	case "component-kube-proxy":
		parameters = append(parameters, domain.ParameterDefinition{
			Name: "kubeRoot", Description: "复用 kubelet 安装目录", Type: domain.ParameterTypeString,
			Required: true, Visibility: domain.ParameterInternal, MinLength: 1,
		})
	}
	return parameters
}

func (s Seeder) seedKubernetes1175Catalog(ctx context.Context, now time.Time) error {
	constraints := map[string]any{"architecture": []any{"amd64"}, "operatingSystem": []any{"SUSE"}, "ipFamily": []any{"IPv4"}}
	for _, spec := range kubernetes1175ReleaseSpecs {
		if spec.componentID != "component-etcd" && spec.componentID != "component-kube-proxy" && spec.componentID != "component-coredns" {
			if err := s.createComponentIfMissing(ctx, component(spec.componentID, spec.slug, spec.name, spec.description, ComponentOwnerK8sID, now)); err != nil {
				return fmt.Errorf("seed Kubernetes 1.17.5 component %s: %w", spec.componentID, err)
			}
		}
		destructive := spec.action != domain.ActionPreflight
		actions := []domain.ActionDefinition{{
			ID: "action-" + spec.slug + "-" + string(spec.action), ReleaseID: spec.releaseID,
			Name: string(spec.action) + " " + spec.name, Kind: spec.action,
			Playbook: "k8s-1.17.5-cluster/components/" + spec.slug + ".yml",
			Limit:    spec.hostGroup, HostGroup: spec.hostGroup, TimeoutSeconds: 3600,
			RiskLevel: func() domain.RiskLevel {
				if destructive {
					return domain.RiskDestructive
				}
				return domain.RiskLow
			}(), Destructive: destructive,
		}, {
			ID: "action-" + spec.slug + "-verify", ReleaseID: spec.releaseID,
			Name: "verify " + spec.name, Kind: domain.ActionVerify,
			Playbook: "k8s-1.17.5-cluster/components/" + spec.slug + "-verify.yml",
			Limit:    spec.hostGroup, HostGroup: spec.hostGroup, TimeoutSeconds: 900,
			RiskLevel: domain.RiskLow, Destructive: false,
		}}
		dependencies := make([]domain.ComponentDependency, 0, len(spec.dependencies))
		for i, dependency := range spec.dependencies {
			item := domain.ComponentDependency{
				ID: fmt.Sprintf("dependency-%s-%d", spec.slug, i+1), ReleaseID: spec.releaseID,
				UpstreamComponentID: dependency.componentID, UpstreamReleaseID: dependency.releaseID, Purpose: dependency.purpose,
			}
			if spec.componentID == "component-kube-proxy" && dependency.componentID == "component-kubelet" {
				item.ParameterMappings = []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}
			}
			dependencies = append(dependencies, item)
		}
		releaseType := domain.ReleaseAtomic
		if spec.componentID == "component-kubernetes-distribution" {
			releaseType = domain.ReleaseBundle
		}
		release := domain.ComponentRelease{
			ID: spec.releaseID, ComponentID: spec.componentID, Version: spec.version, Type: releaseType,
			Status: domain.ReleaseReleased, ReleaseNotes: "来自 6909da3 作业快照的最小逻辑组件；介质未完成部署验真。",
			Verified: false, RiskLevel: domain.RiskDestructive, EnvironmentConstraints: constraints,
			Parameters: kubernetes1175ReleaseParameters(spec.componentID), Dependencies: dependencies, Actions: actions,
			CreatedAt: now, ReleasedAt: ptr(now),
		}
		if err := s.createReleaseIfMissing(ctx, release); err != nil {
			return fmt.Errorf("seed Kubernetes 1.17.5 release %s: %w", spec.releaseID, err)
		}
	}
	return nil
}

func kubernetes1175Node(id, name, releaseID, group string, x, y float64) domain.ScenarioNode {
	action := domain.ActionInstall
	for _, spec := range kubernetes1175ReleaseSpecs {
		if spec.releaseID == releaseID {
			action = spec.action
			break
		}
	}
	node := domain.ScenarioNode{ID: id, Name: name, ReleaseID: releaseID, Action: action, HostGroup: group, Values: map[string]any{}, RunInputs: []string{}, Position: domain.GraphPosition{X: x, Y: y}, Destructive: action != domain.ActionPreflight}
	switch id {
	case "k8s1175-proxy-master":
		node.DependencySources = map[string]string{"dependency-kube-proxy-1": "k8s1175-kubelet-master"}
	case "k8s1175-proxy-worker":
		node.DependencySources = map[string]string{"dependency-kube-proxy-1": "k8s1175-kubelet-worker"}
	case "k8s1175-ext-proxy-master":
		node.DependencySources = map[string]string{"dependency-kube-proxy-1": "k8s1175-ext-kubelet-master"}
	case "k8s1175-ext-proxy-worker":
		node.DependencySources = map[string]string{"dependency-kube-proxy-1": "k8s1175-ext-kubelet-worker"}
	}
	return node
}

func kubernetes1175CoreGraph(prefix string) domain.ScenarioGraph {
	nodes := []domain.ScenarioNode{
		kubernetes1175Node(prefix+"preflight", "Host Preflight", "release-host-preflight-k8s-1.17.5", "all", 40, 300),
		kubernetes1175Node(prefix+"bootstrap-master", "Host Bootstrap · Control", "release-host-bootstrap-k8s-1.17.5", "k8smaster", 300, 180),
		kubernetes1175Node(prefix+"bootstrap-worker", "Host Bootstrap · Worker", "release-host-bootstrap-k8s-1.17.5", "k8snode", 300, 420),
		kubernetes1175Node(prefix+"pki", "Cluster PKI", "release-cluster-pki-k8s-1.17.5", "k8s_cert_controller", 560, 60),
		kubernetes1175Node(prefix+"docker-master", "Docker · Control", "release-docker-18.09.7", "k8smaster", 560, 180),
		kubernetes1175Node(prefix+"docker-worker", "Docker · Worker", "release-docker-18.09.7", "k8snode", 560, 540),
		kubernetes1175Node(prefix+"distribution-master", "Distribution · Control", "release-kubernetes-distribution-1.17.5", "k8smaster", 820, 120),
		kubernetes1175Node(prefix+"distribution-worker", "Distribution · Worker", "release-kubernetes-distribution-1.17.5", "k8snode", 820, 480),
		kubernetes1175Node(prefix+"etcd", "etcd", "release-etcd-3.3.10", "k8setcd", 820, 0),
		kubernetes1175Node(prefix+"encryption", "Encryption Configuration", "release-kubernetes-encryption-config-1.17.5", "k8smaster", 1080, 60),
		kubernetes1175Node(prefix+"apiserver", "kube-apiserver", "release-kube-apiserver-1.17.5", "k8smaster", 1340, 60),
		kubernetes1175Node(prefix+"controller", "kube-controller-manager", "release-kube-controller-manager-1.17.5", "k8smaster", 1600, 0),
		kubernetes1175Node(prefix+"scheduler", "kube-scheduler", "release-kube-scheduler-1.17.5", "k8smaster", 1600, 100),
		kubernetes1175Node(prefix+"rbac", "Bootstrap RBAC", "release-kubernetes-bootstrap-rbac-1.17.5", "k8smaster", 1600, 200),
		kubernetes1175Node(prefix+"flannel-master", "Flannel · Control", "release-flannel-0.11.0", "k8smaster", 1080, 300),
		kubernetes1175Node(prefix+"flannel-worker", "Flannel · Worker", "release-flannel-0.11.0", "k8snode", 1080, 540),
		kubernetes1175Node(prefix+"kubelet-master", "kubelet · Control", "release-kubelet-1.17.5", "k8smaster", 1860, 300),
		kubernetes1175Node(prefix+"kubelet-worker", "kubelet · Worker", "release-kubelet-1.17.5", "k8snode", 1860, 540),
		kubernetes1175Node(prefix+"proxy-master", "kube-proxy · Control", "release-kube-proxy-1.17.5", "k8smaster", 2120, 300),
		kubernetes1175Node(prefix+"proxy-worker", "kube-proxy · Worker", "release-kube-proxy-1.17.5", "k8snode", 2120, 540),
		kubernetes1175Node(prefix+"coredns", "CoreDNS", "release-coredns-1.3.1", "k8smaster", 2380, 180),
	}
	edgePairs := [][2]string{
		{"preflight", "bootstrap-master"}, {"preflight", "bootstrap-worker"},
		{"bootstrap-master", "pki"}, {"bootstrap-master", "docker-master"}, {"bootstrap-worker", "docker-worker"},
		{"pki", "etcd"}, {"pki", "distribution-master"}, {"pki", "distribution-worker"}, {"pki", "encryption"},
		{"distribution-master", "encryption"}, {"distribution-master", "apiserver"}, {"encryption", "apiserver"}, {"etcd", "apiserver"}, {"pki", "apiserver"},
		{"apiserver", "controller"}, {"distribution-master", "controller"}, {"pki", "controller"},
		{"apiserver", "scheduler"}, {"distribution-master", "scheduler"}, {"pki", "scheduler"}, {"apiserver", "rbac"},
		{"docker-master", "flannel-master"}, {"etcd", "flannel-master"}, {"pki", "flannel-master"},
		{"docker-worker", "flannel-worker"}, {"etcd", "flannel-worker"}, {"pki", "flannel-worker"},
		{"docker-master", "kubelet-master"}, {"distribution-master", "kubelet-master"}, {"flannel-master", "kubelet-master"}, {"apiserver", "kubelet-master"}, {"rbac", "kubelet-master"},
		{"docker-worker", "kubelet-worker"}, {"distribution-worker", "kubelet-worker"}, {"flannel-worker", "kubelet-worker"}, {"apiserver", "kubelet-worker"}, {"rbac", "kubelet-worker"},
		{"kubelet-master", "proxy-master"}, {"distribution-master", "proxy-master"}, {"pki", "proxy-master"},
		{"kubelet-worker", "proxy-worker"}, {"distribution-worker", "proxy-worker"}, {"pki", "proxy-worker"},
		{"apiserver", "coredns"}, {"flannel-master", "coredns"}, {"flannel-worker", "coredns"}, {"kubelet-master", "coredns"}, {"kubelet-worker", "coredns"}, {"proxy-master", "coredns"}, {"proxy-worker", "coredns"},
	}
	edges := make([]domain.ScenarioEdge, 0, len(edgePairs))
	for i, pair := range edgePairs {
		edges = append(edges, domain.ScenarioEdge{ID: fmt.Sprintf("%sedge-%02d", prefix, i+1), Source: prefix + pair[0], Target: prefix + pair[1]})
	}
	return domain.ScenarioGraph{Nodes: nodes, Edges: edges}
}

func (s Seeder) seedKubernetes1175Scenarios(ctx context.Context, now time.Time) error {
	coreGraph := kubernetes1175CoreGraph("k8s1175-")
	core := domain.Scenario{ID: "scenario-k8s-1.17.5", Slug: "kubernetes-1-17-5-cluster-build", Name: "Kubernetes 1.17.5 Cluster Build", Description: "按最小逻辑组件编排的 Kubernetes 1.17.5 核心集群 DAG。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	coreRevision := domain.ScenarioRevision{ID: "scenario-k8s-1.17.5-r1", ScenarioID: core.ID, Revision: 1, Status: domain.RevisionDraft, Graph: coreGraph, ExecutionPolicy: map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention", "destructive": true}, CreatedAt: now}
	if _, err := s.createScenarioIfMissing(ctx, core, coreRevision); err != nil {
		return fmt.Errorf("seed Kubernetes 1.17.5 core scenario: %w", err)
	}

	extendedGraph := kubernetes1175CoreGraph("k8s1175-ext-")
	optionalNodes := []domain.ScenarioNode{
		kubernetes1175Node("k8s1175-ext-logging-master", "Node Logging · Control", "release-node-logging-k8s-1.17.5", "k8smaster", 2640, 0),
		kubernetes1175Node("k8s1175-ext-logging-worker", "Node Logging · Worker", "release-node-logging-k8s-1.17.5", "k8snode", 2640, 100),
		kubernetes1175Node("k8s1175-ext-haproxy", "HAProxy", "release-haproxy-source-6909da3", "k8smaster", 2640, 200),
		kubernetes1175Node("k8s1175-ext-blackbox-etcd", "Blackbox · etcd", "release-blackbox-exporter-source-6909da3", "k8setcd", 2640, 300),
		kubernetes1175Node("k8s1175-ext-blackbox-master", "Blackbox · Control", "release-blackbox-exporter-source-6909da3", "k8smaster", 2640, 400),
		kubernetes1175Node("k8s1175-ext-blackbox-worker", "Blackbox · Worker", "release-blackbox-exporter-source-6909da3", "k8snode", 2640, 500),
		kubernetes1175Node("k8s1175-ext-node-exporter-master", "Node Exporter · Control", "release-node-exporter-0.18.0", "k8smaster", 2900, 0),
		kubernetes1175Node("k8s1175-ext-node-exporter-worker", "Node Exporter · Worker", "release-node-exporter-0.18.0", "k8snode", 2900, 100),
		kubernetes1175Node("k8s1175-ext-metrics", "Metrics Server", "release-metrics-server-0.3.1", "k8smaster", 2900, 200),
		kubernetes1175Node("k8s1175-ext-amc-master", "AMC · Control", "release-amc-source-6909da3", "k8smaster", 2900, 300),
		kubernetes1175Node("k8s1175-ext-amc-worker", "AMC · Worker", "release-amc-source-6909da3", "k8snode", 2900, 400),
		kubernetes1175Node("k8s1175-ext-gluster-master", "GlusterFS · Control", "release-glusterfs-client-3.12.6", "k8smaster", 2900, 500),
		kubernetes1175Node("k8s1175-ext-gluster-worker", "GlusterFS · Worker", "release-glusterfs-client-3.12.6", "k8snode", 2900, 600),
		kubernetes1175Node("k8s1175-ext-pprof-etcd", "Go/pprof · etcd", "release-go-pprof-toolkit-source-6909da3", "k8setcd", 3160, 0),
		kubernetes1175Node("k8s1175-ext-pprof-master", "Go/pprof · Control", "release-go-pprof-toolkit-source-6909da3", "k8smaster", 3160, 100),
		kubernetes1175Node("k8s1175-ext-pprof-worker", "Go/pprof · Worker", "release-go-pprof-toolkit-source-6909da3", "k8snode", 3160, 200),
		kubernetes1175Node("k8s1175-ext-prometheus-access", "Prometheus Access", "release-prometheus-access-bootstrap-k8s-1.17.5", "k8smaster", 3160, 300),
		kubernetes1175Node("k8s1175-ext-autoscaling-rbac", "Autoscaling RBAC", "release-autoscaling-rbac-k8s-1.17.5", "k8smaster", 3160, 400),
	}
	extendedGraph.Nodes = append(extendedGraph.Nodes, optionalNodes...)
	optionalEdges := [][2]string{
		{"bootstrap-master", "logging-master"}, {"bootstrap-worker", "logging-worker"},
		{"apiserver", "haproxy"}, {"pki", "haproxy"},
		{"bootstrap-master", "blackbox-etcd"}, {"bootstrap-master", "blackbox-master"}, {"bootstrap-worker", "blackbox-worker"},
		{"bootstrap-master", "node-exporter-master"}, {"bootstrap-worker", "node-exporter-worker"},
		{"apiserver", "metrics"}, {"kubelet-master", "amc-master"}, {"kubelet-worker", "amc-worker"},
		{"bootstrap-master", "gluster-master"}, {"bootstrap-worker", "gluster-worker"},
		{"bootstrap-master", "pprof-etcd"}, {"bootstrap-master", "pprof-master"}, {"bootstrap-worker", "pprof-worker"},
		{"apiserver", "prometheus-access"}, {"apiserver", "autoscaling-rbac"},
	}
	for i, pair := range optionalEdges {
		extendedGraph.Edges = append(extendedGraph.Edges, domain.ScenarioEdge{ID: fmt.Sprintf("k8s1175-ext-optional-edge-%02d", i+1), Source: "k8s1175-ext-" + pair[0], Target: "k8s1175-ext-" + pair[1]})
	}
	extended := domain.Scenario{ID: "scenario-k8s-1.17.5-extended", Slug: "kubernetes-1-17-5-extended-cluster-build", Name: "Kubernetes 1.17.5 Extended Cluster Build", Description: "完整包含核心 DAG，并追加源快照中具有真实任务入口的附加能力。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	extendedRevision := domain.ScenarioRevision{ID: "scenario-k8s-1.17.5-extended-r1", ScenarioID: extended.ID, Revision: 1, Status: domain.RevisionDraft, Graph: extendedGraph, ExecutionPolicy: map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention", "destructive": true}, CreatedAt: now}
	if _, err := s.createScenarioIfMissing(ctx, extended, extendedRevision); err != nil {
		return fmt.Errorf("seed Kubernetes 1.17.5 extended scenario: %w", err)
	}
	return nil
}

func (s Seeder) seedKubernetes1175Environment(ctx context.Context, now time.Time) error {
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{
		map[string]any{"name": "cert-controller", "address": "127.0.0.1", "groups": []any{"k8s_cert_controller"}},
		map[string]any{"name": "control-1", "address": "192.0.2.11", "groups": []any{"k8setcd", "k8smaster", "k8s_F5"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "control-2", "address": "192.0.2.12", "groups": []any{"k8setcd", "k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "control-3", "address": "192.0.2.13", "groups": []any{"k8setcd", "k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "worker-1", "address": "192.0.2.21", "groups": []any{"k8snode"}, "port": 22, "user": "sysop"},
	}})
	environment := domain.Environment{ID: "environment-k8s-1.17.5-template", Name: "Kubernetes 1.17.5 SUSE Template", Description: "TEST-NET 脱敏模板；未知校验和和镜像 digest 保持为空，安装入口 fail-closed。", OwnerID: EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.EnvironmentRevision{ID: "environment-k8s-1.17.5-template-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64", "os": "SUSE", "network": "IPv4", "templateOnly": true}, Inventory: inventory, Variables: map[string]string{"FILE_STATION": "192.0.2.80:8080"}, CredentialRefs: []domain.CredentialRef{{Name: "K8S_ENCRYPTION_KEY", Kind: "envVarRef", Reference: "NEWPLATFORM_K8S1175_ENCRYPTION_KEY", Configured: true}}, MaxConcurrent: 1, CreatedAt: now}
	if err := s.createEnvironmentIfMissing(ctx, environment, revision); err != nil {
		return fmt.Errorf("seed Kubernetes 1.17.5 environment: %w", err)
	}
	return nil
}
