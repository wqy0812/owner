package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
)

var openFuyaoEnvironmentPaths = map[string]string{
	"cluster_id":                   "operation.management_cluster_id",
	"strategy":                     "operation.strategy",
	"BKE_ADMIN":                    "artifact_sources.binaries.bke_admin",
	"JQ_MEDPATH":                   "artifact_sources.binaries.jq",
	"BOOTSTRAP_IMAGE":              "artifact_sources.binaries.bootstrap_image",
	"SSH_KEY_PUB":                  "ssh.public_key",
	"SSH_KNOWN_HOSTS":              "ssh.known_hosts",
	"ENV_DOCKER_HARBOR_DOMAIN":     "artifact_sources.registry.domain",
	"ENV_DOCKER_HARBOR_IP":         "artifact_sources.registry.ip",
	"ENV_DOCKER_HARBOR_PORT":       "artifact_sources.registry.port",
	"ENV_DOCKER_HARBOR_PORJECT":    "artifact_sources.registry.project",
	"ENV_FILESTATION_DOMAIN":       "artifact_sources.file_station.domain",
	"ENV_FILESTATION_IP":           "artifact_sources.file_station.ip",
	"ENV_FILESTATION_PORT":         "artifact_sources.file_station.port",
	"ENV_FILESTATION_PREFIX":       "artifact_sources.file_station.prefix",
	"ENV_FILESTATION_URL":          "artifact_sources.file_station.url",
	"ENV_CHART_REPO_PORT":          "artifact_sources.chart_repository.port",
	"chart_museum_url":             "artifact_sources.chart_repository.url",
	"helm_repo_name":               "artifact_sources.chart_repository.name",
	"ENV_AMC_USABLITY_ADDR":        "monitoring.amc_address",
	"ENV_AMC_USABLITY_PORT":        "monitoring.amc_port",
	"SERVICE_IP_RANGE_IPV4":        "network.service_ipv4_cidr",
	"CLUSTER_IP_RANGE_IPV4":        "network.pod_ipv4_cidr",
	"CLUSTER_IP_RANGE_IPV6":        "network.pod_ipv6_cidr",
	"CLUSTER_DNS_SVC_IP":           "network.cluster_dns_service_ip",
	"KUBERNETES_CLUSTER_IP":        "network.kubernetes_service_ip",
	"PORTMAP_HOST_PORT_MIN":        "network.host_port_min",
	"PORTMAP_HOST_PORT_MAX":        "network.host_port_max",
	"NET_IPV4_IP_LOCAL_PORT_RANGE": "network.local_port_range",
	"openFuyao_version":            "versions.openfuyao",
	"kubernetes_version":           "versions.kubernetes",
	"etcd_version":                 "versions.etcd",
	"containerd_version":           "versions.containerd",
	"calico_version":               "versions.calico",
	"coredns_version":              "versions.coredns",
	"kubeproxy_version":            "versions.kube_proxy",
	"openstack_vlan_version":       "versions.openstack_vlan",
	"redis_operator_version":       "versions.redis_operator",
	"harbor_secret_version":        "versions.harbor_secret_version",
	"pause_tag":                    "versions.pause",
	"cluster_api_version":          "versions.cluster_api",
	"bkeagent_deployer_version":    "versions.bkeagent_deployer",
	"bkeagent_deployer_tag":        "versions.bkeagent_deployer_tag",
	"IP_MOD_VERSION":               "versions.ip_mod",
	"certOutputPath":               "certificates.output_path",
	"certOutputFile":               "certificates.output_file",
	"cert_config":                  "certificates.config_path",
	"CERT_EXPIRY_TIME":             "certificates.expiry",
	"addon_params":                 "addon_params",
}

var openFuyaoProperties = map[string]map[string]any{
	"cluster_id":        {"type": "string", "minLength": 1},
	"cluster_role":      {"type": "string", "enum": []any{"manager", "work"}, "default": "manager"},
	"strategy":          {"type": "string", "enum": []any{"StatefulFlatNetworkStrategy", "StatelessPortMappingStrategy", "StatelessFlatNetworkStrategy"}},
	"target_host_group": {"type": "string", "enum": []any{"bootstrap_host", "management_cluster_k8smaster", "work_cluster_k8smaster", "work_cluster_k8snode"}},
	"addon_params":      {"type": "object"},
}

var openFuyaoStringParameters = []string{
	"BKE_ADMIN", "JQ_MEDPATH", "BOOTSTRAP_IMAGE", "SSH_KEY_PUB", "SSH_KNOWN_HOSTS",
	"ENV_DOCKER_HARBOR_DOMAIN", "ENV_DOCKER_HARBOR_IP", "ENV_DOCKER_HARBOR_PORJECT",
	"ENV_FILESTATION_DOMAIN", "ENV_FILESTATION_IP", "ENV_FILESTATION_PREFIX", "ENV_FILESTATION_URL",
	"chart_museum_url", "helm_repo_name", "ENV_AMC_USABLITY_ADDR",
	"SERVICE_IP_RANGE_IPV4", "CLUSTER_IP_RANGE_IPV4", "CLUSTER_IP_RANGE_IPV6", "CLUSTER_DNS_SVC_IP",
	"KUBERNETES_CLUSTER_IP", "NET_IPV4_IP_LOCAL_PORT_RANGE",
	"openFuyao_version", "kubernetes_version", "etcd_version", "containerd_version", "calico_version",
	"coredns_version", "kubeproxy_version", "openstack_vlan_version", "redis_operator_version",
	"harbor_secret_version", "pause_tag", "cluster_api_version", "bkeagent_deployer_version",
	"bkeagent_deployer_tag", "IP_MOD_VERSION", "certOutputPath", "certOutputFile", "cert_config",
	"CERT_EXPIRY_TIME",
}

var openFuyaoIntegerParameters = []string{
	"ENV_DOCKER_HARBOR_PORT", "ENV_FILESTATION_PORT", "ENV_CHART_REPO_PORT", "ENV_AMC_USABLITY_PORT",
	"PORTMAP_HOST_PORT_MIN", "PORTMAP_HOST_PORT_MAX",
}

func init() {
	for _, name := range openFuyaoStringParameters {
		openFuyaoProperties[name] = map[string]any{"type": "string", "minLength": 1}
	}
	for _, name := range openFuyaoIntegerParameters {
		openFuyaoProperties[name] = map[string]any{"type": "integer"}
	}
	for name, path := range openFuyaoEnvironmentPaths {
		if property := openFuyaoProperties[name]; property != nil {
			property["x-environmentPath"] = path
		}
	}
}

var (
	openFuyaoSSHCredentials      = []string{"ansible_ssh_pass"}
	openFuyaoRegistryCredentials = []string{
		"ansible_ssh_pass", "ENV_DOCKER_SECRET_USERNAME", "ENV_DOCKER_SECRET_PASSWORD",
	}
	openFuyaoRepositoryCredentials = []string{
		"ansible_ssh_pass", "ENV_DOCKER_SECRET_USERNAME", "ENV_DOCKER_SECRET_PASSWORD",
		"ENV_CHART_PULL_USERNAME", "ENV_CHART_PULL_PASSWORD",
	}
)

func openFuyaoSchema(keys ...string) map[string]any {
	properties := make(map[string]any, len(keys))
	required := make([]any, 0, len(keys))
	for _, key := range keys {
		definition, ok := openFuyaoProperties[key]
		if !ok {
			panic("missing OpenFuyao property definition: " + key)
		}
		copyDefinition := make(map[string]any, len(definition))
		for name, value := range definition {
			copyDefinition[name] = value
		}
		properties[key] = copyDefinition
		if _, hasDefault := definition["default"]; !hasDefault {
			required = append(required, key)
		}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": properties, "required": required,
	}
}

func openFuyaoSchemaDefault(schema map[string]any, key string, value any) {
	properties, _ := schema["properties"].(map[string]any)
	definition, ok := properties[key].(map[string]any)
	if !ok {
		return
	}
	definition["default"] = value
	required, _ := schema["required"].([]any)
	filtered := required[:0]
	for _, item := range required {
		if item != key {
			filtered = append(filtered, item)
		}
	}
	schema["required"] = filtered
}

func openFuyaoComponents(now time.Time, constraints map[string]any) []seededComponent {
	type spec struct {
		id, slug, name, owner, group string
		kind                         domain.ReleaseType
		parameters                   []string
		credentials                  []string
		tags                         []string
		dependencies                 []domain.ComponentDependency
	}
	specs := []spec{
		{id: "component-bke-cert", slug: "bke-cert", name: "BKE Certificates", owner: ComponentOwnerRuntimeID, group: "bootstrap_host", kind: domain.ReleaseAtomic,
			parameters: []string{"cluster_id", "target_host_group", "SERVICE_IP_RANGE_IPV4", "KUBERNETES_CLUSTER_IP", "certOutputPath", "certOutputFile", "cert_config", "CERT_EXPIRY_TIME"}, credentials: openFuyaoSSHCredentials, tags: []string{"ins"}},
		{id: "component-bke-bootstrap", slug: "bke-bootstrap", name: "BKE Bootstrap", owner: ComponentOwnerRuntimeID, group: "bootstrap_host", kind: domain.ReleaseAtomic,
			parameters: []string{"cluster_id", "target_host_group", "BKE_ADMIN", "SSH_KEY_PUB", "SSH_KNOWN_HOSTS", "ENV_CHART_REPO_PORT", "ENV_DOCKER_HARBOR_DOMAIN", "ENV_DOCKER_HARBOR_IP", "ENV_DOCKER_HARBOR_PORT", "ENV_DOCKER_HARBOR_PORJECT", "ENV_FILESTATION_URL", "BOOTSTRAP_IMAGE", "ENV_AMC_USABLITY_ADDR", "ENV_AMC_USABLITY_PORT", "openFuyao_version"}, credentials: openFuyaoSSHCredentials, tags: []string{"rcv", "ins"},
			dependencies: []domain.ComponentDependency{{UpstreamComponentID: "component-bke-cert", UpstreamReleaseID: "release-bke-cert-25.12", Purpose: "management cluster certificate bootstrap"}}},
		{id: "component-bke-common", slug: "bke-common", name: "BKE Common", owner: ComponentOwnerRuntimeID, group: "management_cluster_k8smaster", kind: domain.ReleaseAtomic,
			parameters: []string{"target_host_group", "ENV_DOCKER_HARBOR_DOMAIN", "ENV_DOCKER_HARBOR_PORT"}, credentials: openFuyaoRegistryCredentials, tags: []string{"image_plugin", "ins"}},
		{id: "component-bke-addon", slug: "bke-addon", name: "BKE Addons", owner: ComponentOwnerK8sID, group: "management_cluster_k8smaster", kind: domain.ReleaseBundle,
			parameters: []string{"target_host_group", "strategy", "ENV_FILESTATION_IP", "ENV_FILESTATION_PORT", "ENV_DOCKER_HARBOR_DOMAIN", "chart_museum_url", "helm_repo_name", "addon_params"}, credentials: openFuyaoRepositoryCredentials, tags: []string{"init"},
			dependencies: []domain.ComponentDependency{{UpstreamComponentID: "component-bke-common", UpstreamReleaseID: "release-bke-common-25.12", Purpose: "image credential provider and cluster logging"}}},
		{id: "component-bke-master", slug: "bke-master", name: "BKE Cluster Control Plane", owner: ComponentOwnerK8sID, group: "management_cluster_k8smaster", kind: domain.ReleaseAtomic,
			parameters: []string{
				"cluster_id", "cluster_role", "target_host_group", "strategy", "BKE_ADMIN", "JQ_MEDPATH", "SSH_KEY_PUB", "SSH_KNOWN_HOSTS",
				"ENV_DOCKER_HARBOR_DOMAIN", "ENV_DOCKER_HARBOR_IP", "ENV_DOCKER_HARBOR_PORT", "ENV_DOCKER_HARBOR_PORJECT",
				"ENV_FILESTATION_DOMAIN", "ENV_FILESTATION_IP", "ENV_FILESTATION_PORT", "ENV_FILESTATION_PREFIX", "ENV_FILESTATION_URL", "ENV_CHART_REPO_PORT",
				"SERVICE_IP_RANGE_IPV4", "CLUSTER_IP_RANGE_IPV4", "CLUSTER_IP_RANGE_IPV6",
				"CLUSTER_DNS_SVC_IP", "PORTMAP_HOST_PORT_MIN", "PORTMAP_HOST_PORT_MAX", "openFuyao_version", "kubernetes_version",
				"etcd_version", "containerd_version", "calico_version", "coredns_version", "kubeproxy_version", "openstack_vlan_version",
				"redis_operator_version", "harbor_secret_version", "pause_tag", "cluster_api_version", "bkeagent_deployer_version",
				"bkeagent_deployer_tag", "IP_MOD_VERSION", "certOutputPath", "certOutputFile", "addon_params",
			}, credentials: openFuyaoRegistryCredentials, tags: []string{"rcv", "ins"},
			dependencies: []domain.ComponentDependency{{UpstreamComponentID: "component-bke-addon", UpstreamReleaseID: "release-bke-addon-25.12", Purpose: "cluster manifests and chart repository preparation"}}},
		{id: "component-bke-nodes", slug: "bke-nodes", name: "BKE Work Nodes", owner: ComponentOwnerK8sID, group: "work_cluster_k8snode", kind: domain.ReleaseAtomic,
			parameters: []string{"cluster_id", "cluster_role", "target_host_group", "BKE_ADMIN", "JQ_MEDPATH", "SSH_KEY_PUB", "SSH_KNOWN_HOSTS", "ENV_FILESTATION_URL", "NET_IPV4_IP_LOCAL_PORT_RANGE"}, credentials: openFuyaoSSHCredentials, tags: []string{"image_plugin", "rcv", "ins"},
			dependencies: []domain.ComponentDependency{{UpstreamComponentID: "component-bke-master", UpstreamReleaseID: "release-bke-master-25.12", Purpose: "ready work-cluster control plane"}}},
	}

	items := make([]seededComponent, 0, len(specs))
	for _, item := range specs {
		releaseID := "release-" + item.slug + "-25.12"
		parameterSchema := openFuyaoSchema(item.parameters...)
		openFuyaoSchemaDefault(parameterSchema, "target_host_group", item.group)
		if item.slug == "bke-nodes" {
			openFuyaoSchemaDefault(parameterSchema, "cluster_role", "work")
			properties := parameterSchema["properties"].(map[string]any)
			properties["cluster_id"].(map[string]any)["x-environmentPath"] = "operation.work_cluster_id"
		}
		release := domain.ComponentRelease{
			ID: releaseID, ComponentID: item.id, Version: "v25.12", Type: item.kind, Status: domain.ReleaseReleased,
			ReleaseNotes: "OpenFuyao v25.12 作业快照的平台变量契约；真实介质和目标环境尚未验真。",
			Verified:     false, RiskLevel: domain.RiskDestructive, EnvironmentConstraints: constraints,
			ParameterSchema: parameterSchema, Dependencies: item.dependencies,
			Actions: []domain.ActionDefinition{{
				ID: "action-" + item.slug + "-install", ReleaseID: releaseID,
				Name: "OpenFuyao " + item.slug + " install", Kind: domain.ActionInstall,
				Playbook: "openfuyao/component-" + item.slug + ".platform.yml", Tags: item.tags,
				Limit: item.group, HostGroup: item.group, RequiredCredentials: append([]string(nil), item.credentials...),
				TimeoutSeconds: 3600, RiskLevel: domain.RiskDestructive, Destructive: true,
			}}, CreatedAt: now, ReleasedAt: ptr(now),
		}
		for i := range release.Dependencies {
			release.Dependencies[i].ID = fmt.Sprintf("dependency-%s-%d", item.slug, i+1)
			release.Dependencies[i].ReleaseID = releaseID
		}
		if item.slug == "bke-master" {
			release.Actions = append(release.Actions, domain.ActionDefinition{
				ID: "action-bke-master-verify", ReleaseID: releaseID, Name: "Verify BKE cluster readiness", Kind: domain.ActionVerify,
				Playbook: "openfuyao/component-bke-master-verify.platform.yml", Limit: item.group, HostGroup: item.group,
				RequiredCredentials: openFuyaoSSHCredentials, TimeoutSeconds: 300, RiskLevel: domain.RiskLow,
			})
		}
		description := "OpenFuyao v25.12 可独立编排的交付能力（真实作业快照）。"
		items = append(items, seededComponent{component: component(item.id, item.slug, item.name, description, item.owner, now), releases: []domain.ComponentRelease{release}})
	}
	return items
}

func openFuyaoBindings(releaseID, clusterPath string) map[string]string {
	bindings := map[string]string{}
	for _, item := range openFuyaoComponents(time.Time{}, map[string]any{}) {
		if item.releases[0].ID != releaseID {
			continue
		}
		properties, _ := item.releases[0].ParameterSchema["properties"].(map[string]any)
		for name, raw := range properties {
			definition, _ := raw.(map[string]any)
			if path, ok := definition["x-environmentPath"].(string); ok {
				bindings[name] = path
			}
		}
		break
	}
	if clusterPath != "" {
		bindings["cluster_id"] = clusterPath
	}
	bindings["strategy"] = "operation.strategy"
	delete(bindings, "cluster_role")
	delete(bindings, "target_host_group")
	return bindings
}

func openFuyaoNode(id, name, releaseID, group, role, clusterPath string, action domain.ActionKind, x float64) domain.ScenarioNode {
	values := map[string]any{"target_host_group": group}
	if role != "" {
		values["cluster_role"] = role
	}
	return domain.ScenarioNode{
		ID: id, Name: name, ReleaseID: releaseID, Action: action, HostGroup: group,
		Values: values, Bindings: openFuyaoBindings(releaseID, clusterPath), RunInputs: []string{},
		Position: domain.GraphPosition{X: x, Y: 100}, Destructive: action != domain.ActionVerify,
	}
}

func chainEdges(prefix string, nodes []domain.ScenarioNode) []domain.ScenarioEdge {
	edges := make([]domain.ScenarioEdge, 0, len(nodes)-1)
	for i := 0; i+1 < len(nodes); i++ {
		edges = append(edges, domain.ScenarioEdge{ID: fmt.Sprintf("%s-edge-%d", prefix, i+1), Source: nodes[i].ID, Target: nodes[i+1].ID})
	}
	return edges
}

func (s Seeder) seedOpenFuyaoScenarios(ctx context.Context, now time.Time) error {
	managerNodes := []domain.ScenarioNode{
		openFuyaoNode("open-manager-cert", "Certificates", "release-bke-cert-25.12", "bootstrap_host", "manager", "operation.management_cluster_id", domain.ActionInstall, 40),
		openFuyaoNode("open-manager-bootstrap", "Bootstrap", "release-bke-bootstrap-25.12", "bootstrap_host", "manager", "operation.management_cluster_id", domain.ActionInstall, 300),
		openFuyaoNode("open-manager-common", "Common", "release-bke-common-25.12", "management_cluster_k8smaster", "manager", "operation.management_cluster_id", domain.ActionInstall, 560),
		openFuyaoNode("open-manager-addon", "Addons", "release-bke-addon-25.12", "management_cluster_k8smaster", "manager", "operation.management_cluster_id", domain.ActionInstall, 820),
		openFuyaoNode("open-manager-master", "Management Control Plane", "release-bke-master-25.12", "management_cluster_k8smaster", "manager", "operation.management_cluster_id", domain.ActionInstall, 1080),
	}
	workNodes := []domain.ScenarioNode{
		openFuyaoNode("open-work-cert", "Work Cluster Certificates", "release-bke-cert-25.12", "management_cluster_k8smaster", "work", "operation.work_cluster_id", domain.ActionInstall, 40),
		openFuyaoNode("open-work-common", "Work Cluster Common", "release-bke-common-25.12", "work_cluster_k8smaster", "work", "operation.work_cluster_id", domain.ActionInstall, 300),
		openFuyaoNode("open-work-addon", "Work Cluster Addons", "release-bke-addon-25.12", "work_cluster_k8smaster", "work", "operation.work_cluster_id", domain.ActionInstall, 560),
		openFuyaoNode("open-work-master", "Work Cluster Control Plane", "release-bke-master-25.12", "work_cluster_k8smaster", "work", "operation.work_cluster_id", domain.ActionInstall, 820),
	}
	enrollmentNodes := []domain.ScenarioNode{
		openFuyaoNode("open-work-ready", "Verify Work Cluster", "release-bke-master-25.12", "work_cluster_k8smaster", "work", "operation.work_cluster_id", domain.ActionVerify, 40),
		openFuyaoNode("open-work-nodes", "Enroll Work Nodes", "release-bke-nodes-25.12", "work_cluster_k8snode", "work", "operation.work_cluster_id", domain.ActionInstall, 300),
	}

	definitions := []struct {
		scenario domain.Scenario
		revision domain.ScenarioRevision
	}{
		{
			scenario: domain.Scenario{ID: "scenario-openfuyao", Slug: "openfuyao-management-cluster", Name: "OpenFuyao Management Cluster Build", Description: "构建 OpenFuyao 管理集群；包含恢复步骤，执行前必须审批。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now},
			revision: domain.ScenarioRevision{ID: "scenario-openfuyao-r1", ScenarioID: "scenario-openfuyao", Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: managerNodes, Edges: chainEdges("open-manager", managerNodes)}, ExecutionPolicy: openFuyaoExecutionPolicy(), CreatedAt: now},
		},
		{
			scenario: domain.Scenario{ID: "scenario-openfuyao-work-cluster", Slug: "openfuyao-work-cluster", Name: "OpenFuyao Work Cluster Build", Description: "独立构建业务集群控制面，不自动纳管工作节点。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now},
			revision: domain.ScenarioRevision{ID: "scenario-openfuyao-work-cluster-r1", ScenarioID: "scenario-openfuyao-work-cluster", Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: workNodes, Edges: chainEdges("open-work", workNodes)}, ExecutionPolicy: openFuyaoExecutionPolicy(), CreatedAt: now},
		},
		{
			scenario: domain.Scenario{ID: "scenario-openfuyao-work-nodes", Slug: "openfuyao-work-node-enrollment", Name: "OpenFuyao Work Node Enrollment", Description: "先只读确认业务集群就绪，再独立纳管工作节点。", OwnerID: ScenarioOwnerID, CreatedAt: now, UpdatedAt: now},
			revision: domain.ScenarioRevision{ID: "scenario-openfuyao-work-nodes-r1", ScenarioID: "scenario-openfuyao-work-nodes", Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: enrollmentNodes, Edges: chainEdges("open-enroll", enrollmentNodes)}, ExecutionPolicy: openFuyaoExecutionPolicy(), CreatedAt: now},
		},
	}
	for _, definition := range definitions {
		if _, err := s.createScenarioIfMissing(ctx, definition.scenario, definition.revision); err != nil {
			return fmt.Errorf("seed OpenFuyao scenario %s: %w", definition.scenario.ID, err)
		}
	}
	return nil
}

func openFuyaoExecutionPolicy() map[string]any {
	return map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention", "destructive": true}
}

func (s Seeder) seedOpenFuyaoEnvironment(ctx context.Context, now time.Time) error {
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{
		map[string]any{"name": "bootstrap-1", "address": "192.0.2.10", "groups": []any{"bootstrap_host"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "manager-1", "address": "192.0.2.20", "groups": []any{"management_cluster_k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "work-master-1", "address": "192.0.2.30", "groups": []any{"work_cluster_k8smaster"}, "port": 22, "user": "sysop"},
		map[string]any{"name": "work-node-1", "address": "192.0.2.40", "groups": []any{"work_cluster_k8snode"}, "port": 22, "user": "sysop"},
	}})
	parameters := map[string]any{
		"operation": map[string]any{"management_cluster_id": "demo-management-cluster", "work_cluster_id": "demo-work-cluster", "strategy": "StatelessFlatNetworkStrategy"},
		"artifact_sources": map[string]any{
			"registry":         map[string]any{"domain": "registry.example.invalid", "ip": "192.0.2.60", "port": 443, "project": "openfuyao"},
			"chart_repository": map[string]any{"url": "https://charts.example.invalid", "port": 443, "name": "openfuyao"},
			"file_station":     map[string]any{"domain": "files.example.invalid", "ip": "192.0.2.70", "port": 443, "prefix": "/file-station", "url": "https://files.example.invalid/file-station"},
			"binaries":         map[string]any{"bke_admin": "bke-admin", "jq": "jq", "bootstrap_image": "bootstrap-image"},
		},
		"ssh":        map[string]any{"public_key": "/path/to/demo-only/id_ed25519.pub", "known_hosts": "/path/to/demo-only/known_hosts"},
		"monitoring": map[string]any{"amc_address": "192.0.2.80", "amc_port": 58080},
		"network": map[string]any{
			"service_ipv4_cidr": "10.96.0.0/12", "pod_ipv4_cidr": "192.168.0.0/16", "pod_ipv6_cidr": "fd00:10:244::/56",
			"cluster_dns_service_ip": "10.96.0.10", "kubernetes_service_ip": "10.96.0.1",
			"host_port_min": 30000, "host_port_max": 32767, "local_port_range": "1024 65535",
		},
		"versions": map[string]any{
			"openfuyao": "v25.12", "kubernetes": "v1.34.3-of.1", "etcd": "v3.6.7-of.1", "containerd": "v2.1.1",
			"calico": "v3.27.3-icbc", "coredns": "v1.12.2-of.1", "kube_proxy": "v1.34.3-of.1.icbc.harbor",
			"openstack_vlan": "v0.0.2", "redis_operator": "v1.0.0", "harbor_secret_version": "v1.0.0", "pause": "placeholder",
			"cluster_api": "1.0.5", "bkeagent_deployer": "source-6909da3", "bkeagent_deployer_tag": "source-6909da3", "ip_mod": "source-6909da3",
		},
		"certificates": map[string]any{"output_path": "/tmp/newplatform-demo-certs", "output_file": "cluster-certs.tar.gz", "config_path": "/etc/openFuyao/certs/cert_config", "expiry": "8760h"},
		"addon_params": map[string]any{"amc": map[string]any{}, "clusterconfig": map[string]any{}, "clustermonitor": map[string]any{}, "monitcontrollermanager": map[string]any{}, "housekeeping": map[string]any{}, "openstack-vlan": map[string]any{}},
	}
	credentialRefs := []domain.CredentialRef{
		{Name: "ansible_ssh_pass", Kind: "envVarRef", Reference: "NEWPLATFORM_OPENFUYAO_SSH_PASSWORD", Configured: true},
		{Name: "ENV_DOCKER_SECRET_USERNAME", Kind: "envVarRef", Reference: "NEWPLATFORM_OPENFUYAO_REGISTRY_USERNAME", Configured: true},
		{Name: "ENV_DOCKER_SECRET_PASSWORD", Kind: "envVarRef", Reference: "NEWPLATFORM_OPENFUYAO_REGISTRY_PASSWORD", Configured: true},
		{Name: "ENV_CHART_PULL_USERNAME", Kind: "envVarRef", Reference: "NEWPLATFORM_OPENFUYAO_CHART_USERNAME", Configured: true},
		{Name: "ENV_CHART_PULL_PASSWORD", Kind: "envVarRef", Reference: "NEWPLATFORM_OPENFUYAO_CHART_PASSWORD", Configured: true},
	}
	sort.Slice(credentialRefs, func(i, j int) bool { return credentialRefs[i].Name < credentialRefs[j].Name })
	environment := domain.Environment{ID: "environment-openfuyao-template", Name: "OpenFuyao Preflight Template", Description: "包含管理集群、业务控制面和业务节点的 TEST-NET 脱敏模板；不会连接真实基础设施。", OwnerID: EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.EnvironmentRevision{ID: "environment-openfuyao-template-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64", "os": "Kylin V10", "network": "IPv4", "templateOnly": true}, Inventory: inventory, Parameters: parameters, CredentialRefs: credentialRefs, MaxConcurrent: 1, CreatedAt: now}
	return s.createEnvironmentIfMissing(ctx, environment, revision)
}
