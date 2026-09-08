import { beforeEach, describe, expect, it, vi } from 'vitest';
import { answerConfirm, moreAction, optionsFor, selectOption } from './antdInteractions';
import { defaultResponse } from './fixtures/appFixtures';
import { fillField, user as userEvent } from './interactions';
import { act, fireEvent, screen, waitFor, within } from './render';
import { pendingResponse } from './testLifecycle';

import { ComponentsPage } from '../pages/ComponentsPage';
import { admin, alice, components, dave, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(ComponentsPage, '/components');

beforeEach(() => { installFetch(); });

describe("ComponentContract", () => {
  it('allows an invalidated candidate Draft to be withdrawn', async () => {
    let candidateBody: Record<string, unknown> | undefined;
    const release = {
      id: 'release-stale-candidate', componentId: 'component-stale-candidate', version: '1.0.0-rc1', status: 'draft',
      candidate: true, readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [],
      actions: [{ kind: 'install', playbook: 'install.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-stale-candidate/candidate')) {
        candidateBody = JSON.parse(String(init?.body));
        return json({ ...release, candidate: false });
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-stale-candidate', name: 'Stale Candidate', slug: 'stale-candidate', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'],
        latestRelease: release, releases: [release],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components?selected=component-stale-candidate');
    const withdraw = await screen.findByRole('button', { name: '撤回候选' });
    expect(withdraw).toBeEnabled();
    await userEvent.click(withdraw);
    await waitFor(() => expect(candidateBody).toEqual({ candidate: false }));
  });

  it('shows which upstream component parameter a release depends on', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }, {
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', tags: ['network'],
        latestRelease: {
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ kind: 'execution', upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        },
        releases: [{
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ kind: 'execution', upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components?selected=component-kube-proxy');
    expect((await screen.findAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot')).length).toBeGreaterThan(0);
    expect(screen.getByText('内部参数')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    expect(screen.getByRole('dialog', { name: '1.17.5 Release 详情' })).toBeInTheDocument();
    expect(screen.getAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot').length).toBeGreaterThan(1);
  });

  it('keeps component navigation locked until a Draft save finishes and its dialog unmounts', async () => {
    const schedulerDraft = {
      id: 'release-scheduler-draft', componentId: 'component-scheduler', version: '1.17.5-r2',
      status: 'draft', releaseNotes: 'Scheduler draft', parameters: [], dependencies: [],
      actions: [{ name: 'rollback', kind: 'rollback', playbook: 'managed/scheduler/kube-scheduler-rollback.yml', fromReleaseId: '1.17.5-r2', toReleaseId: '1.17.5' }],
    };
    const proxyDraft = {
      id: 'release-proxy-draft', componentId: 'component-proxy', version: '1.17.5-r2',
      status: 'draft', releaseNotes: 'Proxy draft', parameters: [], dependencies: [], actions: [],
    };
    let resolveSave!: (response: Response) => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-scheduler-draft') && init?.method === 'PUT') return pendingResponse(resolve => { resolveSave = resolve; }, init?.signal);
      if (url.endsWith('/components')) return json([{
        id: 'component-scheduler', name: 'kube-scheduler', slug: 'kube-scheduler', ownerId: alice.id,
        layer: 'orchestration_core', tags: ['control_plane'],
        latestRelease: schedulerDraft, releases: [schedulerDraft],
      }, {
        id: 'component-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id,
        layer: 'orchestration_core', tags: ['network'],
        latestRelease: proxyDraft, releases: [proxyDraft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-scheduler');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    const draftDialog = screen.getByRole('dialog', { name: '配置 Draft 1.17.5-r2' });
    const editor = within(draftDialog);
    expect(editor.getByText('tasks/rollback.yml')).toBeInTheDocument();

    await userEvent.click(editor.getByRole('button', { name: '保存 Draft' }));
    expect(await editor.findByRole('button', { name: '保存中…' })).toBeDisabled();

    const proxyButton = screen.getByRole('button', { name: /kube-proxy/ });
    expect(proxyButton).toBeDisabled();
    expect(editor.getByRole('button', { name: '关闭' })).toBeDisabled();
    fireEvent.keyDown(draftDialog, { key: 'Escape', keyCode: 27 });
    expect(draftDialog).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'kube-scheduler' })).toBeInTheDocument();

    await act(async () => resolveSave(await json(schedulerDraft)));
    await waitFor(() => expect(draftDialog).not.toBeInTheDocument());

    expect(proxyButton).toBeEnabled();
    await userEvent.click(proxyButton);
    expect(await screen.findByRole('heading', { name: 'kube-proxy' })).toBeInTheDocument();
    expect(screen.queryByText('tasks/rollback.yml')).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input, init]) => String(input).includes('/component-releases/') && init?.method === 'PUT')).toHaveLength(1);
  });

  it('switches the visible contract when a component has multiple releases', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', tags: ['network'],
        latestRelease: { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', status: 'released', parameters: [], dependencies: [] },
        releases: [
          { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', status: 'released', parameters: [], dependencies: [] },
          {
            id: 'release-kube-proxy-old', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
            parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
            dependencies: [{ kind: 'execution', upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
          },
        ],
      }, {
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components?selected=component-kube-proxy');
    expect(await screen.findByText(/当前展示 .*1.17.5，因为它有参数映射/)).toBeInTheDocument();
    expect(screen.getAllByText(/本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot/).length).toBeGreaterThan(0);
    expect(screen.getByText(/1.17.5：本组件参数 kubeRoot/)).toBeInTheDocument();
    expect(screen.getByText(/1.17.5 的公开参数可被下游引用/)).toBeInTheDocument();
    expect(screen.getByLabelText('当前组件版本').closest('.ant-select')).toHaveTextContent('1.17.5');
    await selectOption(screen.getByLabelText('当前组件版本'), /1.34.3/);
    expect(screen.getByLabelText('当前组件版本').closest('.ant-select')).toHaveTextContent('1.34.3');
    expect(screen.getByText('没有直接依赖')).toBeInTheDocument();
    expect(screen.getByText(/1.34.3 锁定的上游/)).toBeInTheDocument();
    expect(screen.getByText(/1.34.3 的公开参数可被下游引用/)).toBeInTheDocument();
    expect(screen.queryByText(/当前展示 1.17.5，因为它有参数映射/)).not.toBeInTheDocument();
    await selectOption(screen.getByLabelText('当前组件版本'), /1.17.5/);
    expect(screen.getAllByText(/本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot/).length).toBeGreaterThan(0);
  });

  it('lets the owner edit dependencies and parameters on the component page', async () => {
    const draft = {
      id: 'release-kube-proxy-draft', componentId: 'component-kube-proxy', version: '1.17.6', status: 'draft',
      parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal', modifiable: false, valueProvider: 'upstream_mapping' }],
      dependencies: [] as Array<Record<string, unknown>>,
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-kube-proxy-draft/contract') && init?.method === 'PATCH') {
        return json({ ...draft, ...JSON.parse(String(init.body)), status: 'draft' });
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.34.3', status: 'draft', parameters: [] },
        releases: [
          { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.34.3', status: 'draft', parameters: [] },
          { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        ],
      }, {
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', tags: ['network'],
        latestRelease: draft,
        releases: [draft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/components?selected=component-kube-proxy');
    await userEvent.click(await screen.findByLabelText('编辑直接依赖'));
    const editor = within(document.getElementById('release-contract-editor')!);
    expect(editor.getByRole('button', { name: '新增依赖' })).toBeInTheDocument();
    expect(editor.queryByRole('button', { name: '新增参数' })).not.toBeInTheDocument();
    expect(editor.queryByLabelText(/内部/)).not.toBeInTheDocument();
    expect(editor.queryByRole('button', { name: '编辑参数 kubeRoot' })).not.toBeInTheDocument();
    await userEvent.click(editor.getByRole('button', { name: '新增依赖' }));
    await selectOption(editor.getByLabelText('上游组件'), 'kubelet');
    expect(within(await optionsFor(editor.getByLabelText('已发布版本'))).getByRole('option', { name: '1.34.3 · Draft' })).toBeInTheDocument();
    await selectOption(editor.getByLabelText('已发布版本'), /1.17.5/);
    await userEvent.click(editor.getByRole('button', { name: '增加映射' }));
    await selectOption(editor.getAllByLabelText('上游公开参数')[0], /kubeInstallRoot/);
    await selectOption(editor.getByLabelText('本 Release 目标参数'), /kubeRoot/);
    await userEvent.click(editor.getByRole('button', { name: '保存直接依赖' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/component-releases/release-kube-proxy-draft/contract') && init?.method === 'PATCH');
      expect(JSON.parse(String(call?.[1]?.body))).not.toHaveProperty('parameters');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        section: 'dependencies', expectedDefinitionGeneration: 0, newParameters: [], removeParameters: [],
        dependencies: [{
          kind: 'execution',
          upstreamComponentId: 'component-kubelet',
          upstreamReleaseId: 'release-kubelet',
          parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }],
        }],
      });
    });
  });

  it('creates a version from the dedicated entry with inherited scope', async () => {
    const released = components[0].releases[0];
    const draft = { ...released, id: 'release-containerd-draft', parentReleaseId: released.id, compatibility: 'compatible', version: 'v2.1.2', status: 'draft', parameters: [], dependencies: [] };
    let created = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: created ? draft : released, releases: created ? [draft, released] : [released] }]);
      if (url.endsWith('/components/component-containerd/release-draft-plan')) return json({ mode: 'evolution', lineId: 'line-component-containerd', lineName: 'component-containerd baseline', parentReleaseId: released.id, parentVersion: released.version, targetVersion: draft.version, compatibility: 'compatible', planDigest: 'draft-plan', actions: [], removedActions: [], playbooks: [], artifactCount: 0, imageCount: 0 });
      if (url.endsWith('/components/component-containerd/release-drafts')) { created = true; return json(draft); }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    vi.stubGlobal('confirm', vi.fn(() => true));
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增版本' }));
    expect(screen.getByRole('dialog', { name: '新增版本 · containerd' })).toBeInTheDocument();
    const editor = within(screen.getByRole('dialog'));
    expect(editor.queryByLabelText('x86/amd64')).not.toBeInTheDocument();
    await fillField(editor.getByPlaceholderText('v1.1.0'), 'v2.1.2');
    await fillField(editor.getByPlaceholderText('说明变化和下游注意事项'), '调整合同');
    await userEvent.click(editor.getByRole('button', { name: '创建版本' }));
    await answerConfirm();

    expect(await screen.findByRole('button', { name: '保存 Draft' })).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '配置 Draft v2.1.2' })).toBeInTheDocument();
  });

  it('groups versions by release line and renders the three compatibility badges', async () => {
    const root117 = { ...components[0].releases[0], id: 'release-117', lineId: 'line-117', lineName: 'Kubernetes 1.17', version: '1.17.17', compatibility: 'not_applicable' };
    const root134 = { ...components[0].releases[0], id: 'release-134', lineId: 'line-134', lineName: 'Kubernetes 1.34', version: '1.34.8', compatibility: 'not_applicable' };
    const compatible = { ...root134, id: 'release-134-compatible', version: '1.34.9', parentReleaseId: root134.id, compatibility: 'compatible' };
    const breaking = { ...root134, id: 'release-134-breaking', version: '1.35.0', parentReleaseId: compatible.id, compatibility: 'breaking' };
    const catalog = [{
      ...components[0],
      releases: [root117, breaking, compatible, root134],
      releaseLines: [
        { id: 'line-117', componentId: components[0].id, name: 'Kubernetes 1.17', latestReleasedId: root117.id, releases: [root117], createdAt: '2026-08-01T00:00:00Z' },
        { id: 'line-134', componentId: components[0].id, name: 'Kubernetes 1.34', latestReleasedId: breaking.id, releases: [breaking, compatible, root134], createdAt: '2026-08-01T00:00:00Z' },
      ],
    }];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json(catalog);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    expect(await screen.findByText('Kubernetes 1.17')).toBeInTheDocument();
    expect(screen.getByText('Kubernetes 1.34')).toBeInTheDocument();
    expect(screen.getAllByText('全新基线')).toHaveLength(2);
    expect(screen.getByText('兼容升级')).toBeInTheDocument();
    expect(screen.getByText('破坏性升级')).toBeInTheDocument();
    expect(screen.queryByText('不兼容变更')).not.toBeInTheDocument();
  });

  it('creates an empty Draft without cloning an existing release', async () => {
    const released = { ...components[0].releases[0], environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] } };
    const catalog = [{ ...components[0], latestRelease: released, releases: [released] }];
    const draft = { ...released, id: 'release-containerd-empty', version: 'v2.2.0', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [], artifacts: [], images: [] };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components/component-containerd/release-draft-plan') && init?.method === 'POST') return json({ mode: 'new_line', lineName: 'containerd 2.2', targetVersion: draft.version, compatibility: 'not_applicable', planDigest: 'draft-plan', actions: [], removedActions: [], playbooks: [], artifactCount: 0, imageCount: 0 });
      if (url.endsWith('/components/component-containerd/release-drafts') && init?.method === 'POST') return json(draft);
      if (url.endsWith('/components')) return json(catalog);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('confirm', vi.fn(() => true));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增分支' }));
    expect(screen.getByRole('dialog', { name: '新增分支 · containerd' })).toHaveTextContent('内容模板不建立跨分支升级关系');
    const editor = within(screen.getByRole('dialog'));
    expect(editor.getByLabelText('x86/amd64')).not.toBeChecked();
    expect(editor.getByLabelText('SUSE')).not.toBeChecked();
    await fillField(editor.getByLabelText('分支名称'), 'containerd 2.2');
    await fillField(editor.getByPlaceholderText('v1.1.0'), 'v2.2.0');
    await selectOption(editor.getByLabelText('风险级别'), /破坏性/);
    await fillField(editor.getByPlaceholderText('说明变化和下游注意事项'), '全新合同');
    expect(editor.getByLabelText(/确认以上适配范围/)).toBeDisabled();
    for (const option of editor.getAllByLabelText('不限制')) {
      expect(option).not.toBeChecked();
      await userEvent.click(option);
    }
    await userEvent.click(editor.getByLabelText(/确认以上适配范围/));
    await userEvent.click(editor.getByRole('button', { name: '创建分支' }));
    await answerConfirm();

    expect(await screen.findByRole('button', { name: '保存直接依赖' })).toBeInTheDocument();
    const createCall = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/components/component-containerd/release-drafts') && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({ mode: 'new_line', lineName: 'containerd 2.2', version: 'v2.2.0', releaseNotes: '全新合同', riskLevel: 'destructive', compatibility: 'not_applicable', environmentConstraints: {}, expectedPlanDigest: 'draft-plan' });
  });

  it('synchronizes template constraints and confirms before overwriting manual edits', async () => {
    const first = { ...components[0].releases[0], id: 'release-template-a', version: '1.0.0', environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] } };
    const second = { ...components[0].releases[0], id: 'release-template-b', version: '2.0.0', environmentConstraints: { architecture: ['arm64'], operatingSystem: ['Ubuntu'] } };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: second, releases: [second, first] }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));


    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增分支' }));
    const editor = within(screen.getByRole('dialog'));
    const template = editor.getByLabelText('内容模板（可选）');
    await selectOption(template, new RegExp(first.version));
    expect(editor.getByLabelText('x86/amd64')).toBeChecked();
    expect(editor.getByLabelText('SUSE')).toBeChecked();
    await userEvent.click(editor.getByLabelText('ARM/arm64'));
    await selectOption(template, new RegExp(second.version));
    await answerConfirm(false);
    expect(template.closest('.ant-select')).toHaveTextContent(first.version);
    expect(editor.getByLabelText('x86/amd64')).toBeChecked();
    await selectOption(template, new RegExp(second.version));
    await answerConfirm();
    expect(template.closest('.ant-select')).toHaveTextContent(second.version);
    expect(editor.getByLabelText('x86/amd64')).not.toBeChecked();
    expect(editor.getByLabelText('ARM/arm64')).toBeChecked();
    expect(editor.getByLabelText('Ubuntu')).toBeChecked();
  });

  it('shows the backend reason when a deprecated latest publication blocks evolution', async () => {
    const root = { ...components[0].releases[0], id: 'release-root', lineId: 'line-deprecated', version: '1.0.0' };
    const deprecated = { ...components[0].releases[0], id: 'release-deprecated', lineId: 'line-deprecated', version: '2.0.0', status: 'deprecated', releasedAt: '2026-08-31T00:00:00Z' };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        ...components[0], latestRelease: root, releases: [deprecated, root],
        releaseLines: [{ id: 'line-deprecated', componentId: components[0].id, name: '稳定线', latestReleasedId: root.id, evolutionEligible: false, evolutionBlockedReason: '最后一个曾发布版本 2.0.0 已废弃，请创建新发布线', releases: [deprecated, root], createdAt: '2026-08-01T00:00:00Z' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增分支' }));
    expect(screen.getByText(/最后一个曾发布版本 2.0.0 已废弃/)).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '基于现有发布线演进' })).not.toBeInTheDocument();
  });

  it('keeps readiness and lifecycle editing on the selected Draft when several Drafts exist', async () => {
    const base = components[0].releases[0];
    const first = { ...base, id: 'release-containerd-draft-a', version: 'v2.2.0-a', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [] };
    const second = { ...base, id: 'release-containerd-draft-b', version: 'v2.2.0-b', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [] };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: first, releases: [first, second, base] }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components?selected=component-containerd&release=release-containerd-draft-b');
    expect(await screen.findByLabelText('Draft v2.2.0-b 发布就绪度')).toBeInTheDocument();
    expect(screen.queryByLabelText('Draft v2.2.0-a 发布就绪度')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '编辑版本与 Playbook' }));
    expect(screen.getByRole('dialog', { name: '配置 Draft v2.2.0-b' })).toBeInTheDocument();
  });

  it('hides empty Draft creation from non component owners', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    expect(await screen.findByRole('heading', { name: 'containerd', level: 2 })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增分支' })).not.toBeInTheDocument();
  });

  it('shows the backend conflict when an empty Draft version already exists', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components/component-containerd/release-draft-plan') && init?.method === 'POST') return json({ mode: 'new_line', lineName: 'duplicate', targetVersion: 'v2.1.1', compatibility: 'not_applicable', planDigest: 'duplicate-plan', actions: [], removedActions: [], playbooks: [], artifactCount: 0, imageCount: 0 });
      if (url.endsWith('/components/component-containerd/release-drafts') && init?.method === 'POST') return json({ error: { code: 'CONFLICT', message: '版本 v2.1.1 已存在' } }, 409);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    vi.stubGlobal('confirm', vi.fn(() => true));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增分支' }));
    const editor = within(screen.getByRole('dialog'));
    await fillField(editor.getByLabelText('分支名称'), 'duplicate');
    await fillField(editor.getByPlaceholderText('v1.1.0'), 'v2.1.1');
    await fillField(editor.getByPlaceholderText('说明变化和下游注意事项'), '重复版本');
    for (const option of editor.getAllByLabelText('不限制')) await userEvent.click(option);
    await userEvent.click(editor.getByLabelText(/确认以上适配范围/));
    await userEvent.click(editor.getByRole('button', { name: '创建分支' }));
    await answerConfirm();
    expect(await screen.findByText('版本 v2.1.1 已存在')).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '新增分支 · containerd' })).toBeInTheDocument();
  });

  it('scrolls to the matching contract section when editing a draft', async () => {
    const scrolled: string[] = [];
    const originalScroll = HTMLElement.prototype.scrollIntoView;
    HTMLElement.prototype.scrollIntoView = function scrollIntoView() {
      scrolled.push(this.id);
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    try {
      renderApp('/components?selected=component-kubelet');
      await userEvent.click(await screen.findByLabelText('编辑参数合同'));
      expect(screen.getByRole('button', { name: '新增参数' })).toBeInTheDocument();
      expect(scrolled).toContain('contract-parameters');
      await userEvent.click(screen.getByRole('button', { name: '取消' }));
      scrolled.length = 0;
      await userEvent.click(await screen.findByLabelText('编辑参数合同'));
      expect(screen.getByRole('button', { name: '新增参数' })).toBeInTheDocument();
      expect(document.getElementById('contract-parameters')).toBeInTheDocument();
      expect(scrolled).toContain('contract-parameters');
      await userEvent.click(screen.getByRole('button', { name: '取消' }));
      scrolled.length = 0;
      await userEvent.click(screen.getByRole('button', { name: '编辑直接依赖' }));
      expect(screen.getByRole('button', { name: '新增依赖' })).toBeInTheDocument();
      expect(scrolled).toContain('contract-dependencies');
    } finally {
      HTMLElement.prototype.scrollIntoView = originalScroll;
    }
  });

  it('lets the owner choose public vs internal visibility on a draft release', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components?selected=component-kubelet');
    await userEvent.click(await screen.findByLabelText('编辑参数合同'));
    const editor = within(document.getElementById('release-contract-editor')!);
    expect(editor.queryByLabelText(/内部/)).not.toBeInTheDocument();
    await userEvent.click(editor.getByRole('button', { name: '编辑参数 kubeInstallRoot' }));
    const parameter = editor;
    expect(parameter.getByLabelText(/内部/)).toBeChecked();
    expect(parameter.getByLabelText(/公开/)).not.toBeChecked();
    await userEvent.click(parameter.getByLabelText(/公开/));
    expect(parameter.getByLabelText(/公开/)).toBeChecked();
  });

  it('renders all six component layers including an empty L5', async () => {
    renderApp('/components');
    expect((await screen.findAllByText('containerd')).length).toBeGreaterThan(0);
    for (const label of ['L1', 'L2', 'L3', 'L4', 'L5', 'L6']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByText('可观测与节点管理层')).toBeInTheDocument();
    expect(screen.queryByText('本层暂无组件')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /L5.*可观测与节点管理层/ }));
    expect(screen.getByText('本层暂无组件')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('tab', { name: '版本历史' }));
    expect(screen.getByText(/0 项依赖 · 0 个参数映射/)).toBeInTheDocument();
    expect(screen.getByText('0 个公开参数')).toBeInTheDocument();
  });

  it('collapses unused catalog layers and can expand or fold them', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: /containerd/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /L2.*容器运行时与状态存储层/ })).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: /L1.*主机基础与安全准备层/ })).toHaveAttribute('aria-expanded', 'false');
    await userEvent.click(screen.getByRole('button', { name: '全部折叠' }));
    expect(screen.queryByRole('button', { name: /containerd/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /L2.*容器运行时与状态存储层/ }));
    expect(screen.getByRole('button', { name: /containerd/ })).toBeInTheDocument();
  });

  it('searches and filters the component owner catalog without changing component data', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: /containerd/ })).toBeInTheDocument();
    await fillField(screen.getByLabelText('搜索组件'), 'missing-component');
    expect(screen.getByText('没有匹配的组件')).toBeInTheDocument();
    expect(screen.getByText(/当前详情不在目录筛选结果中/)).toBeInTheDocument();
    await userEvent.clear(screen.getByLabelText('搜索组件'));
    await userEvent.click(screen.getByRole('button', { name: '有 Draft' }));
    expect(screen.getByText('没有匹配的组件')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '全部' }));
    expect(screen.getByRole('button', { name: /containerd/ })).toBeInTheDocument();
  });

  it('lets the owner confirm or cancel Draft deprecation', async () => {
    const draft = {
      id: 'release-draft-deprecate', componentId: 'component-draft-deprecate', version: '2.0.0-rc1', status: 'draft',
      releaseNotes: 'candidate draft', parameters: [], dependencies: [], actions: [],
    };
    let deprecated = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-draft-deprecate', name: 'Draft Component', slug: 'draft-component', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'], latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/component-releases/release-draft-deprecate/impact?operation=deprecate')) return json({ changeKind: 'deprecation', componentOwners: [], scenarioOwners: [], scenarios: [], paths: [] });
      if (url.endsWith('/component-releases/release-draft-deprecate/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...draft, status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components');
    await moreAction('废弃草稿');
    expect(screen.getByRole('dialog', { name: '废弃草稿 2.0.0-rc1' })).toHaveTextContent('废弃后仍可恢复为 Draft');
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(deprecated).toBe(false);

    await moreAction('废弃草稿');
    const confirm = screen.getByRole('button', { name: '确认废弃草稿' });
    await waitFor(() => expect(confirm).toBeEnabled());
    await userEvent.click(confirm);
    await waitFor(() => expect(deprecated).toBe(true));
  });

  it('restores or permanently deletes a never-published deprecated Release', async () => {
    const release = {
      id: 'release-disposable', componentId: 'component-disposable', lineId: 'line-disposable', lineName: 'Disposable',
      version: 'test', status: 'deprecated', compatibility: 'not_applicable', readiness: { status: 'blocked', blockers: [] },
      releaseNotes: 'discardable', parameters: [], dependencies: [], actions: [], artifacts: [], images: [], createdAt: '2026-09-01T07:39:00Z',
    };
    let state: 'draft' | 'deprecated' = 'deprecated';
    let deleted = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-disposable', name: 'Disposable Component', slug: 'disposable-component', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'], releases: deleted ? [] : [{ ...release, status: state }],
      }]);
      if (url.endsWith('/component-releases/release-disposable/restore') && init?.method === 'POST') {
        state = 'draft';
        return json({ ...release, status: state });
      }
      if (url.endsWith('/component-releases/release-disposable/impact?operation=deprecate')) return json({ changeKind: 'deprecation', componentOwners: [], scenarioOwners: [], scenarios: [], paths: [] });
      if (url.endsWith('/component-releases/release-disposable/deprecate') && init?.method === 'POST') {
        state = 'deprecated';
        return json({ ...release, status: state, deprecatedAt: '2026-09-01T08:00:00Z' });
      }
      if (url.endsWith('/component-releases/release-disposable') && init?.method === 'DELETE') {
        deleted = true;
        return json({ deleted: true });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-disposable&release=release-disposable');
    await moreAction('恢复 Draft');
    expect(await screen.findByText(/确认恢复 test/)).toBeInTheDocument();
    await answerConfirm();
    await waitFor(() => expect(state).toBe('draft'));
    await moreAction('废弃草稿');
    const deprecate = screen.getByRole('button', { name: '确认废弃草稿' });
    await waitFor(() => expect(deprecate).toBeEnabled());
    await userEvent.click(deprecate);
    await moreAction('永久删除');
    expect(await screen.findByText(/此操作不可恢复/)).toBeInTheDocument();
    await answerConfirm();
    await waitFor(() => expect(deleted).toBe(true));
    expect(await screen.findByText('组件版本已永久删除')).toBeInTheDocument();
  });

  it('lets component owners read rejected review comments from the release row and details', async () => {
    const release = {
      id: 'release-review-result', componentId: 'component-review-result', version: '1.0.0', status: 'draft',
      review: { status: 'rejected', reviewedBy: admin.id, submittedAt: '2026-01-01T08:00:00Z', reviewedAt: '2026-01-01T09:00:00Z', comment: '第一项：核对参数合同。\n第二项：补齐验证说明。' },
    };
    const fallback = installFetch().getMockImplementation()!;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => String(input).endsWith('/components') ? json([{
      id: release.componentId, name: 'Review Result Component', ownerId: alice.id, layer: 'runtime_state', tags: [], releases: [release],
    }]) : fallback(input, init));
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '审批意见' }));
    const dialog = screen.getByRole('dialog', { name: '1.0.0 审批意见' });
    expect(dialog).toHaveTextContent('已驳回');
    expect(dialog).toHaveTextContent(admin.name);
    expect(dialog).toHaveTextContent('审批时间');
    expect(dialog).toHaveTextContent('第一项：核对参数合同。');
    expect(dialog).toHaveTextContent('第二项：补齐验证说明。');
    expect(within(dialog).queryByRole('button', { name: /批准|驳回/ })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByText('关闭'));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    await userEvent.click(screen.getByLabelText('平台合同审核 · 待完成'));
    await userEvent.click(screen.getByRole('button', { name: '查看审批意见' }));
    expect(screen.getByRole('dialog', { name: '1.0.0 审批意见' })).toHaveTextContent('第二项：补齐验证说明。');
    const reopened = screen.getByRole('dialog');
    await userEvent.click(within(reopened).getByText('关闭'));
    await waitFor(() => expect(reopened).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    expect(screen.getByRole('dialog', { name: '1.0.0 Release 详情' })).toHaveTextContent('第二项：补齐验证说明。');
    expect(fetchMock.mock.calls.some(([input]) => /review-preview|review-decision/.test(String(input)))).toBe(false);
  });

  it.each([
    ['approved', 'draft'],
    ['approved', 'released'],
    ['pending', 'draft'],
    ['not_submitted', 'draft'],
  ])('hides review comments and entry points for %s reviews on %s releases', async (status, releaseStatus) => {
    const fallback = installFetch().getMockImplementation()!;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => String(input).endsWith('/components') ? json([{
      id: 'component-review-result', name: 'Review Result Component', ownerId: alice.id, layer: 'runtime_state', tags: [],
      releases: [{ id: 'release-review-result', componentId: 'component-review-result', version: '1.0.0', status: releaseStatus, review: { status, comment: '不应展示的审批备注' } }],
    }]) : fallback(input, init)));
    renderApp('/components');
    const details = await screen.findByRole('button', { name: '查看详情' });
    expect(screen.queryByRole('button', { name: '审批意见' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查看审批意见' })).not.toBeInTheDocument();
    expect(screen.queryByText('不应展示的审批备注')).not.toBeInTheDocument();
    await userEvent.click(details);
    const dialog = screen.getByRole('dialog', { name: '1.0.0 Release 详情' });
    expect(within(dialog).queryByRole('heading', { name: '平台合同审批意见' })).not.toBeInTheDocument();
    expect(dialog).not.toHaveTextContent('不应展示的审批备注');
  });

  it('shows each release environment constraints such as architecture', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-containerd', name: 'containerd', slug: 'containerd', ownerId: alice.id, layer: 'runtime_state', tags: ['runtime'],
        latestRelease: {
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE', 'Kylin'], ipFamily: ['IPv4'] },
        },
        releases: [{
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE', 'Kylin'], ipFamily: ['IPv4'] },
        }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components');
    expect((await screen.findAllByText('适配标签')).length).toBeGreaterThan(0);
    expect(screen.getAllByText('架构').length).toBeGreaterThan(0);
    expect(screen.getAllByText('x86/amd64').length).toBeGreaterThan(0);
    expect(screen.getAllByText('ARM/arm64').length).toBeGreaterThan(0);
    expect(screen.getAllByText('操作系统').length).toBeGreaterThan(0);
    expect(screen.getAllByText('SUSE').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Kylin').length).toBeGreaterThan(0);
    expect(screen.getAllByText('IP 协议族').length).toBeGreaterThan(0);
    expect(screen.getAllByText('IPv4').length).toBeGreaterThan(0);
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    expect(screen.getAllByText('架构').length).toBeGreaterThan(1);
    expect(screen.getAllByText('x86/amd64').length).toBeGreaterThan(1);
  });

  it('keeps inherited scope read-only when creating a version', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-containerd', name: 'containerd', slug: 'containerd', ownerId: alice.id, layer: 'runtime_state', tags: ['runtime'],
        latestRelease: {
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released',
          environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] },
        },
        releases: [{
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released',
          environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] },
        }],
      }]);
      if (url.endsWith('/components/component-containerd/release-draft-plan')) {
        return json({ mode: 'evolution', lineId: 'line-component-containerd', lineName: 'containerd baseline', parentReleaseId: 'release-containerd-2', parentVersion: 'v2.1.1', targetVersion: 'v2.2.0', compatibility: 'compatible', planDigest: 'draft-plan', actions: [], removedActions: [], playbooks: [], artifactCount: 0, imageCount: 0 });
      }
      if (url.endsWith('/components/component-containerd/release-drafts')) {
        return json({
          id: 'release-containerd-3', componentId: 'component-containerd', parentReleaseId: 'release-containerd-2', version: 'v2.2.0', status: 'draft',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE'], ipFamily: ['IPv4'] },
        });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('confirm', vi.fn(() => true));
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新增版本' }));
    expect(screen.getByRole('dialog', { name: '新增版本 · containerd' })).toBeInTheDocument();
    expect(within(screen.getByRole('dialog')).queryByRole('checkbox')).not.toBeInTheDocument();
    const editor = within(screen.getByRole('dialog'));
    await fillField(editor.getByPlaceholderText('v1.1.0'), 'v2.2.0');
    await fillField(editor.getByPlaceholderText('说明变化和下游注意事项'), '增加 ARM 适配');
    await userEvent.click(editor.getByRole('button', { name: '创建版本' }));
    await answerConfirm();
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/components/component-containerd/release-drafts') && init?.method === 'POST');
      expect(JSON.parse(String(call?.[1]?.body))).not.toHaveProperty('environmentConstraints');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        mode: 'evolution',
        parentReleaseId: 'release-containerd-2',
        compatibility: 'compatible',
        version: 'v2.2.0',
        releaseNotes: '增加 ARM 适配',

      });
    });
  });

  it('submits the simplified component metadata contract', async () => {
    const fetchMock = installFetch();
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建组件' }));
    await fillField(screen.getByPlaceholderText('例如 containerd'), 'storage driver');
    await fillField(screen.getByPlaceholderText('containerd'), 'storage-driver');
    await selectOption(screen.getByLabelText('组件层级'), /L4/);
    await fillField(screen.getByLabelText('标签'), 'storage, optional');
    await userEvent.click(screen.getByRole('button', { name: '创建组件' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/components') && init?.method === 'POST');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        layer: 'cluster_service', tags: ['storage', 'optional'],
      });
    });
  });

  it('surfaces a backend 403 instead of silently accepting a forbidden write', async () => {
    installFetch({ componentCreateForbidden: true });
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建组件' }));
    await fillField(screen.getByPlaceholderText('例如 containerd'), 'sample');
    await fillField(screen.getByPlaceholderText('containerd'), 'sample');
    await userEvent.click(screen.getByRole('button', { name: '创建组件' }));
    expect(await screen.findByText(/权限不足：只有资源 Owner 可以修改组件/)).toBeInTheDocument();
  });
});
