import type { ComponentDependency, ComponentRelease, ScenarioEdge, ScenarioNodeData } from '../types/domain';

export interface ScenarioGraphNode {
  id: string;
  type?: string;
  position: { x: number; y: number };
  data: ScenarioNodeData;
}

export interface ScenarioGraphIssue {
  code: 'contract_unavailable' | 'invalid_edge_kind' | 'missing_dependency_node' | 'dependency_source_required' | 'dependency_source_invalid' | 'mixed_lifecycle_direction' | 'sequence_conflicts_dependency' | 'sequence_redundant';
  nodeId: string;
  dependencyId: string;
  message: string;
}

export function scenarioDependencyKey(release: ComponentRelease, dependency: ComponentDependency) {
  return dependency.id ?? `${release.id}:${dependency.releaseId}`;
}

export function scenarioDependencyEdgeId(dependencyId: string, sourceId: string, targetId: string) {
  return `dependency:${dependencyId}:${sourceId}:${targetId}`;
}

export function scenarioDependenciesForNode(node: Pick<ScenarioGraphNode, 'data'>, release?: ComponentRelease) {
  if (!release) return [];
  if (node.data.action !== 'verify') return release.dependencies ?? [];
  return release.dependencies?.filter((dependency) => dependency.parameterMappings?.length) ?? [];
}

const reverseDependencyAction = (action: ScenarioNodeData['action']) => action === 'rollback' || action === 'uninstall';
const mutationDirection = (action: ScenarioNodeData['action']) => {
  if (action && ['install', 'upgrade', 'configure'].includes(action)) return 1;
  if (action && ['rollback', 'uninstall'].includes(action)) return -1;
  return 0;
};

export function reconcileScenarioGraph(
  inputNodes: ScenarioGraphNode[],
  inputEdges: ScenarioEdge[],
  releases: Map<string, ComponentRelease>,
): { nodes: ScenarioGraphNode[]; edges: ScenarioEdge[]; issues: ScenarioGraphIssue[] } {
  const nodes = inputNodes.map((node) => ({
    ...node,
    data: { ...node.data, dependencySources: { ...(node.data.dependencySources ?? {}) } },
  }));
  // Missing detail is not an empty contract. Keep the persisted graph intact
  // until every referenced contract can participate in normalization.
  const unavailable = nodes.filter(node => !releases.has(node.data.releaseId));
  if (unavailable.length) return {
    nodes, edges: inputEdges.map(edge => ({ ...edge })),
    issues: unavailable.map(node => ({ code: 'contract_unavailable' as const, nodeId: node.id, dependencyId: '', message: node.data.contractAvailability === 'missing' ? '组件版本不存在，请联系负责人核对' : node.data.contractAvailability === 'unshared' ? '组件尚未共享，详细合同暂不可用' : '组件合同尚未加载完成' })),
  };
  const dependencyEdges: ScenarioEdge[] = [];
  const dependencyPairs = new Set<string>();
  const issues: ScenarioGraphIssue[] = [];
  for (const node of nodes) {
    const release = releases.get(node.data.releaseId);
    if (!release) continue;
    const nextSources: Record<string, string> = {};
    for (const dependency of scenarioDependenciesForNode(node, release)) {
      const dependencyId = scenarioDependencyKey(release!, dependency);
      const candidates = nodes.filter((candidate) => candidate.id !== node.id && candidate.data.releaseId === dependency.releaseId).map((candidate) => candidate.id).sort();
      if (!candidates.length) {
        issues.push({ code: 'missing_dependency_node', nodeId: node.id, dependencyId, message: '缺少合同锁定的上游 Release 节点' });
        continue;
      }
      const configured = node.data.dependencySources?.[dependencyId];
      const invalidConfigured = Boolean(configured && !candidates.includes(configured));
      let selected = configured && candidates.includes(configured) ? configured : undefined;
      if (!selected && candidates.length === 1) selected = candidates[0];
      if (!selected) {
        issues.push({
          code: invalidConfigured ? 'dependency_source_invalid' : 'dependency_source_required', nodeId: node.id, dependencyId,
          message: invalidConfigured ? '原依赖来源已失效，请重新选择' : '存在多个匹配的上游节点，请选择具体来源',
        });
        continue;
      }
      nextSources[dependencyId] = selected;
      if (dependency.kind === 'configuration') continue;
      const selectedNode = nodes.find((candidate) => candidate.id === selected)!;
      const sourceDirection = mutationDirection(selectedNode.data.action);
      const targetDirection = mutationDirection(node.data.action);
      if (sourceDirection && targetDirection && sourceDirection !== targetDirection) {
        issues.push({ code: 'mixed_lifecycle_direction', nodeId: node.id, dependencyId, message: '同一依赖链不能混用正向变更与回滚/卸载动作' });
        continue;
      }
      const reverse = reverseDependencyAction(node.data.action);
      const source = reverse ? node.id : selected;
      const target = reverse ? selected : node.id;
      const edge = { id: scenarioDependencyEdgeId(dependencyId, source, target), source, target, kind: 'dependency' as const, dependencyId };
      dependencyEdges.push(edge);
      dependencyPairs.add(`${edge.source}\u0000${edge.target}`);
    }
    node.data.dependencySources = nextSources;
  }

  const seenPairs = new Set<string>();
  const sequenceEdges: ScenarioEdge[] = [];
  for (const input of inputEdges) {
    if (input.kind === 'dependency') continue;
    if (input.kind !== 'sequence') {
      issues.push({ code: 'invalid_edge_kind', nodeId: input.target, dependencyId: input.id, message: '连线必须明确指定依赖或手工顺序类型' });
      continue;
    }
    const pair = `${input.source}\u0000${input.target}`;
    if (seenPairs.has(pair)) continue;
    const dependencyAlreadyOrdersPair = dependencyPairs.has(pair) || isScenarioNodeReachable(input.source, input.target, dependencyEdges);
    if (input.kind === 'sequence' && dependencyAlreadyOrdersPair) {
      issues.push({ code: 'sequence_redundant', nodeId: input.target, dependencyId: input.id, message: '手工顺序与已有依赖路径重复' });
    }
    if (isScenarioNodeReachable(input.target, input.source, dependencyEdges)) {
      issues.push({
        code: 'sequence_conflicts_dependency',
        nodeId: input.target,
        dependencyId: input.id,
        message: '手工顺序与 Release 依赖方向冲突',
      });
    }
    seenPairs.add(pair);
    sequenceEdges.push({ id: input.id, source: input.source, target: input.target, kind: 'sequence' });
  }
  const edges = [...sequenceEdges, ...dependencyEdges].sort(compareEdges);
  for (const edge of sequenceEdges) {
    if (issues.some((issue) => issue.code === 'sequence_redundant' && issue.dependencyId === edge.id)) continue;
    if (isScenarioNodeReachable(edge.source, edge.target, edges.filter((candidate) => candidate.id !== edge.id))) {
      issues.push({ code: 'sequence_redundant', nodeId: edge.target, dependencyId: edge.id, message: '手工顺序与已有拓扑路径重复' });
    }
  }
  return { nodes, edges, issues };
}

function compareEdges(left: ScenarioEdge, right: ScenarioEdge) {
  const leftKey = `${left.kind ?? ''}\u0000${left.source}\u0000${left.target}\u0000${left.id}`;
  const rightKey = `${right.kind ?? ''}\u0000${right.source}\u0000${right.target}\u0000${right.id}`;
  return leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0;
}

export function isScenarioNodeReachable(source: string, target: string, edges: Array<Pick<ScenarioEdge, 'source' | 'target'>>) {
  const forward = new Map<string, string[]>();
  for (const edge of edges) forward.set(edge.source, [...(forward.get(edge.source) ?? []), edge.target]);
  const queue = [source];
  const seen = new Set<string>();
  while (queue.length) {
    const current = queue.shift()!;
    if (current === target) return true;
    if (seen.has(current)) continue;
    seen.add(current);
    queue.push(...(forward.get(current) ?? []));
  }
  return false;
}

// Inspect the first undecided frontier without choosing among its nodes.
export function scenarioExecutionOrderNodes(nodes: Array<{ id: string }>, edges: Array<Pick<ScenarioEdge, 'source' | 'target'>>) {
  const indegree = new Map(nodes.map(node => [node.id, 0]));
  const next = new Map<string, string[]>();
  if (indegree.size !== nodes.length) return [];
  for (const edge of edges) {
    if (!indegree.has(edge.source) || !indegree.has(edge.target)) return [];
    indegree.set(edge.target, indegree.get(edge.target)! + 1);
    next.set(edge.source, [...(next.get(edge.source) ?? []), edge.target]);
  }
  // Detect cycles before reporting ordering choices.
  const remaining = new Map(indegree);
  const check = nodes.filter(node => remaining.get(node.id) === 0).map(node => node.id);
  let visited = 0;
  while (check.length) {
    const id = check.pop()!;
    visited++;
    for (const target of next.get(id) ?? []) {
      remaining.set(target, remaining.get(target)! - 1);
      if (remaining.get(target) === 0) check.push(target);
    }
  }
  if (visited !== nodes.length) return [];
  const ready = nodes.filter(node => indegree.get(node.id) === 0).map(node => node.id);
  while (ready.length) {
    if (ready.length > 1) return ready.sort();
    const id = ready.pop()!;
    for (const target of next.get(id) ?? []) {
      indegree.set(target, indegree.get(target)! - 1);
      if (indegree.get(target) === 0) ready.push(target);
    }
  }
  return [];
}
