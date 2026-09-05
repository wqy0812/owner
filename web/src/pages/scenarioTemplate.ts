import { executableActionTypes, type ActionDefinition, type Component, type ScenarioEdge, type ScenarioNode } from '../types/domain';
import { COMPONENT_LAYERS } from '../types/componentClassification';

export interface ScenarioTemplate {
  environmentConstraints?: Record<string, unknown>;
  nodes: ScenarioNode[];
  edges: ScenarioEdge[];
}

const ACTION_TYPES = new Set<ActionDefinition['type']>(['inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall']);
const LAYERS = new Set<string>(COMPONENT_LAYERS.map((layer) => layer.value));

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function requiredString(value: unknown, label: string) {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${label}不能为空。`);
  return value.trim();
}

function assertOnlyKeys(value: Record<string, unknown>, allowed: readonly string[], label: string) {
  const accepted = new Set(allowed);
  const unknown = Object.keys(value).find((key) => !accepted.has(key));
  if (unknown) throw new Error(`${label}包含不支持字段 ${unknown}。`);
}

function optionalStringMap(value: unknown, label: string): Record<string, string> | undefined {
  if (value === undefined) return undefined;
  if (!isRecord(value)) throw new Error(`${label}必须是字符串映射。`);
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [requiredString(key, `${label} 的键`), requiredString(item, `${label}.${key}`)]));
}

export function parseScenarioTemplate(text: string): ScenarioTemplate {
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new Error('场景模板不是有效 JSON。'); }
  if (!isRecord(raw) || !Array.isArray(raw.nodes) || !Array.isArray(raw.edges)) throw new Error('模板必须包含 nodes 和 edges 数组。');
  assertOnlyKeys(raw, ['nodes', 'edges', 'environmentConstraints'], '场景模板');

  const nodeIDs = new Set<string>();
  const nodes = raw.nodes.map((value, index) => {
    if (!isRecord(value) || !isRecord(value.position) || !isRecord(value.data)) throw new Error(`第 ${index + 1} 个节点结构无效。`);
    assertOnlyKeys(value, ['id', 'type', 'position', 'data'], `nodes[${index}]`);
    assertOnlyKeys(value.position, ['x', 'y'], `nodes[${index}].position`);
    assertOnlyKeys(value.data, ['label', 'componentId', 'releaseId', 'version', 'action', 'parameterValues', 'dependencySources', 'layer'], `nodes[${index}].data`);
    if (value.type !== undefined && value.type !== 'component') throw new Error(`nodes[${index}].type 必须是 component。`);
    const id = requiredString(value.id, `nodes[${index}].id`);
    if (nodeIDs.has(id)) throw new Error(`节点 ID ${id} 重复。`);
    nodeIDs.add(id);
    const x = value.position.x;
    const y = value.position.y;
    if (typeof x !== 'number' || !Number.isFinite(x) || typeof y !== 'number' || !Number.isFinite(y)) throw new Error(`节点 ${id} 的 position 必须是有限坐标。`);
    const action = requiredString(value.data.action, `节点 ${id} 的 action`) as ActionDefinition['type'];
    if (!ACTION_TYPES.has(action)) throw new Error(`节点 ${id} 的 action ${action} 无效。`);
    if (value.data.parameterValues !== undefined && !isRecord(value.data.parameterValues)) throw new Error(`节点 ${id} 的 parameterValues 必须是对象。`);
    const version = value.data.version === undefined ? undefined : requiredString(value.data.version, `节点 ${id} 的 version`);
    const layer = (value.data.layer === undefined ? undefined : requiredString(value.data.layer, `节点 ${id} 的 layer`)) as Component['layer'] | undefined;
    if (layer !== undefined && !LAYERS.has(layer)) throw new Error(`节点 ${id} 的 layer 无效。`);
    return {
      id,
      type: 'component' as const,
      position: { x, y },
      data: {
        label: requiredString(value.data.label, `节点 ${id} 的 label`),
        componentId: requiredString(value.data.componentId, `节点 ${id} 的 componentId`),
        releaseId: requiredString(value.data.releaseId, `节点 ${id} 的 releaseId`),
        action,
        parameterValues: (value.data.parameterValues ?? {}) as Record<string, unknown>,
        dependencySources: optionalStringMap(value.data.dependencySources, `节点 ${id} 的 dependencySources`),
        ...(version === undefined ? {} : { version }),
        ...(layer === undefined ? {} : { layer }),
      },
    };
  });

  const edgeIDs = new Set<string>();
  const edgePairs = new Set<string>();
  const edges = raw.edges.map((value, index) => {
    if (!isRecord(value)) throw new Error(`第 ${index + 1} 条边结构无效。`);
    assertOnlyKeys(value, ['id', 'source', 'target', 'kind', 'dependencyId'], `edges[${index}]`);
    const id = requiredString(value.id, `edges[${index}].id`);
    if (edgeIDs.has(id)) throw new Error(`边 ID ${id} 重复。`);
    edgeIDs.add(id);
    const source = requiredString(value.source, `边 ${id} 的 source`);
    const target = requiredString(value.target, `边 ${id} 的 target`);
    if (!nodeIDs.has(source) || !nodeIDs.has(target)) throw new Error(`边 ${id} 引用了不存在的节点。`);
    if (source === target) throw new Error(`边 ${id} 不能连接节点自身。`);
    const pair = `${source}\u0000${target}`;
    if (edgePairs.has(pair)) throw new Error(`边 ${id} 与已有连线重复。`);
    edgePairs.add(pair);
    const kind = requiredString(value.kind, `边 ${id} 的 kind`);
    if (kind !== 'dependency' && kind !== 'sequence') throw new Error(`边 ${id} 的 kind 必须是 dependency 或 sequence。`);
    const dependencyId = value.dependencyId === undefined ? undefined : requiredString(value.dependencyId, `边 ${id} 的 dependencyId`);
    if (kind === 'dependency' && !dependencyId) throw new Error(`依赖边 ${id} 必须包含 dependencyId。`);
    if (kind === 'sequence' && dependencyId) throw new Error(`顺序边 ${id} 不能包含 dependencyId。`);
    return { id, source, target, kind, ...(dependencyId === undefined ? {} : { dependencyId }) } as ScenarioEdge;
  });
  const outgoing = new Map<string, string[]>();
  const indegree = new Map([...nodeIDs].map((id) => [id, 0]));
  for (const edge of edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target]);
    indegree.set(edge.target, (indegree.get(edge.target) ?? 0) + 1);
  }
  const ready = [...indegree].filter(([, count]) => count === 0).map(([id]) => id);
  let visited = 0;
  while (ready.length) {
    const id = ready.shift()!;
    visited += 1;
    for (const target of outgoing.get(id) ?? []) {
      const next = (indegree.get(target) ?? 0) - 1;
      indegree.set(target, next);
      if (next === 0) ready.push(target);
    }
  }
  if (visited !== nodes.length) throw new Error('场景节点和边必须组成无环 DAG。');
  if (raw.environmentConstraints !== undefined && !isRecord(raw.environmentConstraints)) throw new Error('适配标签必须是对象。');
  return { nodes, edges, environmentConstraints: raw.environmentConstraints as Record<string, unknown> | undefined };
}

export function validateScenarioTemplateReferences(template: ScenarioTemplate, components: Component[]) {
  const releases = new Map(components.flatMap((component) => (component.releases ?? []).map((release) => [release.id, { component, release }] as const)));
  for (const node of template.nodes) {
    const match = releases.get(node.data.releaseId);
    if (!match) throw new Error(`节点 ${node.id} 引用了当前不可见或不存在的 Release。`);
    if (match.component.id !== node.data.componentId) throw new Error(`节点 ${node.id} 的 componentId 与 releaseId 不匹配。`);
    if (node.data.version !== undefined && node.data.version !== match.release.version) throw new Error(`节点 ${node.id} 的 version 与 releaseId 不匹配。`);
    if (node.data.layer !== undefined && node.data.layer !== match.component.layer) throw new Error(`节点 ${node.id} 的 layer 与 componentId 不匹配。`);
    const action = node.data.action!;
    if (!executableActionTypes(match.release.actions).includes(action)) throw new Error(`节点 ${node.id} 的 Release 不支持 ${action} 动作。`);
  }
}

export function serializeScenarioTemplate(template: ScenarioTemplate) {
  return JSON.stringify({
    environmentConstraints: template.environmentConstraints ?? {},
    nodes: template.nodes.map(({ id, position, data }) => ({
      id, type: 'component', position,
      data: {
        label: data.label, componentId: data.componentId, releaseId: data.releaseId, version: data.version,
        action: data.action, parameterValues: data.parameterValues ?? {}, dependencySources: data.dependencySources ?? {}, layer: data.layer,
      },
    })),
    edges: template.edges.map(({ id, source, target, kind, dependencyId }) => ({ id, source, target, ...(kind ? { kind } : {}), ...(dependencyId ? { dependencyId } : {}) })),
  }, null, 2);
}
