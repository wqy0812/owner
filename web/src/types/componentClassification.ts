import type { ComponentLayer } from './domain';

export interface ComponentLayerDefinition {
  value: ComponentLayer;
  code: string;
  label: string;
  description: string;
}

export const COMPONENT_LAYERS: ComponentLayerDefinition[] = [
  { value: 'host_foundation', code: 'L1', label: '主机基础与安全准备层', description: '主机准备、公共配置与信任材料' },
  { value: 'runtime_state', code: 'L2', label: '容器运行时与状态存储层', description: '容器执行能力与控制面状态存储' },
  { value: 'orchestration_core', code: 'L3', label: 'Kubernetes 编排核心层', description: '控制面、节点管理和 Service 转发' },
  { value: 'cluster_service', code: 'L4', label: '集群网络、服务发现与存储层', description: 'CNI、DNS、流量入口和持久化存储' },
  { value: 'observability_management', code: 'L5', label: '可观测与节点管理层', description: '指标、事件、日志与节点管理能力' },
  { value: 'platform_extension', code: 'L6', label: '平台与可选集群扩展层', description: '平台能力、弹性与可选扩展' },
];

export function componentLayer(value: ComponentLayer) {
  return COMPONENT_LAYERS.find((layer) => layer.value === value) ?? COMPONENT_LAYERS[5];
}
