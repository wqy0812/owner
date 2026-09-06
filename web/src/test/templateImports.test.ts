import sample from '../../../examples/components/host-foundation-example.json';
import { describe, expect, it } from 'vitest';
import { parseComponentImportTemplate } from '../pages/componentTemplateImport';
import { parseScenarioTemplate, serializeScenarioTemplate, validateScenarioTemplateReferences } from '../pages/scenarioTemplate';
import type { Component } from '../types/domain';

function componentTemplate(overrides: Record<string, unknown> = {}) {
  return [{
    component: {
      name: 'Runtime', slug: 'runtime', description: 'runtime', layer: 'runtime_state', tags: ['runtime', 'core'],
    },
    release: {
      lineName: 'Runtime 1.0', version: '1.0.0', parameters: [], dependencies: [],
      actions: [
        { id: 'install', name: 'install', type: 'install', playbook: 'tasks/install.yml', timeoutSeconds: 60 },
        { id: 'health', name: 'health', type: 'check', playbook: 'tasks/checks/health.yml', timeoutSeconds: 60 },
      ],
    },
    playbooks: [
      { filename: 'tasks/install.yml', content: '- assert:\n    that: true\n' },
      { filename: 'tasks/checks/health.yml', content: '- assert:\n    that: true\n' },
    ],
    ...overrides,
  }];
}

describe('component template import', () => {
  it('rejects missing, duplicate, and invalid role paths before execution', () => {
    const base = componentTemplate()[0];
    const cases = [
      { ...base, playbooks: [{ filename: 'tasks/install.yml', content: '---\n[]\n' }] },
      { ...base, playbooks: [{ filename: 'tasks/install.yml', content: '---\n[]\n' }, { filename: 'tasks/install.yml', content: '---\n[]\n' }, { filename: 'tasks/checks/health.yml', content: '---\n[]\n' }] },
      { ...base, playbooks: [...base.playbooks, { filename: 'unused.yml', content: '---\n[]\n' }] },
    ];
    for (const value of cases) expect(() => parseComponentImportTemplate(JSON.stringify([value]))).toThrow();
  });

  it('rejects unsupported component-template fields instead of silently dropping them', () => {
    const base = componentTemplate()[0];
    expect(() => parseComponentImportTemplate(JSON.stringify([{ ...base, release: { ...base.release, status: 'released' } }]))).toThrow(/不支持字段 status/);
    expect(() => parseComponentImportTemplate(JSON.stringify([{ ...base, playbooks: [{ ...base.playbooks[0], path: 'managed/forged.yml' }, base.playbooks[1]] }]))).toThrow(/不支持字段 path/);
  });

  it('rejects dependency cycles during full preflight', () => {
    const [first] = parseComponentImportTemplate(JSON.stringify(componentTemplate()));
    const second = structuredClone(first);
    second.component.slug = 'network';
    second.component.name = 'Network';
    first.release.dependencies = [{ componentSlug: 'network', parameterMappings: [] }];
    second.release.dependencies = [{ componentSlug: 'runtime', parameterMappings: [] }];
    expect(() => parseComponentImportTemplate(JSON.stringify([first, second]))).toThrow(/存在环/);
  });

  it('rejects invalid serialized fields and dependency mappings during client preflight', () => {
    const base = componentTemplate()[0];
    const invalid = [
      { ...base, release: { ...base.release, breaking: 'yes' } },
      { ...base, release: { ...base.release, parameters: [{ name: 'port', description: 'port', type: 'integer', visibility: 'public', modifiable: false, valueProvider: 'component_owner', fixedValue: '6443' }] } },
      { ...base, release: { ...base.release, parameters: [{ name: 'options', description: 'options', type: 'object', visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner' }] } },
      { ...base, release: { ...base.release, actions: [{ name: 'install', type: 'install', playbook: 'install.yml', timeoutSeconds: 0 }], }, playbooks: [base.playbooks[0]] },
      { ...base, release: { ...base.release, actions: [{ name: 'upgrade', type: 'upgrade', playbook: 'install.yml', timeoutSeconds: 60 }], }, playbooks: [base.playbooks[0]] },
      { ...base, release: { ...base.release, actions: [{ name: 'rollback', type: 'rollback', playbook: 'install.yml', timeoutSeconds: 60, fromReleaseId: 'future', toReleaseId: 'old' }], }, playbooks: [base.playbooks[0]] },
    ];
    for (const value of invalid) {
      expect(() => parseComponentImportTemplate(JSON.stringify([value]))).toThrow();
    }
  });

  it('validates public mapping sources, declared targets, and matching types', () => {
    const upstream: any = componentTemplate()[0];
    upstream.release.parameters = [{ name: 'port', description: 'port', type: 'integer', visibility: 'public', modifiable: false, valueProvider: 'component_owner', fixedValue: 6443 }];
    const downstream: any = structuredClone(upstream);
    downstream.component.slug = 'worker';
    downstream.component.name = 'Worker';
    downstream.release.parameters = [{ name: 'endpoint', description: 'endpoint', type: 'string', visibility: 'internal', modifiable: false, valueProvider: 'upstream_mapping' }];
    downstream.release.dependencies = [{ componentSlug: 'runtime', parameterMappings: [{ upstreamParameter: 'port', targetParameter: 'endpoint' }] }];
    expect(() => parseComponentImportTemplate(JSON.stringify([upstream, downstream]))).toThrow(/类型不一致/);
    downstream.release.parameters[0].type = 'integer';
    expect(parseComponentImportTemplate(JSON.stringify([upstream, downstream]))).toHaveLength(2);
  });
});

describe('scenario template import', () => {
  const template = {
    environmentConstraints: {},
    nodes: [{
      id: 'runtime', type: 'component' as const, position: { x: 10, y: 20 },
      data: { label: 'Runtime', componentId: 'component-runtime', releaseId: 'release-runtime', action: 'install' as const, parameterValues: {}, dependencySources: {} },
    }],
    edges: [],
  };

  it('round-trips nodes and edges without loss', () => {
    expect(parseScenarioTemplate(serializeScenarioTemplate(template))).toEqual(template);
  });

  it('round-trips typed dependency and sequence edges and rejects untyped edges', () => {
    const typed = {
      ...template,
      nodes: [template.nodes[0], { ...template.nodes[0], id: 'control', data: { ...template.nodes[0].data, label: 'Control' } }],
      edges: [
        { id: 'dependency', source: 'runtime', target: 'control', kind: 'dependency' as const, dependencyId: 'dep-runtime' },
      ],
    };
    expect(parseScenarioTemplate(serializeScenarioTemplate(typed))).toEqual(typed);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...typed, edges: [{ id: 'legacy', source: 'runtime', target: 'control' }] }))).toThrow(/kind/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...typed, edges: [{ id: 'bad', source: 'runtime', target: 'control', kind: 'dependency' }] }))).toThrow(/dependencyId/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...typed, edges: [{ id: 'bad', source: 'runtime', target: 'control', kind: 'sequence', dependencyId: 'dep-runtime' }] }))).toThrow(/顺序边/);
  });

  it('rejects malformed nodes, invalid coordinates, and missing edge endpoints', () => {
    expect(() => parseScenarioTemplate('{"nodes":[{}],"edges":[]}')).toThrow(/节点结构无效/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, nodes: [{ ...template.nodes[0], position: { x: 'bad', y: 1 } }] }))).toThrow(/有限坐标/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, edges: [{ id: 'missing', source: 'runtime', target: 'absent' }] }))).toThrow(/不存在的节点/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, executionPolicy: {} }))).toThrow(/不支持字段 executionPolicy/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, edges: [{ id: 'legacy', source: 'runtime', target: 'runtime', label: 'legacy' }] }))).toThrow(/不支持字段 label/);
  });

  it('rejects cycles and release/action mismatches before a draft can be replaced', () => {
    const cyclic = {
      ...template,
      nodes: [
        template.nodes[0],
        { ...template.nodes[0], id: 'worker', data: { ...template.nodes[0].data, label: 'Worker' } },
      ],
      edges: [
        { kind: 'sequence', id: 'runtime-worker', source: 'runtime', target: 'worker' },
        { kind: 'sequence', id: 'worker-runtime', source: 'worker', target: 'runtime' },
      ],
    };
    expect(() => parseScenarioTemplate(JSON.stringify(cyclic))).toThrow(/无环 DAG/);

    const component = {
      id: 'component-runtime', name: 'Runtime', slug: 'runtime', ownerId: 'component-owner-a',
      layer: 'runtime_state', tags: ['runtime', 'core'],
      releases: [{ id: 'release-runtime', componentId: 'component-runtime', lineId: 'line-runtime', lineName: 'Runtime 1.0', compatibility: 'not_applicable', version: '1.0.0', state: 'released', review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] }, actions: [{ type: 'verify', playbook: 'verify.yml' }] }],
    } as Component;
    expect(() => validateScenarioTemplateReferences(template, [component])).toThrow(/不支持 install/);
    const verifiedTemplate: any = structuredClone(template);
    verifiedTemplate.nodes[0].data.action = 'verify';
    expect(() => validateScenarioTemplateReferences(verifiedTemplate, [component])).not.toThrow();
    verifiedTemplate.nodes[0].data.componentId = 'component-other';
    expect(() => validateScenarioTemplateReferences(verifiedTemplate, [component])).toThrow(/不匹配/);
  });
});

it('validates the shared directory sample with an explicit dependency and public mapping', () => {
  const entries = parseComponentImportTemplate(JSON.stringify(sample));
  expect(entries).toHaveLength(2);
  expect(entries[1].release.dependencies?.[0]).toMatchObject({componentSlug:'host-foundation-example',parameterMappings:[{upstreamParameter:'shared_root',targetParameter:'prepared_root'}]});
});
