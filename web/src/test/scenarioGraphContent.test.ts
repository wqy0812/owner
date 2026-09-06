import { describe, expect, it } from 'vitest';
import { scenarioGraphContent, scenarioGraphSignature, type ScenarioGraphContent } from '../types/scenarioGraphContent';
import { reconcileScenarioGraph } from '../pages/scenarioGraph';
import type { ComponentRelease } from '../types/domain';

const graph = (): ScenarioGraphContent => ({ nodes: [
  { id: 'a', position: { x: 0, y: 0 }, data: { label: 'A', componentId: 'ca', releaseId: 'ra', action: 'install', parameterValues: { entries: ['first', 'second'], config: { z: 1, a: 2 } }, dependencySources: {} } },
  { id: 'b', position: { x: 5, y: 0 }, data: { label: 'B', componentId: 'cb', releaseId: 'rb', action: 'install', parameterValues: {}, dependencySources: { z: 'a', a: 'a' } } },
], edges: [{ id: 'manual', source: 'a', target: 'b', kind: 'sequence' }], environmentConstraints: { architecture: ['arm64', 'amd64'] } });

describe('persisted graph signatures', () => {
  it('ignores object ordering, graph collection ordering and display metadata', () => {
    const a = graph(), b = graph();
    b.nodes[0].data = { ...b.nodes[0].data, layer: 'runtime_state', componentOwnerName: 'new owner', contractAvailability: 'available', version: 'renamed', parameterValues: { config: { a: 2, z: 1 }, entries: ['first', 'second'] } };
    b.nodes[1].data.dependencySources = { a: 'a', z: 'a' };
    b.nodes.reverse(); b.environmentConstraints = { architecture: ['amd64', 'arm64'], empty: [] };
    expect(scenarioGraphSignature(a)).toBe(scenarioGraphSignature(b));
    expect(scenarioGraphContent(b).graph.nodes[0].data).not.toHaveProperty('componentOwnerName');
  });
  it('normalizes only the save contracts empty values and preserves parameter array order', () => {
    const a = graph(), b = graph(); delete b.nodes[1].data.parameterValues;
    expect(scenarioGraphSignature(a)).toBe(scenarioGraphSignature(b));
    b.nodes[0].data.parameterValues!.entries = ['second', 'first'];
    expect(scenarioGraphSignature(a)).not.toBe(scenarioGraphSignature(b));
  });
  it.each(['position', 'release', 'source', 'edge', 'parameter', 'constraint'])('detects an actual %s edit', field => {
    const a = graph(), b = graph();
    if (field === 'position') b.nodes[0].position.x++;
    if (field === 'release') b.nodes[0].data.releaseId = 'new';
    if (field === 'source') b.nodes[1].data.dependencySources!.a = 'another';
    if (field === 'edge') b.edges[0].source = 'b';
    if (field === 'parameter') b.nodes[0].data.parameterValues!.added = false;
    if (field === 'constraint') b.environmentConstraints = { architecture: ['amd64'] };
    expect(scenarioGraphSignature(a)).not.toBe(scenarioGraphSignature(b));
  });
  it('does not mark a valid dependency mapping dirty when reconciliation reorders keys', () => {
    const input = graph(); input.edges = [];
    const releases = new Map([['ra', { id: 'ra', dependencies: [] }], ['rb', { id: 'rb', dependencies: [{ id: 'z', releaseId: 'ra', kind: 'configuration' }, { id: 'a', releaseId: 'ra', kind: 'configuration' }] }]]) as unknown as Map<string, ComponentRelease>;
    input.nodes[1].data.dependencySources = { a: 'a', z: 'a' };
    const output = reconcileScenarioGraph(input.nodes, input.edges, releases);
    expect(output.issues).toEqual([]);
    expect(scenarioGraphSignature(input)).toBe(scenarioGraphSignature({ ...output, environmentConstraints: input.environmentConstraints }));
    delete input.nodes[1].data.dependencySources.a;
    expect(scenarioGraphSignature(input)).not.toBe(scenarioGraphSignature({ ...output, environmentConstraints: input.environmentConstraints }));
  });
});
