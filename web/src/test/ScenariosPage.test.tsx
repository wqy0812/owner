import { beforeEach, describe, expect, it, vi } from 'vitest';
import { answerConfirm, moreAction, optionsFor, selectOption } from './antdInteractions';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { act, fireEvent, screen, waitFor, within } from './render';
import { EventSourceMock } from './setup';
import { pendingResponse } from './testLifecycle';

import { ScenariosPage } from '../pages/ScenariosPage';
import { alice, carol, components, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(ScenariosPage, '/scenarios');

beforeEach(() => { installFetch(); });

describe("ScenariosPage", () => {
  it('downloads the current scenario 版本 as JSON instead of using the clipboard', async () => {
    installFetch({ initialUser: carol, withScenario: true });
    let exportedBlob: Blob | undefined;
    let downloadedAs = '';
    const createObjectURL = vi.fn((blob: Blob) => { exportedBlob = blob; return 'blob:scenario-export'; });
    const revokeObjectURL = vi.fn();
    const clipboardWrite = vi.fn();
    const createDescriptor = Object.getOwnPropertyDescriptor(URL, 'createObjectURL');
    const revokeDescriptor = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL');
    const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: clipboardWrite } });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function captureDownload(this: HTMLAnchorElement) {
      downloadedAs = this.download;
    });
    try {
      renderApp('/scenarios');
      await moreAction('导出 JSON');
      expect(downloadedAs).toBe('sample-cluster-build-r1.json');
      expect(exportedBlob?.type).toBe('application/json;charset=utf-8');
      const exportedText = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(exportedBlob!);
      });
      expect(JSON.parse(exportedText)).toMatchObject({ nodes: [{ id: 'component-step' }], edges: [] });
      expect(JSON.parse(exportedText)).not.toHaveProperty('executionPolicy');
      expect(clipboardWrite).not.toHaveBeenCalled();
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:scenario-export');
      expect(await screen.findByText('场景 JSON 已导出')).toBeInTheDocument();
    } finally {
      click.mockRestore();
      if (createDescriptor) Object.defineProperty(URL, 'createObjectURL', createDescriptor); else delete (URL as unknown as { createObjectURL?: unknown }).createObjectURL;
      if (revokeDescriptor) Object.defineProperty(URL, 'revokeObjectURL', revokeDescriptor); else delete (URL as unknown as { revokeObjectURL?: unknown }).revokeObjectURL;
      if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor); else delete (navigator as unknown as { clipboard?: Clipboard }).clipboard;
    }
  });

  it('adapts the backend graph DTO into an editable React Flow node', async () => {
    installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    const node = await screen.findByText('component-step');
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeInTheDocument();
    fireEvent.click(node);
    expect(screen.getByDisplayValue('all')).toBeDisabled();
    expect(screen.queryByRole('option', { name: 'rollback' })).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '目标集群动作' })).toBeDisabled();
    expect(screen.queryByRole('option', { name: 'uninstall' })).not.toBeInTheDocument();
  });

  it('generates a locked dependency edge from the exact Release contract and saves its binding', async () => {
    const runtimeRelease = {
      id: 'release-runtime-auto', componentId: 'component-runtime-auto', version: '1.0.0', status: 'released',
      dependencies: [], actions: [{ kind: 'install', playbook: 'runtime.yml', hostGroup: 'runtime_nodes' }],
    };
    const controlRelease = {
      id: 'release-control-auto', componentId: 'component-control-auto', version: '1.0.0', status: 'released',
      dependencies: [{ kind: 'execution', id: 'dep-runtime-auto', upstreamComponentId: 'component-runtime-auto', upstreamComponentName: 'Runtime', upstreamReleaseId: runtimeRelease.id, upstreamVersion: runtimeRelease.version, purpose: 'CRI', parameterMappings: [] }],
      actions: [{ kind: 'install', playbook: 'control.yml', hostGroup: 'control_plane' }],
    };
    const revision = {
      id: 'scenario-auto-r1', scenarioId: 'scenario-auto', revision: 1, state: 'draft', edges: [],
      nodes: [
        { id: 'runtime', type: 'component', position: { x: 40, y: 40 }, data: { label: 'Runtime node', componentId: 'component-runtime-auto', releaseId: runtimeRelease.id, action: 'install', hostGroup: 'runtime_nodes', parameterValues: {}, dependencySources: {} } },
        { id: 'control', type: 'component', position: { x: 340, y: 40 }, data: { label: 'Control node', componentId: 'component-control-auto', releaseId: controlRelease.id, action: 'install', hostGroup: 'control_plane', parameterValues: {}, dependencySources: {} } },
      ],
    };
    let savedGraph: Record<string, any> | undefined;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json([
        { id: 'component-runtime-auto', name: 'Runtime', slug: 'runtime-auto', ownerId: alice.id, layer: 'runtime_state', tags: [], latestRelease: runtimeRelease, releases: [runtimeRelease] },
        { id: 'component-control-auto', name: 'Control', slug: 'control-auto', ownerId: alice.id, layer: 'orchestration_core', tags: [], latestRelease: controlRelease, releases: [controlRelease] },
      ]);
      if (url.endsWith('/scenario-revisions/scenario-auto-r1/graph') && init?.method === 'PUT') {
        const submittedGraph = JSON.parse(String(init.body));
        savedGraph = submittedGraph;
        return json({ ...revision, ...submittedGraph.graph, nodes: submittedGraph.graph.nodes.map((node: any) => ({ ...node, data: { ...revision.nodes.find(item => item.id === node.id)?.data, ...node.data } })) });
      }
      if (url.endsWith('/scenarios')) return json([{ id: 'scenario-auto', name: 'Automatic graph', slug: 'automatic-graph', ownerId: carol.id, currentRevisionId: revision.id, currentRevision: revision, revisions: [revision] }]);
      if (url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({ generatedAt: '', role: carol.role, summary: {}, assets: {}, items: [] });
    }));

    renderApp('/scenarios');
    expect(await screen.findByText('自动依赖')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Control node'));
    expect(screen.getByText('版本依赖与配置来源')).toBeInTheDocument();
    expect(screen.getByDisplayValue(/Runtime node.*自动绑定/)).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(savedGraph).toBeDefined());
    const submittedGraph = (savedGraph as Record<string, any>).graph;
    expect(submittedGraph.nodes.find((node: any) => node.id === 'control').data.dependencySources).toEqual({ 'dep-runtime-auto': 'runtime' });
    expect(submittedGraph.edges).toEqual([expect.objectContaining({ source: 'runtime', target: 'control', kind: 'dependency', dependencyId: 'dep-runtime-auto' })]);
  });

  it('groups scenario-owned parameters by exact release and keeps repeated node values independent', async () => {
    const release = {
      id: 'release-parameter-overview', componentId: 'component-containerd', version: 'v3.0.0', status: 'released',
      parameters: [{ name: 'cpu', description: '容器 CPU', type: 'integer', required: true, visibility: 'public', modifiable: true, valueProvider: 'scenario_owner', suggestedValue: 4 }],
      actions: [{ kind: 'install', playbook: 'install.yml', hostGroup: 'runtime_nodes' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: release, releases: [release] }]);
      if (url.endsWith('/scenarios')) return json([{
        id: 'scenario-parameters', name: 'Parameter overview', slug: 'parameter-overview', ownerId: carol.id, currentRevisionId: 'scenario-parameters-r1',
        currentRevision: {
          id: 'scenario-parameters-r1', scenarioId: 'scenario-parameters', revision: 1, state: 'draft', edges: [],
          nodes: [
            { id: 'runtime-a', type: 'component', position: { x: 40, y: 40 }, data: { label: 'Runtime A', componentId: 'component-containerd', releaseId: release.id, action: 'install', hostGroup: 'runtime_nodes', parameterValues: {}, dependencySources: {} } },
            { id: 'runtime-b', type: 'component', position: { x: 340, y: 40 }, data: { label: 'Runtime B', componentId: 'component-containerd', releaseId: release.id, action: 'install', hostGroup: 'runtime_nodes', parameterValues: { cpu: 8 }, dependencySources: {} } },
          ],
        },
        revisions: [],
      }]);
      if (url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/scenarios');
    await userEvent.click(await screen.findByRole('button', { name: '参数总览' }));
    expect(screen.getByText('Runtime A')).toBeInTheDocument();
    expect(screen.getByText('Runtime B')).toBeInTheDocument();
    expect(screen.getByText('0/1 已完成')).toBeInTheDocument();
    expect(screen.getByText('1/1 已完成')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '采用建议值 4' }));
    expect(screen.getAllByText('1/1 已完成')).toHaveLength(2);
    expect(screen.getAllByRole('spinbutton', { name: 'cpu 的值' }).map((input) => input.getAttribute('value'))).toEqual(['4', '8']);
  });

  it('shows a clear empty state when a scenario has no scenario-owner parameters', async () => {
    installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    await userEvent.click(await screen.findByRole('button', { name: '参数总览' }));
    expect(screen.getByText('没有集群 Owner 参数')).toBeInTheDocument();
    expect(screen.getByText('当前 DAG 的参数全部由组件、环境或上游映射提供。')).toBeInTheDocument();
  });

  it('previews a new version from a released source and does not submit when cancelled', async () => {
    const testPassed = {
      id: 'scenario-confirm-r1', scenarioId: 'scenario-confirm', revision: 1, state: 'released',
      nodes: [], edges: [],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios/scenario-confirm/revision-clone-plan') && init?.method === 'POST') {
        return json({ scenarioId: 'scenario-confirm', sourceRevisionId: testPassed.id, sourceRevision: 1, nextRevision: 2, nodeCount: 0, edgeCount: 0, sourceRunId: 'formal-acceptance-run', planDigest: 'scenario-clone-plan' });
      }
      if (url.endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST') {
        return json({ ...testPassed, id: 'scenario-confirm-r2', revision: 2, state: 'draft' }, 201);
      }
      if (url.endsWith('/scenarios')) return json([{
        id: 'scenario-confirm', slug: 'scenario-confirm', name: 'Confirm Scenario', ownerId: carol.id,
        currentRevisionId: testPassed.id, currentRevision: testPassed, revisions: [testPassed],
      }]);
      if (url.endsWith('/environments')) return json([]);
      if (url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/scenarios');
    await userEvent.click(await screen.findByRole('button', { name: '新增版本' }));
    await screen.findByRole('dialog', { name: '新增版本预览' });
    expect(screen.getByText('锁定来源正式 Run：formal-acceptance-run')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: '新增版本' }));
    await userEvent.click(await screen.findByRole('button', { name: '确认新增版本' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(true));
    const submitted = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST');
    expect(JSON.parse(String(submitted?.[1]?.body))).toMatchObject({ sourceRunId: 'formal-acceptance-run', expectedPlanDigest: 'scenario-clone-plan' });
  });

  it('shows revision history and can abandon the current draft', async () => {
    const released = {
      id: 'scenario-history-r1', scenarioId: 'scenario-history', revision: 1, state: 'released',
      nodes: [], edges: [],
    };
    const draft = {
      id: 'scenario-history-r2', scenarioId: 'scenario-history', revision: 2, state: 'draft',
      nodes: [], edges: [],
    };
    const abandoned = { ...draft, state: 'abandoned' };
    let scenario = {
      id: 'scenario-history', slug: 'scenario-history', name: 'History Scenario', ownerId: carol.id,
      currentRevisionId: draft.id, currentRevision: draft, revisions: [draft, released],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenario-revisions/scenario-history-r2/abandon') && init?.method === 'POST') {
        scenario = { ...scenario, currentRevisionId: released.id, currentRevision: released, revisions: [abandoned, released] };
        return json(scenario);
      }
      if (url.endsWith('/scenarios')) return json([scenario]);
      if (url.endsWith('/environments')) return json([]);
      if (url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/scenarios');

    const revisionSelect = await screen.findByRole('combobox', { name: '版本' });
    expect(within(await optionsFor(revisionSelect)).getByRole('option', { name: 'r2 · 草稿 · 当前' })).toBeInTheDocument();
    expect(within(await optionsFor(revisionSelect)).getByRole('option', { name: 'r1 · 已发布' })).toBeInTheDocument();
    await selectOption(revisionSelect, /^r1 ·/);
    expect(screen.getByRole('button', { name: '环境运行' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '放弃草稿' })).not.toBeInTheDocument();
    await selectOption(revisionSelect, /^r2 ·/);
    await moreAction('放弃草稿');

    expect(await screen.findByText(/确认放弃版本 2 草稿/)).toHaveTextContent('系统将恢复到最近的不可变版本，当前草稿会保留在历史记录中。');
    await answerConfirm();
    expect(await screen.findByText('草稿已放弃')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('combobox', { name: '版本' }).closest('.ant-select')).toHaveTextContent('r1'));
    expect(within(await optionsFor(screen.getByRole('combobox', { name: '版本' }))).getByRole('option', { name: 'r2 · 已放弃' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增版本' })).toBeInTheDocument();
  });

  it('confirms and deletes a never-published scenario', async () => {
    const draft = {
      id: 'scenario-delete-r1', scenarioId: 'scenario-delete', revision: 1, state: 'draft',
      nodes: [], edges: [],
    };
    let scenarios = [{
      id: 'scenario-delete', slug: 'scenario-delete', name: 'Disposable Scenario', ownerId: carol.id,
      currentRevisionId: draft.id, currentRevision: draft, revisions: [draft],
    }];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      if (url.endsWith('/scenarios/scenario-delete') && init?.method === 'DELETE') {
        scenarios = [];
        return json({ deleted: true });
      }
      if (url.endsWith('/scenarios')) return json(scenarios);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/scenarios');

    await moreAction('删除场景');
    expect(await screen.findByText(/确认永久删除场景“Disposable Scenario”/)).toHaveTextContent('仅从未发布且从未产生 Run 的场景允许删除；此操作不可恢复。');
    await answerConfirm(false);
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-delete') && init?.method === 'DELETE')).toBe(false);

    await moreAction('删除场景');
    await answerConfirm();
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-delete') && init?.method === 'DELETE')).toBe(true));
    expect(await screen.findByText('场景已删除')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('开始编排第一个场景')).toBeInTheDocument());
  });

  it('allows the same release to be added more than once with independent host groups', async () => {
    installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    const paletteButton = await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    await userEvent.click(paletteButton);
    expect(await screen.findByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument();
    expect(screen.getAllByText('containerd').length).toBeGreaterThanOrEqual(2);
  });

  it('keeps unsaved scenario nodes when a component metadata refresh arrives', async () => {
    const fetchMock = installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    const paletteButton = await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    await userEvent.click(paletteButton);
    expect(await screen.findByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument();

    const callsBefore = fetchMock.mock.calls.length;
    act(() => EventSourceMock.instances.at(-1)?.emit('release.published'));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore));

    await waitFor(() => expect(screen.getByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument());
  });

  it('keeps persisted scenario content clean through selection, views and metadata refresh', async () => {
    const fetchMock = installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    await screen.findByText('component-step');
    const dirty = () => screen.queryByText(/目标集群有未保存的修改/);
    expect(dirty()).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('component-step'));
    await userEvent.click(screen.getByRole('button', { name: '参数总览' }));
    expect(dirty()).not.toBeInTheDocument();
    const callsBefore = fetchMock.mock.calls.length;
    act(() => EventSourceMock.instances.at(-1)?.emit('release.published'));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore));
    expect(dirty()).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '环境测试' })).toBeEnabled();
  });

  it.each(['success', 'failure', 'conflict'] as const)('preserves edits made during a scenario save: %s', async outcome => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    let complete: ((response: Response) => void) | undefined;
    let submitted: any;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/scenario-revisions/scenario-sample-r1/graph') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body));
        return pendingResponse(resolve => { complete = resolve; }, init?.signal);
      }
      return base(input, init);
    }));
    renderApp('/scenarios');
    await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(complete).toBeDefined());
    await userEvent.click(screen.getByRole('button', { name: /containerd.*已使用 1 次/ }));
    await act(async () => {
      complete!(await (outcome === 'success'
        ? json({ id: 'scenario-sample-r1', scenarioId: 'scenario-sample', revision: 1, state: 'draft', revisionDigest: 'saved-digest', ...submitted.graph, nodes: submitted.graph.nodes.map((node: any) => ({ ...node, data: { ...node.data, componentId: 'component-containerd' } })) })
        : json({ error: { code: outcome === 'conflict' ? 'conflict' : 'internal_error', message: outcome } }, outcome === 'conflict' ? 409 : 500)));
    });
    await screen.findByText(outcome === 'success' ? '场景图已保存' : '保存失败');
    expect(screen.getByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument();
    expect(screen.getByText(/目标集群有未保存的修改/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '环境测试' })).toBeDisabled();
  });

  it('ignores a late save after leaving and returning to a 版本', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    let complete: ((response: Response) => void) | undefined;
    let submitted: any;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scenario-revisions/scenario-sample-r1/graph') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body));
        return pendingResponse(resolve => { complete = resolve; }, init?.signal, { ignoreAbort: true });
      }
      if (url.endsWith('/scenarios')) {
        const body = await (await base(input, init)).json();
        body.items[0].revisions.push({ ...body.items[0].currentRevision, id: 'other-revision', revision: 2, state: 'released', nodes: [] });
        return json(body.items);
      }
      return base(input, init);
    }));
    renderApp('/scenarios');
    await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(complete).toBeDefined());
    await selectOption(screen.getByRole('combobox', { name: '版本' }), /^r2 ·/);
    await selectOption(screen.getByRole('combobox', { name: '版本' }), /^r1 ·/);
    await userEvent.click(screen.getByRole('button', { name: /containerd.*已使用 1 次/ }));
    await act(async () => { complete!(await json({ id: 'scenario-sample-r1', scenarioId: 'scenario-sample', revision: 1, state: 'draft', revisionDigest: 'late-digest', ...submitted.graph, nodes: submitted.graph.nodes.map((node: any) => ({ ...node, data: { ...node.data, componentId: 'component-containerd' } })) })); });
    await screen.findByText('场景图已保存');
    expect(screen.getByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument();
    expect(screen.getByText(/目标集群有未保存的修改/)).toBeInTheDocument();
  });







  it('blocks a full scenario test while the current draft DAG is empty', async () => {
    const baseFetch = installFetch({ initialUser: carol });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scenarios')) return json([{
        id: 'scenario-empty', name: 'Empty Scenario', ownerId: carol.id, slug: 'empty-scenario',
        currentRevisionId: 'scenario-empty-r1',
        currentRevision: { id: 'scenario-empty-r1', scenarioId: 'scenario-empty', revision: 1, state: 'draft', nodes: [], edges: [] },
        revisions: [{ id: 'scenario-empty-r1', scenarioId: 'scenario-empty', revision: 1, state: 'draft', nodes: [], edges: [] }],
      }]);
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/scenarios?selected=scenario-empty');
    await userEvent.click(await screen.findByRole('button', { name: '环境测试' }));
    await selectOption(screen.getByRole('combobox', { name: '场景执行环境' }), /测试环境|Test Environment/);

    expect(screen.getByRole('alert')).toHaveTextContent('当前 DAG 为空');
    expect(screen.getByRole('button', { name: '提交安装测试' })).toBeDisabled();
    expect(fetchMock.mock.calls.some(([request, requestInit]) => String(request).endsWith('/scenario-revisions/scenario-empty-r1/test-runs') && requestInit?.method === 'POST')).toBe(false);
  });

  it('submits a scenario test with a preview digest, execution mode and stable idempotency key', async () => {
    const baseFetch = installFetch({ initialUser: carol, withScenario: true });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scenario-revisions/scenario-sample-r1/execution-plan')) return json({ scenarioRevisionId: 'scenario-sample-r1', environmentId: 'environment-test', executionMode: 'install', planDigest: 'install-plan', ready: true, operations: [], steps: [], issues: [] });
      if (url.endsWith('/scenario-revisions/scenario-sample-r1/test-runs') && init?.method === 'POST') return json({ id: 'scenario-test-run', kind: 'scenario_test', status: 'queued', scenarioRevisionId: 'scenario-sample-r1', environmentId: 'environment-test' });
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/scenarios');
    await userEvent.click(await screen.findByRole('button', { name: '环境测试' }));
    await selectOption(screen.getByRole('combobox', { name: '场景执行环境' }), /测试环境|Test Environment/);
    expect(screen.getByRole('button', { name: '提交安装测试' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));
    await screen.findByText('计划已就绪');
    await userEvent.click(screen.getByRole('button', { name: '提交安装测试' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/scenario-revisions/scenario-sample-r1/test-runs'));
      expect(call).toBeDefined();
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({ environmentId: 'environment-test', executionMode: 'install', expectedPlanDigest: 'install-plan', idempotencyKey: expect.any(String) });
    });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '场景完整测试' })).not.toBeInTheDocument());
  });
});

describe("scenario workspace loading and settings", () => {
  it('waits for initial contracts without flashing missing-contract or order warnings', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    let finish!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => String(input).endsWith('/components') ? pendingResponse(resolve => { finish = resolve; }, init?.signal) : base(input, init)));
    renderApp('/scenarios');
    expect(await screen.findByText('正在加载场景工作区…')).toBeVisible();
    await waitFor(() => expect(finish).toBeTypeOf('function'));
    expect(screen.queryByText('组件合同暂不可用')).not.toBeInTheDocument();
    expect(screen.queryByText('执行顺序尚未确定，请补充手工顺序线')).not.toBeInTheDocument();
    await act(async () => finish(await json(components)));
    expect(await screen.findByRole('button', { name: /containerd.*已使用 1 次/ })).toBeVisible();
    expect(screen.queryByText('组件合同暂不可用')).not.toBeInTheDocument();
  });

  it('shows contract failures after the request ends and recovers with retry', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    let failed = true;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => failed && String(input).endsWith('/components') ? json({ error: { message: 'contract read failed' } }, 500) : base(input, init)));
    renderApp('/scenarios');
    expect(await screen.findByText('组件合同加载失败')).toBeVisible();
    expect(screen.queryByText('执行顺序尚未确定，请补充手工顺序线')).not.toBeInTheDocument();
    failed = false;
    await userEvent.click(screen.getByRole('button', { name: '重新检查' }));
    expect(await screen.findByRole('button', { name: /containerd.*已使用 1 次/ })).toBeVisible();
    expect(screen.queryByText('组件合同加载失败')).not.toBeInTheDocument();
  });

  it('keeps the canvas and frozen branch scope during a background contract refresh', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    let refreshing = false;
    let finish!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => refreshing && String(input).endsWith('/components') ? pendingResponse(resolve => { finish = resolve; }, init?.signal) : base(input, init)));
    renderApp('/scenarios');
    await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    const settings = screen.getByRole('region', { name: '适配检查' });
    expect(settings.closest('aside')).toHaveClass('node-inspector');
    const disclosure = settings.querySelector('details')!;
    expect(disclosure.open).toBe(false);
    await userEvent.click(within(settings).getByText('适配检查'));
    expect(within(settings).queryByRole('checkbox')).not.toBeInTheDocument();
    refreshing = true;
    act(() => EventSourceMock.instances.at(-1)?.emit('release.published'));
    await waitFor(() => expect(finish).toBeTypeOf('function'));
    expect(screen.getByRole('button', { name: /containerd.*已使用 1 次/ })).toBeVisible();
    expect(screen.getByLabelText('分支适配标签')).toBeInTheDocument();
    await act(async () => finish(await json(components)));
    expect(screen.getByLabelText('分支适配标签')).toBeInTheDocument();
    expect(disclosure.open).toBe(true);
  });

  it('resets settings on 版本 changes and keeps historical adaptation values read-only', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    const sample = (await (await base('/api/v1/scenarios')).json()).items[0];
    const historical = { ...sample.currentRevision, id: 'historical-r1', state: 'released', environmentConstraints: { architecture: ['arm64'] } };
    sample.currentRevision.revision = 2;
    sample.revisions = [sample.currentRevision, historical];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => String(input).endsWith('/scenarios') ? json([sample]) : base(input, init)));
    renderApp('/scenarios');
    const settings = await screen.findByRole('region', { name: '适配检查' });
    await userEvent.click(within(settings).getByText('适配检查'));
    expect(within(settings).queryByRole('checkbox')).not.toBeInTheDocument();
    await selectOption(screen.getByRole('combobox', { name: '版本' }), /^r1 ·/);
    await waitFor(() => expect(settings.querySelector('details')!.open).toBe(false));
    expect(settings.querySelector('.scenario-settings__summary')).toHaveTextContent('ARM/arm64');
    await userEvent.click(within(settings).getByText('适配检查'));
    expect(within(settings).queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.getByLabelText('分支适配标签')).toHaveTextContent('ARM/arm64');
    expect(screen.queryByRole('button', { name: '保存草稿' })).not.toBeInTheDocument();
    await selectOption(screen.getByRole('combobox', { name: '版本' }), /^r2 ·/);
    await waitFor(() => expect(settings.querySelector('details')!.open).toBe(false));
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeEnabled();
  });

  it('keeps the latest scene selected when switching during a slow background contract request', async () => {
    const base = installFetch({ initialUser: carol, withScenario: true });
    const sample = (await (await base('/api/v1/scenarios')).json()).items[0];
    const secondRevision = { ...sample.currentRevision, id: 'second-r1', scenarioId: 'second-scene', nodes: sample.currentRevision.nodes.map((node: { data: object }) => ({ ...node, data: { ...node.data, label: 'Second scene node' } })), environmentConstraints: { architecture: ['arm64'] } };
    const second = { ...sample, id: 'second-scene', name: 'Second scene', currentRevisionId: secondRevision.id, currentRevision: secondRevision, revisions: [secondRevision] };
    let slow = false;
    let finish!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/scenarios')) return json([sample, second]);
      if (slow && String(input).endsWith('/components')) return pendingResponse(resolve => { finish = resolve; }, init?.signal, { ignoreAbort: true });
      return base(input, init);
    }));
    renderApp('/scenarios');
    await screen.findByRole('region', { name: '适配检查' });
    slow = true;
    act(() => EventSourceMock.instances.at(-1)?.emit('release.published'));
    await waitFor(() => expect(finish).toBeTypeOf('function'));
    const picker = screen.getByRole('combobox', { name: '当前场景' });
    await selectOption(picker, second.name);
    await selectOption(picker, sample.name);
    await selectOption(picker, second.name);
    await act(async () => finish(await json(components)));
    expect(picker.closest('.ant-select')).toHaveTextContent(second.name);
    await userEvent.click(screen.getByRole('button', { name: '节点表' }));
    expect(screen.getByText('Second scene node')).toBeVisible();
    expect(screen.queryByText('组件合同暂不可用')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: '适配检查' }).querySelector('.scenario-settings__summary')).toHaveTextContent('ARM/arm64');
  });
});
