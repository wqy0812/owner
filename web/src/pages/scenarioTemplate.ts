import type { ActionDefinition, ScenarioEdge, ScenarioNode } from '../types/domain';

export interface ScenarioTemplate {
  nodes: ScenarioNode[];
  edges: ScenarioEdge[];
  executionPolicy: Record<string, unknown>;
}

const ACTION_TYPES = new Set<ActionDefinition['type']>(['inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall']);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function requiredString(value: unknown, label: string) {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${label}不能为空。`);
  return value.trim();
}

function optionalStringMap(value: unknown, label: string): Record<string, string> | undefined {
  if (value === undefined) return undefined;
  if (!isRecord(value) || Object.values(value).some((item) => typeof item !== 'string')) throw new Error(`${label}必须是字符串映射。`);
  return value as Record<string, string>;
}

export function parseScenarioTemplate(text: string): ScenarioTemplate {
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new Error('场景模板不是有效 JSON。'); }
  if (!isRecord(raw) || !Array.isArray(raw.nodes) || !Array.isArray(raw.edges)) throw new Error('模板必须包含 nodes 和 edges 数组。');
  if (!isRecord(raw.executionPolicy ?? {})) throw new Error('executionPolicy 必须是对象。');

  const nodeIDs = new Set<string>();
  const nodes = raw.nodes.map((value, index) => {
    if (!isRecord(value) || !isRecord(value.position) || !isRecord(value.data)) throw new Error(`第 ${index + 1} 个节点结构无效。`);
    const id = requiredString(value.id, `nodes[${index}].id`);
    if (nodeIDs.has(id)) throw new Error(`节点 ID ${id} 重复。`);
    nodeIDs.add(id);
    const x = value.position.x;
    const y = value.position.y;
    if (typeof x !== 'number' || !Number.isFinite(x) || typeof y !== 'number' || !Number.isFinite(y)) throw new Error(`节点 ${id} 的 position 必须是有限坐标。`);
    const action = requiredString(value.data.action, `节点 ${id} 的 action`) as ActionDefinition['type'];
    if (!ACTION_TYPES.has(action)) throw new Error(`节点 ${id} 的 action ${action} 无效。`);
    if (value.data.values !== undefined && !isRecord(value.data.values)) throw new Error(`节点 ${id} 的 values 必须是对象。`);
    if (value.data.runInputs !== undefined && (!Array.isArray(value.data.runInputs) || value.data.runInputs.some((item) => typeof item !== 'string'))) throw new Error(`节点 ${id} 的 runInputs 必须是字符串数组。`);
    return {
      id,
      type: 'component' as const,
      position: { x, y },
      data: {
        ...value.data,
        label: requiredString(value.data.label, `节点 ${id} 的 label`),
        componentId: requiredString(value.data.componentId, `节点 ${id} 的 componentId`),
        releaseId: requiredString(value.data.releaseId, `节点 ${id} 的 releaseId`),
        action,
        hostGroup: requiredString(value.data.hostGroup, `节点 ${id} 的 hostGroup`),
        values: (value.data.values ?? {}) as Record<string, unknown>,
        runInputs: (value.data.runInputs ?? []) as string[],
        dependencySources: optionalStringMap(value.data.dependencySources, `节点 ${id} 的 dependencySources`),
      },
    };
  });

  const edgeIDs = new Set<string>();
  const edges = raw.edges.map((value, index) => {
    if (!isRecord(value)) throw new Error(`第 ${index + 1} 条边结构无效。`);
    const id = requiredString(value.id, `edges[${index}].id`);
    if (edgeIDs.has(id)) throw new Error(`边 ID ${id} 重复。`);
    edgeIDs.add(id);
    const source = requiredString(value.source, `边 ${id} 的 source`);
    const target = requiredString(value.target, `边 ${id} 的 target`);
    if (!nodeIDs.has(source) || !nodeIDs.has(target)) throw new Error(`边 ${id} 引用了不存在的节点。`);
    if (source === target) throw new Error(`边 ${id} 不能连接节点自身。`);
    return { id, source, target };
  });
  return { nodes, edges, executionPolicy: raw.executionPolicy as Record<string, unknown> ?? {} };
}

export function serializeScenarioTemplate(template: ScenarioTemplate) {
  return JSON.stringify({
    nodes: template.nodes.map(({ id, position, data }) => ({ id, type: 'component', position, data })),
    edges: template.edges.map(({ id, source, target }) => ({ id, source, target })),
    executionPolicy: template.executionPolicy,
  }, null, 2);
}
