import type { ComponentCategory, ComponentKind, ComponentLayer, ComponentRequiredness } from './domain';

export interface ComponentLayerDefinition {
  value: ComponentLayer;
  code: string;
  label: string;
  description: string;
  categories: ComponentCategory[];
}

export const COMPONENT_LAYERS: ComponentLayerDefinition[] = [
  { value: 'host_foundation', code: 'L1', label: '主机基础与安全准备层', description: '主机准备、公共配置与信任材料', categories: ['preflight', 'bootstrap', 'security'] },
  { value: 'runtime_state', code: 'L2', label: '容器运行时与状态存储层', description: '容器执行能力与控制面状态存储', categories: ['runtime', 'state_store'] },
  { value: 'orchestration_core', code: 'L3', label: 'Kubernetes 编排核心层', description: '控制面、节点管理和 Service 转发', categories: ['control_plane', 'worker', 'network'] },
  { value: 'cluster_service', code: 'L4', label: '集群网络、服务发现与存储层', description: 'CNI、DNS、流量入口和持久化存储', categories: ['network', 'dns', 'ingress', 'storage'] },
  { value: 'observability_management', code: 'L5', label: '可观测与节点管理层', description: '指标、事件、日志与节点管理能力', categories: ['observability', 'node_management'] },
  { value: 'platform_extension', code: 'L6', label: '平台与可选集群扩展层', description: '平台能力、弹性与可选扩展', categories: ['platform', 'autoscaling'] },
];

export const COMPONENT_CATEGORY_LABELS: Record<ComponentCategory, string> = {
  preflight: '安装前检查', bootstrap: '主机初始化', security: '安全与证书', runtime: '容器运行时', state_store: '状态存储',
  control_plane: '控制面', worker: '工作节点', network: '网络', dns: '服务发现', ingress: '流量入口', storage: '存储',
  observability: '可观测', node_management: '节点管理', platform: '平台能力', autoscaling: '弹性扩展',
};

export const COMPONENT_KIND_LABELS: Record<ComponentKind, string> = {
  software: '独立软件', software_bundle: '软件组合', delivery_stage: '交付阶段',
};

export const COMPONENT_REQUIREDNESS_LABELS: Record<ComponentRequiredness, string> = {
  core_required: '核心必选', profile_required: '方案必选', optional: '可选',
};

export function componentLayer(value: ComponentLayer) {
  return COMPONENT_LAYERS.find((layer) => layer.value === value) ?? COMPONENT_LAYERS[5];
}
