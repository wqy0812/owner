import { describe, expect, it } from 'vitest';
import { reconcileScenarioGraph, scenarioDependencyEdgeId } from '../pages/scenarioGraph';
import type { ComponentRelease, ScenarioNode } from '../types/domain';

const upstream = { id: 'release-runtime', dependencies: [] } as unknown as ComponentRelease;
const dependency = { id: 'dep-runtime', componentId: 'component-runtime', releaseId: upstream.id, parameterMappings: [] };
const downstream = { id: 'release-control', dependencies: [dependency] } as unknown as ComponentRelease;
const releases = new Map([[upstream.id, upstream], [downstream.id, downstream]]);

function node(id: string, releaseId: string, action: ScenarioNode['data']['action'] = 'install'): ScenarioNode {
  return { id, type: 'component', position: { x: 0, y: 0 }, data: { label: id, componentId: `component-${id}`, releaseId, action, parameterValues: {}, dependencySources: {} } };
}

describe('scenario dependency graph reconciliation', () => {
  it('automatically binds the unique exact Release and retains manual sequence edges', () => {
    const result = reconcileScenarioGraph(
      [node('runtime', upstream.id), node('control', downstream.id), node('dns', 'release-dns')],
      [{ id: 'manual', source: 'control', target: 'dns', kind: 'sequence' }],
      new Map([...releases, ['release-dns', { id: 'release-dns', dependencies: [] } as unknown as ComponentRelease]]),
    );
    expect(result.issues).toEqual([]);
    expect(result.nodes[1].data.dependencySources).toEqual({ 'dep-runtime': 'runtime' });
    expect(result.edges).toContainEqual({
      id: scenarioDependencyEdgeId('dep-runtime', 'runtime', 'control'), source: 'runtime', target: 'control', kind: 'dependency', dependencyId: 'dep-runtime',
    });
    expect(result.edges).toContainEqual({ id: 'manual', source: 'control', target: 'dns', kind: 'sequence' });
  });

  it('locks mutual configuration sources without adding an execution cycle', () => {
    const api = { id: 'api', dependencies: [{ id: 'data', componentId: 'kubelet', releaseId: 'kubelet', kind: 'configuration', parameterMappings: [{ sourceParameter: 'dir', targetParameter: 'dir' }] }] } as unknown as ComponentRelease;
    const kubelet = { id: 'kubelet', dependencies: [{ id: 'install-api', componentId: 'api', releaseId: 'api', parameterMappings: [{ sourceParameter: 'endpoint', targetParameter: 'endpoint' }] }] } as unknown as ComponentRelease;
    const result = reconcileScenarioGraph([node('api-node', 'api'), node('kubelet-node', 'kubelet')], [], new Map([[api.id, api], [kubelet.id, kubelet]]));
    expect(result.issues).toEqual([]);
    expect(result.edges).toHaveLength(1);
    expect(result.edges[0]).toMatchObject({ source: 'api-node', target: 'kubelet-node' });
    expect(result.nodes[0].data.dependencySources).toEqual({ data: 'kubelet-node' });
    expect(result.nodes[1].data.dependencySources).toEqual({ 'install-api': 'api-node' });
  });

  it('requires an explicit source when the exact upstream Release appears more than once', () => {
    const nodes = [node('runtime-a', upstream.id), node('runtime-b', upstream.id), node('control', downstream.id)];
    const unresolved = reconcileScenarioGraph(nodes, [], releases);
    expect(unresolved.issues).toMatchObject([{ code: 'dependency_source_required', nodeId: 'control' }]);
    expect(unresolved.edges).toEqual([]);

    nodes[2].data.dependencySources = { 'dep-runtime': 'runtime-b' };
    const selected = reconcileScenarioGraph(nodes, [], releases);
    expect(selected.issues).toEqual([]);
    expect(selected.edges[0]).toMatchObject({ source: 'runtime-b', target: 'control', kind: 'dependency' });
  });

  it('does not infer an ambiguous dependency source from a manual edge', () => {
    const result = reconcileScenarioGraph([node('runtime-a', upstream.id), node('runtime-b', upstream.id), node('control', downstream.id)], [{ id: 'manual', source: 'runtime-a', target: 'control', kind: 'sequence' }], releases);
    expect(result.issues).toMatchObject([{code: 'dependency_source_required'}]);
    expect(result.edges).toMatchObject([{kind: 'sequence'}]);
  });
  it('rejects untyped edges', () => {
    const result = reconcileScenarioGraph([node('runtime', upstream.id), node('control', downstream.id)], [{id:'old',source:'runtime',target:'control'}], releases);
    expect(result.issues).toContainEqual(expect.objectContaining({code:'invalid_edge_kind'}));
  });

  it('reports a manual sequence edge that reverses an automatic dependency', () => {
    const result = reconcileScenarioGraph(
      [node('runtime', upstream.id), node('control', downstream.id)],
      [{ id: 'reverse', source: 'control', target: 'runtime', kind: 'sequence' }],
      releases,
    );
    expect(result.issues).toContainEqual(expect.objectContaining({
      code: 'sequence_conflicts_dependency',
      dependencyId: 'reverse',
    }));
  });

  it('reports a manual sequence edge already guaranteed by the dependency graph', () => {
    const result = reconcileScenarioGraph(
      [node('runtime', upstream.id), node('control', downstream.id)],
      [{ id: 'redundant', source: 'runtime', target: 'control', kind: 'sequence' }],
      releases,
    );
    expect(result.issues).toContainEqual(expect.objectContaining({
      code: 'sequence_redundant',
      dependencyId: 'redundant',
    }));
  });

  it('does not add install-time dependencies to a verify-only node without mappings', () => {
    const result = reconcileScenarioGraph([node('verify', downstream.id, 'verify')], [], releases);
    expect(result.issues).toEqual([]);
    expect(result.edges).toEqual([]);
    expect(result.nodes[0].data.dependencySources).toEqual({});
  });

  it('reverses rollback dependencies while retaining the logical source', () => {
	const result = reconcileScenarioGraph([node('runtime', upstream.id, 'rollback'), node('control', downstream.id, 'rollback')], [], releases);
	expect(result.issues).toEqual([]);
	expect(result.edges[0]).toMatchObject({ source: 'control', target: 'runtime', kind: 'dependency' });
	expect(result.nodes[1].data.dependencySources).toEqual({ 'dep-runtime': 'runtime' });
  });

  it('rejects mixed forward and reverse mutation chains', () => {
	const result = reconcileScenarioGraph([node('runtime', upstream.id, 'install'), node('control', downstream.id, 'rollback')], [], releases);
	expect(result.issues).toContainEqual(expect.objectContaining({ code: 'mixed_lifecycle_direction', nodeId: 'control' }));
	expect(result.edges).toEqual([]);
  });
});
