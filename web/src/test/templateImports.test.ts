import { describe, expect, it, vi } from 'vitest';
import {
  executeComponentImport,
  parseComponentImportTemplate,
  type ComponentImportClient,
} from '../pages/componentTemplateImport';
import { parseScenarioTemplate, serializeScenarioTemplate } from '../pages/scenarioTemplate';

function componentTemplate(overrides: Record<string, unknown> = {}) {
  return [{
    component: {
      name: 'Runtime', slug: 'runtime', description: 'runtime', layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'core_required',
    },
    release: {
      version: '1.0.0', type: 'atomic', parameters: [], dependencies: [],
      actions: [
        { name: 'install', type: 'install', playbook: 'install.yml', timeoutSeconds: 60 },
        { name: 'verify', type: 'verify', playbook: 'verify.yml', timeoutSeconds: 60 },
      ],
    },
    playbooks: [
      { filename: 'install.yml', content: '---\n- hosts: all\n  tasks: []\n' },
      { filename: 'verify.yml', content: '---\n- hosts: all\n  tasks: []\n' },
    ],
    ...overrides,
  }];
}

describe('component template import', () => {
  it('creates an action-free Draft, saves files, then binds managed paths', async () => {
    const entries = parseComponentImportTemplate(JSON.stringify(componentTemplate()));
    const calls: string[] = [];
    let createInput: Record<string, unknown> | undefined;
    let updateInput: Record<string, unknown> | undefined;
    const client: ComponentImportClient = {
      createComponent: vi.fn(async (input) => {
        calls.push('component');
        return { ...input, id: 'component-runtime', ownerId: 'component-alice' } as never;
      }),
      createRelease: vi.fn(async (_componentId, input) => {
        calls.push('release');
        createInput = input;
        return { ...input, id: 'release-runtime', componentId: 'component-runtime', state: 'draft' } as never;
      }),
      savePlaybook: vi.fn(async (_releaseId, filename, content) => {
        calls.push(`playbook:${filename}`);
        return { filename, content, path: `managed/runtime/release-runtime/${filename}`, sha256: filename };
      }),
      updateRelease: vi.fn(async (_releaseId, input) => {
        calls.push('update');
        updateInput = input;
        return { ...input, id: 'release-runtime', componentId: 'component-runtime', state: 'draft' } as never;
      }),
    };
    await executeComponentImport(entries, client);
    expect(calls).toEqual(['component', 'release', 'playbook:install.yml', 'playbook:verify.yml', 'update']);
    expect(createInput?.actions).toEqual([]);
    expect((updateInput?.actions as Array<{ playbook: string }>).map((action) => action.playbook)).toEqual([
      'managed/runtime/release-runtime/install.yml',
      'managed/runtime/release-runtime/verify.yml',
    ]);
  });

  it('rejects missing, duplicate, and unreferenced playbooks before execution', () => {
    const base = componentTemplate()[0];
    const cases = [
      { ...base, playbooks: [{ filename: 'install.yml', content: '---\n[]\n' }] },
      { ...base, playbooks: [{ filename: 'install.yml', content: '---\n[]\n' }, { filename: 'install.yml', content: '---\n[]\n' }, { filename: 'verify.yml', content: '---\n[]\n' }] },
      { ...base, playbooks: [...base.playbooks, { filename: 'unused.yml', content: '---\n[]\n' }] },
    ];
    for (const value of cases) expect(() => parseComponentImportTemplate(JSON.stringify([value]))).toThrow();
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
});

describe('scenario template import', () => {
  const template = {
    nodes: [{
      id: 'runtime', type: 'component' as const, position: { x: 10, y: 20 },
      data: { label: 'Runtime', componentId: 'component-runtime', releaseId: 'release-runtime', action: 'install' as const, hostGroup: 'workers', values: {}, runInputs: [] },
    }],
    edges: [],
    executionPolicy: { failure: 'stop' },
  };

  it('round-trips nodes, edges, and execution policy without loss', () => {
    expect(parseScenarioTemplate(serializeScenarioTemplate(template))).toEqual(template);
  });

  it('rejects malformed nodes, invalid coordinates, and missing edge endpoints', () => {
    expect(() => parseScenarioTemplate('{"nodes":[{}],"edges":[],"executionPolicy":{}}')).toThrow(/节点结构无效/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, nodes: [{ ...template.nodes[0], position: { x: 'bad', y: 1 } }] }))).toThrow(/有限坐标/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, edges: [{ id: 'missing', source: 'runtime', target: 'absent' }] }))).toThrow(/不存在的节点/);
    expect(() => parseScenarioTemplate(JSON.stringify({ ...template, executionPolicy: [] }))).toThrow(/必须是对象/);
  });
});
