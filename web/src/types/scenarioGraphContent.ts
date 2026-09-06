import type { ScenarioEdge, ScenarioNode } from './domain';

export interface ScenarioGraphContent {
  nodes: Array<Pick<ScenarioNode, 'id' | 'position' | 'data'>>;
  edges: ScenarioEdge[];
  environmentConstraints?: Record<string, unknown>;
}

function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value)
    .filter(([, item]) => item !== undefined).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)
    .map(([key, item]) => [key, canonical(item)]));
  return value;
}

// This is the wire content, not React Flow's rendering state. Parameter arrays
// remain ordered; only graph collections and constraint sets are unordered.
export function scenarioGraphContent(input: ScenarioGraphContent) {
  const byId = (a: { id: string }, b: { id: string }) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  return {
    graph: {
      nodes: input.nodes.map(({ id, position, data }) => ({ id, type: 'component' as const, position,
        data: { label: data.label, releaseId: data.releaseId, action: data.action,
          parameterValues: data.parameterValues ?? {}, dependencySources: data.dependencySources ?? {} },
      })).sort(byId),
      edges: input.edges.map(({ id, source, target, kind, dependencyId }) => ({ id, source, target, kind, dependencyId })).sort(byId),
    },
    environmentConstraints: Object.fromEntries(Object.entries(input.environmentConstraints ?? {}).flatMap(([key, value]) => {
      if (value == null || value === '') return [];
      const values = Array.isArray(value) ? value : [value];
      return values.length ? [[key, [...new Set(values)].sort()]] : [];
    })),
  };
}

export function scenarioGraphSignature(input: ScenarioGraphContent): string {
  return JSON.stringify(canonical(scenarioGraphContent(input)));
}
