import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { AppProvider } from '../context/AppContext';
import { parseRunInput, uniqueRunInputs } from '../components/RunInputFields';
import { EventSourceMock } from './setup';

const alice = { id: 'component-alice', name: 'Alice Component', role: 'component_owner' };
const dave = { id: 'environment-dave', name: 'Dave Environment', role: 'environment_owner' };
const carol = { id: 'scenario-carol', name: 'Carol Scenario', role: 'scenario_owner' };

const components = [{
  id: 'component-containerd',
  name: 'containerd',
  slug: 'containerd',
  ownerId: alice.id,
  description: 'CRI runtime',
  layer: 'runtime_state',
  category: 'runtime',
  kind: 'software',
  requiredness: 'profile_required',
  latestRelease: { id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released', verified: true, actions: [{ type: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { type: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { type: 'rollback', playbook: 'rollback.yml' }] },
  releases: [{ id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released', verified: true, actions: [{ type: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { type: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { type: 'rollback', playbook: 'rollback.yml' }] }],
}];

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(status >= 400 ? data : Array.isArray(data) ? { items: data } : { data }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }));
}

function installFetch(options: { componentCreateForbidden?: boolean; initialUser?: typeof alice | typeof dave | typeof carol; withScenario?: boolean } = {}) {
  let current = options.initialUser ?? alice;
  const mock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/session/me')) return json(current);
    if (url.endsWith('/session/switch')) {
      const id = JSON.parse(String(init?.body)).userId;
      current = id === dave.id ? dave : id === carol.id ? carol : alice;
      return json(current);
    }
    if (url.endsWith('/components') && init?.method === 'POST') {
      if (options.componentCreateForbidden) return json({ error: { code: 'FORBIDDEN', message: '只有资源 Owner 可以修改组件' } }, 403);
      return json(components[0]);
    }
    if (url.endsWith('/components')) return json(components);
    if (url.endsWith('/scenarios')) return json(options.withScenario ? [{
      id: 'scenario-openfuyao',
      name: 'OpenFuyao Management Cluster Build',
      ownerId: carol.id,
      slug: 'openfuyao-management-cluster-build',
      currentRevisionId: 'scenario-openfuyao-r1',
      currentRevision: {
        id: 'scenario-openfuyao-r1',
        scenarioId: 'scenario-openfuyao',
        revision: 1,
        state: 'draft',
        status: 'draft',
        nodes: [{ id: 'bke-cert', type: 'component', position: { x: 80, y: 80 }, data: { label: 'bke-cert', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', hostGroup: 'bootstrap_host', runInputs: ['rollback_version'] } }],
        edges: [],
        executionPolicy: {},
      },
      revisions: [{
        id: 'scenario-openfuyao-r1',
        scenarioId: 'scenario-openfuyao',
        revision: 1,
        state: 'draft',
        status: 'draft',
        nodes: [{ id: 'bke-cert', type: 'component', position: { x: 80, y: 80 }, data: { label: 'bke-cert', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', hostGroup: 'bootstrap_host', runInputs: ['rollback_version'] } }],
        edges: [],
        executionPolicy: {},
      }],
    }] : []);
    if (url.endsWith('/environments')) return json([{ id: 'environment-test', name: 'Test Environment', ownerId: dave.id, currentRevision: { id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], parameters: {}, credentialRefs: [] } }]);
    if (url.endsWith('/runs')) return json([]);
    if (url.endsWith('/notifications')) return json([]);
    return json({});
  });
  vi.stubGlobal('fetch', mock);
  return mock;
}

function renderApp(path = '/') {
  return render(<MemoryRouter initialEntries={[path]}><AppProvider><App /></AppProvider></MemoryRouter>);
}

describe('platform shell and RBAC UI', () => {
  beforeEach(() => installFetch());
  afterEach(() => vi.unstubAllGlobals());

  it('waits for Demo identity initialization before requesting protected resources', async () => {
    let resolveSwitch!: (response: Response) => void;
    const switchResponse = new Promise<Response>((resolve) => { resolveSwitch = resolve; });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json({ error: { code: 'UNAUTHORIZED', message: '未登录' } }, 401);
      if (url.endsWith('/session/switch')) return switchResponse;
      if (url.endsWith('/components')) return json(components);
      return json([]);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<StrictMode><MemoryRouter initialEntries={['/components']}><AppProvider><App /></AppProvider></MemoryRouter></StrictMode>);
    expect(screen.getByRole('status')).toHaveTextContent('正在初始化 Demo 身份…');
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/session/switch'))).toBe(true));
    expect(fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/session/me'))).toHaveLength(1);
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/components'))).toBe(false);

    await act(async () => resolveSwitch(await json(alice)));
    expect(await screen.findByRole('heading', { name: 'containerd' })).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/components')).length).toBeGreaterThan(0);
  });

  it('shows the dashboard summary and all primary navigation entries', async () => {
    renderApp();
    expect(await screen.findByRole('heading', { name: /早上好/ })).toBeInTheDocument();
    for (const label of ['概览', '组件', '场景', '环境', '运行', '通知', '操作说明书']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument();
    }
    expect(screen.getByText(/无密码身份模式/)).toBeInTheDocument();
  });

  it('changes visible owner actions after a server-backed identity switch', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: '新建组件' })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), dave.id);
    await waitFor(() => expect(screen.queryByRole('button', { name: '新建组件' })).not.toBeInTheDocument());
    expect(screen.getByText('环境 Owner')).toBeInTheDocument();
  });

  it('shows the platform workflow overview from the operation manual entry', async () => {
    renderApp('/manual');
    expect(await screen.findByRole('heading', { name: '操作说明书' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '平台工作流程总览' })).toBeInTheDocument();
    for (const label of ['1. 组件 Release', '2. 场景 Revision', '3. 环境 Revision', '4. Run']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByRole('heading', { name: '场景节点数不等于环境主机数' })).toBeInTheDocument();
    expect(screen.getByText(/实际数量以当前已发布 Revision 为准/)).toBeInTheDocument();
    const commonButtons = screen.getByRole('region', { name: '全员公共按钮操作目录' });
    expect(commonButtons).toHaveTextContent('切换演示身份');
    expect(commonButtons).toHaveTextContent('刷新使用新版本');
    expect(commonButtons).toHaveTextContent('全部已读 / 标为已读');
    expect(commonButtons).toHaveTextContent('重新加载');
    expect(screen.getByRole('link', { name: /查看我的操作手册/ })).toHaveAttribute('href', '/manual/role');
  });

  it('updates the role manual after switching the current owner identity', async () => {
    renderApp('/manual/role');
    expect(await screen.findByRole('heading', { name: '组件 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '组件 Owner 操作路径' })).toBeInTheDocument();
    let buttonDirectory = screen.getByRole('region', { name: '组件 Owner 按钮操作目录' });
    for (const label of ['新建组件', '保存依赖和参数', '保存 Playbook', '预览执行计划 / 刷新执行计划', '上传并构建']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }

    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), carol.id);

    expect(await screen.findByRole('heading', { name: '场景 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '场景 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '组件 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '场景 Owner 按钮操作目录' });
    for (const label of ['新建场景 / 创建场景', '保存草稿', '放大 / 缩小 / 适配视图', '环境测试 / 开始完整测试']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }

    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), dave.id);

    expect(await screen.findByRole('heading', { name: '环境 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '环境 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '场景 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '环境 Owner 按钮操作目录' });
    for (const label of ['新建环境 / 创建环境', '添加主机 / 删除主机', '保存新 Revision', '拒绝', '批准执行']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }
  });

  it('documents deployment handoff and version refresh steps by role', async () => {
    renderApp('/manual/deployment');
    expect(await screen.findAllByRole('heading', { name: '部署与版本切换交接' })).toHaveLength(2);
    for (const heading of ['平台部署人员', '组件 Owner', '场景 Owner', '环境 Owner', '交接证据']) {
      expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    }
    expect(screen.getByText(/旧应用区域已经 inert/)).toBeInTheDocument();
    expect(screen.getByText(/旧 planDigest 不再使用/)).toBeInTheDocument();
    expect(screen.getByRole('region', { name: '部署交接检查清单' })).toHaveTextContent('业务记录计数未被只读验收改变');
  });

  it('shows which upstream component parameter a release depends on', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', category: 'worker', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }, {
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', category: 'network', kind: 'software', requiredness: 'profile_required',
        latestRelease: {
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', state: 'released', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ componentId: 'component-kubelet', componentName: 'kubelet', releaseId: 'release-kubelet', version: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        },
        releases: [{
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', state: 'released', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ componentId: 'component-kubelet', componentName: 'kubelet', releaseId: 'release-kubelet', version: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    renderApp('/components?selected=component-kube-proxy');
    expect((await screen.findAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot')).length).toBeGreaterThan(0);
    expect(screen.getByText('内部')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看合同' }));
    expect(screen.getByRole('dialog', { name: '1.17.5 参数合同' })).toBeInTheDocument();
    expect(screen.getAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot').length).toBeGreaterThan(1);
  });

  it('closes a component release dialog when the selected route changes', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-controller-manager', name: 'kube-controller-manager', slug: 'kube-controller-manager', ownerId: alice.id,
        layer: 'orchestration_core', category: 'control_plane', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-controller-manager', componentId: 'component-controller-manager', version: 'controller-1.17.5', state: 'released', status: 'released' },
        releases: [{ id: 'release-controller-manager', componentId: 'component-controller-manager', version: 'controller-1.17.5', state: 'released', status: 'released' }],
      }, {
        id: 'component-scheduler', name: 'kube-scheduler', slug: 'kube-scheduler', ownerId: alice.id,
        layer: 'orchestration_core', category: 'control_plane', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-scheduler', componentId: 'component-scheduler', version: 'scheduler-1.17.5', state: 'released', status: 'released' },
        releases: [{ id: 'release-scheduler', componentId: 'component-scheduler', version: 'scheduler-1.17.5', state: 'released', status: 'released' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components?selected=component-controller-manager');
    await userEvent.click(await screen.findByRole('button', { name: '查看合同' }));
    expect(screen.getByRole('dialog', { name: 'controller-1.17.5 参数合同' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /kube-scheduler/ }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'kube-scheduler' })).toBeInTheDocument();
  });

  it('keeps component navigation locked until a Draft save finishes and its dialog unmounts', async () => {
    const schedulerDraft = {
      id: 'release-scheduler-draft', componentId: 'component-scheduler', version: '1.17.5-r2', type: 'atomic',
      state: 'draft', status: 'draft', releaseNotes: 'Scheduler draft', parameters: [], dependencies: [],
      actions: [{ name: 'rollback', type: 'rollback', playbook: 'managed/scheduler/kube-scheduler-rollback.yml', fromReleaseId: '1.17.5-r2', toReleaseId: '1.17.5' }],
    };
    const proxyDraft = {
      id: 'release-proxy-draft', componentId: 'component-proxy', version: '1.17.5-r2', type: 'atomic',
      state: 'draft', status: 'draft', releaseNotes: 'Proxy draft', parameters: [], dependencies: [], actions: [],
    };
    let resolveSave!: (response: Response) => void;
    const saveResponse = new Promise<Response>((resolve) => { resolveSave = resolve; });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-scheduler-draft') && init?.method === 'PUT') return saveResponse;
      if (url.endsWith('/components')) return json([{
        id: 'component-scheduler', name: 'kube-scheduler', slug: 'kube-scheduler', ownerId: alice.id,
        layer: 'orchestration_core', category: 'control_plane', kind: 'software', requiredness: 'core_required',
        latestRelease: schedulerDraft, releases: [schedulerDraft],
      }, {
        id: 'component-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id,
        layer: 'orchestration_core', category: 'network', kind: 'software', requiredness: 'profile_required',
        latestRelease: proxyDraft, releases: [proxyDraft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-scheduler');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByRole('dialog', { name: '配置 Draft 1.17.5-r2' })).toBeInTheDocument();
    expect(screen.getByDisplayValue('kube-scheduler-rollback.yml')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '保存 Draft' }));
    expect(await screen.findByRole('button', { name: '保存中…' })).toBeDisabled();

    const proxyButton = screen.getByRole('button', { name: /kube-proxy/ });
    expect(proxyButton).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.getByRole('dialog', { name: '配置 Draft 1.17.5-r2' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'kube-scheduler' })).toBeInTheDocument();

    await act(async () => resolveSave(await json(schedulerDraft)));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

    expect(proxyButton).toBeEnabled();
    await userEvent.click(proxyButton);
    expect(await screen.findByRole('heading', { name: 'kube-proxy' })).toBeInTheDocument();
    expect(screen.queryByDisplayValue('kube-scheduler-rollback.yml')).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input, init]) => String(input).includes('/component-releases/') && init?.method === 'PUT')).toHaveLength(1);
  });

  it('switches the visible contract when a component has multiple releases', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', category: 'network', kind: 'software', requiredness: 'profile_required',
        latestRelease: { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', state: 'released', status: 'released', parameters: [], dependencies: [] },
        releases: [
          { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', state: 'released', status: 'released', parameters: [], dependencies: [] },
          {
            id: 'release-kube-proxy-old', componentId: 'component-kube-proxy', version: '1.17.5', state: 'released', status: 'released',
            parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
            dependencies: [{ componentId: 'component-kubelet', componentName: 'kubelet', releaseId: 'release-kubelet', version: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
          },
        ],
      }, {
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', category: 'worker', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    renderApp('/components?selected=component-kube-proxy');
    expect(await screen.findByText(/当前展示 1.17.5，因为它有参数映射/)).toBeInTheDocument();
    expect(screen.getAllByText(/本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot/).length).toBeGreaterThan(0);
    expect(screen.getByText(/1.17.5：本组件参数 kubeRoot/)).toBeInTheDocument();
    expect(screen.getByText(/1.17.5 的公开参数可被下游引用/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查看 1.17.5 的依赖和参数合同' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: '查看 1.34.3 的依赖和参数合同' }));
    expect(screen.getByRole('button', { name: '查看 1.34.3 的依赖和参数合同' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('没有直接依赖')).toBeInTheDocument();
    expect(screen.getByText(/1.34.3 锁定的上游/)).toBeInTheDocument();
    expect(screen.getByText(/1.34.3 的公开参数可被下游引用/)).toBeInTheDocument();
    expect(screen.queryByText(/当前展示 1.17.5，因为它有参数映射/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看 1.17.5 的依赖和参数合同' }));
    expect(screen.getAllByText(/本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot/).length).toBeGreaterThan(0);
  });

  it('lets the owner edit dependencies and parameters on the component page', async () => {
    const draft = {
      id: 'release-kube-proxy-draft', componentId: 'component-kube-proxy', version: '1.17.6', state: 'draft', status: 'draft',
      parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
      dependencies: [] as Array<Record<string, unknown>>,
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-kube-proxy-draft/contract') && init?.method === 'PUT') {
        return json({ ...draft, ...JSON.parse(String(init.body)), state: 'draft', status: 'draft' });
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', category: 'worker', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', state: 'released', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }, {
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', category: 'network', kind: 'software', requiredness: 'profile_required',
        latestRelease: draft,
        releases: [draft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/components?selected=component-kube-proxy');
    await userEvent.click(await screen.findByRole('button', { name: '编辑直接依赖' }));
    expect(screen.getByRole('button', { name: '新增依赖' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增参数' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /内部/ })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: /公开/ }));
    await userEvent.click(screen.getByRole('button', { name: '新增依赖' }));
    await userEvent.selectOptions(screen.getByLabelText('上游组件'), 'component-kubelet');
    await userEvent.selectOptions(screen.getByLabelText('已发布版本'), 'release-kubelet');
    await userEvent.click(screen.getByRole('button', { name: '增加映射' }));
    await userEvent.selectOptions(screen.getByLabelText('上游公开参数'), 'kubeInstallRoot');
    await userEvent.selectOptions(screen.getByLabelText('本 Release 目标参数'), 'kubeRoot');
    await userEvent.click(screen.getByRole('button', { name: '保存依赖和参数' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/component-releases/release-kube-proxy-draft/contract') && init?.method === 'PUT');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        parameters: [{ name: 'kubeRoot', visibility: 'public' }],
        dependencies: [{
          componentId: 'component-kubelet',
          releaseId: 'release-kubelet',
          parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }],
        }],
      });
    });
  });

  it.each([
    { button: '创建 Draft 编辑直接依赖', dialog: '创建 Draft 编辑直接依赖', target: 'contract-dependencies' },
    { button: '创建 Draft 编辑参数合同', dialog: '创建 Draft 编辑参数合同', target: 'contract-parameters' },
  ])('creates a draft and continues to $target', async ({ button, dialog, target }) => {
    const released = components[0].releases[0];
    const draft = { ...released, id: 'release-containerd-draft', version: 'v2.1.2', state: 'draft', status: 'draft', parameters: [], dependencies: [] };
    let created = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: created ? draft : released, releases: created ? [draft, released] : [released] }]);
      if (url.includes('/component-releases/release-containerd-2/clone')) { created = true; return json(draft); }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: button }));
    expect(screen.getByRole('dialog', { name: dialog })).toBeInTheDocument();
    expect(screen.getByText(/已发布且不可直接修改/)).toBeInTheDocument();
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.1.2');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '调整合同');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));

    expect(await screen.findByRole('button', { name: '保存依赖和参数' })).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toHaveAttribute('id', target));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('scrolls to the matching contract section when editing a draft', async () => {
    const scrolled: string[] = [];
    const originalScroll = HTMLElement.prototype.scrollIntoView;
    HTMLElement.prototype.scrollIntoView = function scrollIntoView() {
      scrolled.push(this.id);
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', category: 'worker', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', state: 'draft', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', state: 'draft', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    try {
      renderApp('/components?selected=component-kubelet');
      await userEvent.click(await screen.findByRole('button', { name: '编辑参数合同' }));
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
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', category: 'worker', kind: 'software', requiredness: 'core_required',
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', state: 'draft', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', state: 'draft', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    renderApp('/components?selected=component-kubelet');
    await userEvent.click(await screen.findByRole('button', { name: '配置合同' }));
    expect(screen.getByRole('radio', { name: /内部/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /公开/ })).not.toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: /公开/ }));
    expect(screen.getByRole('radio', { name: /公开/ })).toBeChecked();
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
		expect(screen.getByText('0 个参数映射')).toBeInTheDocument();
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

  it('shows each release environment constraints such as architecture', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-containerd', name: 'containerd', slug: 'containerd', ownerId: alice.id, layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required',
        latestRelease: {
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE', 'Kylin'], ipFamily: ['IPv4'] },
        },
        releases: [{
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE', 'Kylin'], ipFamily: ['IPv4'] },
        }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    renderApp('/components');
    expect(await screen.findByText('适配环境')).toBeInTheDocument();
    expect(screen.getByText('架构')).toBeInTheDocument();
    expect(screen.getByText('x86/amd64')).toBeInTheDocument();
    expect(screen.getByText('ARM/arm64')).toBeInTheDocument();
    expect(screen.getByText('操作系统')).toBeInTheDocument();
    expect(screen.getByText('SUSE')).toBeInTheDocument();
    expect(screen.getByText('Kylin')).toBeInTheDocument();
    expect(screen.getByText('IP 协议族')).toBeInTheDocument();
    expect(screen.getByText('IPv4')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看合同' }));
    expect(screen.getAllByText('架构').length).toBeGreaterThan(1);
    expect(screen.getAllByText('x86/amd64').length).toBeGreaterThan(1);
  });

  it('lets the owner choose environment constraints when creating a version', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-containerd', name: 'containerd', slug: 'containerd', ownerId: alice.id, layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required',
        latestRelease: {
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released',
          environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] },
        },
        releases: [{
          id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', status: 'released',
          environmentConstraints: { architecture: ['amd64'], operatingSystem: ['SUSE'] },
        }],
      }]);
      if (url.includes('/component-releases/release-containerd-2/clone')) {
        return json({
          id: 'release-containerd-3', componentId: 'component-containerd', version: 'v2.2.0', state: 'draft', status: 'draft',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE'], ipFamily: ['IPv4'] },
        });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '创建 Draft 编辑合同' }));
    expect(screen.getByRole('dialog', { name: '创建 Draft 编辑依赖和参数' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'x86/amd64' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'ARM/arm64' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'SUSE' })).toBeChecked();
    await userEvent.click(screen.getByRole('checkbox', { name: 'ARM/arm64' }));
    await userEvent.click(screen.getByRole('checkbox', { name: 'IPv4' }));
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.2.0');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '增加 ARM 适配');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).includes('/clone') && init?.method === 'POST');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        version: 'v2.2.0',
        releaseNotes: '增加 ARM 适配',
        environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE'], ipFamily: ['IPv4'] },
      });
    });
  });

  it('submits the complete component classification contract', async () => {
    const fetchMock = installFetch();
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建组件' }));
    await userEvent.type(screen.getByPlaceholderText('例如 containerd'), 'storage driver');
    await userEvent.type(screen.getByPlaceholderText('containerd'), 'storage-driver');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '组件层级' }), 'cluster_service');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '能力类别' }), 'storage');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '组件形态' }), 'software');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '必选性' }), 'optional');
    await userEvent.click(screen.getByRole('button', { name: '创建组件' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/components') && init?.method === 'POST');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        layer: 'cluster_service', category: 'storage', kind: 'software', requiredness: 'optional',
      });
    });
  });

  it('surfaces a backend 403 instead of silently accepting a forbidden write', async () => {
    installFetch({ componentCreateForbidden: true });
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建组件' }));
    await userEvent.type(screen.getByPlaceholderText('例如 containerd'), 'demo');
    await userEvent.type(screen.getByPlaceholderText('containerd'), 'demo');
    await userEvent.click(screen.getByRole('button', { name: '创建组件' }));
    expect(await screen.findByText(/权限不足：只有资源 Owner 可以修改组件/)).toBeInTheDocument();
  });

  it('previews rollback versions, invalidates stale plans, and submits rollback-only with the digest', async () => {
    const draft = {
      id: 'release-runtime-draft', componentId: 'component-runtime', version: '2.0.0-rc1', type: 'atomic',
      state: 'draft', status: 'draft', releaseNotes: 'Rollback candidate', parameters: [], dependencies: [],
      actions: [
        { name: 'install', type: 'install', playbook: 'managed/runtime/install.yml' },
        { name: 'verify', type: 'verify', playbook: 'managed/runtime/verify.yml' },
        { name: 'rollback', type: 'rollback', playbook: 'managed/runtime/rollback.yml', fromReleaseId: 'release-runtime-draft', toReleaseId: 'release-runtime-stable' },
      ],
    };
    const previews: Array<Record<string, unknown>> = [];
    let submitted: Record<string, unknown> | undefined;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-runtime-draft/test-plan') && init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        previews.push(body);
        const rollbackVerification = body.rollbackVerification as { kind: string };
        const rollbackStep = {
          order: 1, componentId: 'component-runtime', componentName: 'Runtime', releaseId: 'release-runtime-draft', releaseVersion: '2.0.0-rc1',
          action: 'rollback', playbook: 'managed/runtime/rollback.yml', limit: 'runtime_nodes', needsApproval: true,
          fromReleaseId: 'release-runtime-draft', fromReleaseVersion: '2.0.0-rc1', toReleaseId: 'release-runtime-stable', toReleaseVersion: '1.9.0',
        };
        return json({
          environmentId: 'environment-test', environmentRevisionId: 'environment-test-r1', destructive: true, requiresApproval: true,
          planDigest: rollbackVerification.kind === 'rollback_only' ? 'digest-rollback-only' : 'digest-target',
          steps: rollbackVerification.kind === 'rollback_only' ? [rollbackStep] : [rollbackStep, {
            order: 2, componentId: 'component-runtime', componentName: 'Runtime', releaseId: 'release-runtime-stable', releaseVersion: '1.9.0',
            action: 'verify', playbook: 'managed/runtime/verify.yml', limit: 'runtime_nodes', needsApproval: false,
          }],
        });
      }
      if (url.endsWith('/component-releases/release-runtime-draft/test-runs') && init?.method === 'POST') {
        submitted = JSON.parse(String(init.body));
        return json({ id: 'run-rollback-draft', kind: 'component_test', status: 'awaiting_approval', environmentId: 'environment-test', destructive: true });
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-runtime', name: 'Runtime', slug: 'runtime', ownerId: alice.id,
        layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required',
        latestRelease: draft, releases: [draft, {
          id: 'release-runtime-stable', componentId: 'component-runtime', version: '1.9.0', type: 'atomic',
          state: 'released', status: 'released', releaseNotes: 'Stable', parameters: [], dependencies: [],
          actions: [{ name: 'verify', type: 'verify', playbook: 'managed/runtime/verify.yml', hostGroup: 'runtime_nodes' }],
        }],
      }]);
      if (url.endsWith('/environments')) return json([{ id: 'environment-test', name: 'Six node test', ownerId: dave.id, status: 'ready' }]);
      if (url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-runtime');
    await userEvent.click((await screen.findAllByRole('button', { name: '测试' }))[0]);
    expect(screen.getByRole('option', { name: '安装验证（主动作 + verify）' })).toBeEnabled();
    expect(screen.getByRole('option', { name: '回滚验证（rollback + 可选目标版本 verify）' })).toBeEnabled();
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '测试模式' }), 'rollback');
    expect(screen.getByText('回滚将修改环境状态')).toBeInTheDocument();
    expect(screen.getByText('来源：2.0.0-rc1 · Draft')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '1.9.0 · released · 合同目标' })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '测试环境' }), 'environment-test');
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));

    expect(await screen.findByRole('region', { name: '完整执行计划' })).toHaveTextContent('所属版本 2.0.0-rc1');
    expect(screen.getByRole('region', { name: '完整执行计划' })).toHaveTextContent('所属版本 1.9.0');
    expect(screen.getByRole('button', { name: '确认提交回滚验证' })).toBeEnabled();

    await userEvent.selectOptions(screen.getByRole('combobox', { name: '回滚验证策略' }), 'rollback_only');
    expect(screen.queryByRole('region', { name: '完整执行计划' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认提交回滚验证' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));
    expect(await screen.findByRole('region', { name: '完整执行计划' })).toHaveTextContent('managed/runtime/rollback.yml');
    expect(screen.getByRole('region', { name: '完整执行计划' })).not.toHaveTextContent('managed/runtime/verify.yml');
    await userEvent.click(screen.getByRole('button', { name: '确认提交回滚验证' }));

    await waitFor(() => expect(submitted).toMatchObject({
      environmentId: 'environment-test', mode: 'rollback',
      rollbackVerification: { kind: 'rollback_only' }, expectedPlanDigest: 'digest-rollback-only',
    }));
    expect(previews).toHaveLength(2);
    expect(previews[0]).toMatchObject({ rollbackVerification: { kind: 'target_release', releaseId: 'release-runtime-stable' } });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('lets an environment owner maintain environment variables in a new revision', async () => {
    let submitted: Record<string, string> | undefined;
    const environment = (variables: Record<string, string>) => ({
      id: 'environment-test', name: 'Test Environment', ownerId: dave.id,
      currentRevision: { id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], parameters: {}, variables, credentialRefs: [] },
    });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-test/variables') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body)).variables;
        return json(environment(submitted ?? {}));
      }
      if (url.endsWith('/environments')) return json([environment({})]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await userEvent.click(await screen.findByRole('button', { name: '环境变量' }));
    await userEvent.click(screen.getByRole('button', { name: '添加变量' }));
    await userEvent.type(screen.getByRole('textbox', { name: '环境变量名' }), 'image_registry');
    await userEvent.type(screen.getByRole('textbox', { name: /环境变量 IMAGE_REGISTRY 的值/ }), '192.168.88.54:5000/');
    await userEvent.click(screen.getByRole('button', { name: '保存新 Revision' }));

    await waitFor(() => expect(submitted).toEqual({ IMAGE_REGISTRY: '192.168.88.54:5000/' }));
    expect(await screen.findByText('环境 Revision 已更新')).toBeInTheDocument();
  });

  it('locks the selected environment when submitting a Dockerfile image build', async () => {
    const draft = {
      id: 'release-image-draft', componentId: 'component-image', version: '1.0.0-rc1', type: 'atomic',
      state: 'draft', status: 'draft', releaseNotes: 'Image candidate', parameters: [], dependencies: [], actions: [],
    };
    let submitted: FormData | undefined;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-image-draft/image-builds') && init?.method === 'POST') {
        submitted = init.body as FormData;
        return json({
          id: 'image-build-test', releaseId: draft.id, environmentId: 'environment-build', environmentRevisionId: 'environment-build-r3',
          requestedBy: alice.id, status: 'queued', dockerfileSha256: 'a'.repeat(64), imageTag: '1.0.0-rc1',
          imageRef: '192.168.88.54:5000/components/image:1.0.0-rc1', createdAt: new Date().toISOString(),
        });
      }
      if (url.endsWith('/component-releases/release-image-draft/image-builds')) return json([]);
      if (url.endsWith('/components')) return json([{
        id: 'component-image', name: 'Image component', slug: 'image', ownerId: alice.id,
        layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required', latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/environments')) return json([{
        id: 'environment-build', name: 'Build Environment', ownerId: dave.id,
        currentRevision: { id: 'environment-build-r3', environmentId: 'environment-build', revision: 3, facts: {}, hosts: [], parameters: {}, variables: { IMAGE_REGISTRY: '192.168.88.54:5000' }, credentialRefs: [] },
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-image');
    await userEvent.click(await screen.findByRole('button', { name: '构建镜像' }));
    expect(await screen.findByText('IMAGE_REGISTRY=192.168.88.54:5000')).toBeInTheDocument();
    await userEvent.upload(screen.getByLabelText('Dockerfile'), new File(['FROM scratch\n'], 'Dockerfile', { type: 'text/plain' }));
    const submitButton = screen.getByRole('button', { name: '上传并构建' });
    await waitFor(() => expect(submitButton).toBeEnabled());
    fireEvent.submit(submitButton.closest('form')!);

    await waitFor(() => expect(submitted).toBeDefined());
    expect(submitted?.get('environmentId')).toBe('environment-build');
    expect(submitted?.get('tag')).toBe('1.0.0-rc1');
  });

  it('sends an explicit empty CredentialRef list and keeps it cleared after reopening', async () => {
    let draft = {
      id: 'release-credential-draft', componentId: 'component-credential', version: '1.0.0-rc1', type: 'atomic' as const,
      state: 'draft' as const, status: 'draft' as const, releaseNotes: 'Credential draft', parameters: [], dependencies: [],
      actions: [{ id: 'action-install', name: 'install', type: 'install' as const, playbook: 'managed/credential/install.yml', requiredCredentials: ['K8S_BOOTSTRAP_TOKEN'] }],
    };
    let submitted: Record<string, unknown> | undefined;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-credential-draft') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body));
        draft = { ...draft, ...submitted, state: 'draft', status: 'draft' } as typeof draft;
        return json(draft);
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-credential', name: 'Credential component', slug: 'credential', ownerId: alice.id,
        layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required',
        latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-credential');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByText('K8S_BOOTSTRAP_TOKEN')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '清空全部' }));
    expect(screen.getByText('动作配置有未保存变更')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled();
    await userEvent.click(screen.getByRole('button', { name: '保存 Draft' }));

    await waitFor(() => expect(submitted).toBeDefined());
    expect((submitted?.actions as Array<Record<string, unknown>>)[0].requiredCredentials).toEqual([]);
    expect(await screen.findByText(/已清除 1 个 CredentialRef/)).toBeInTheDocument();

    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByText('未声明 CredentialRef')).toBeInTheDocument();
    expect(screen.queryByText('K8S_BOOTSTRAP_TOKEN')).not.toBeInTheDocument();
  });

  it('persists multiple lifecycle actions with distinct managed Playbooks', async () => {
    const previousRelease = {
      id: 'release-docker-previous', componentId: 'component-docker', version: '25.0.0', type: 'atomic',
      state: 'released', status: 'released', releaseNotes: 'Previous Docker Runtime', parameters: [], dependencies: [], actions: [],
    };
    let draft = {
      id: 'release-docker-draft', componentId: 'component-docker', version: '26.1.0', type: 'atomic',
      state: 'draft', status: 'draft', releaseNotes: 'Docker Runtime draft', parameters: [], dependencies: [],
      actions: [{ name: 'install', type: 'install', playbook: 'managed/docker/release-docker-draft/install.yml', timeoutSeconds: 1800, riskLevel: 'low' }],
    };
    let submittedActions: Array<Record<string, unknown>> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.includes('/component-releases/release-docker-draft/playbook?path=') && (!init?.method || init.method === 'GET')) {
        return json({
          path: 'managed/docker/release-docker-draft/install.yml',
          filename: 'install.yml',
          content: '---\n- name: Install Docker\n  hosts: all\n  tasks: []\n',
          sha256: 'existing-playbook-sha256',
        });
      }
      if (url.endsWith('/component-releases/release-docker-draft/playbook') && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body));
        return json({
          path: `managed/docker/release-docker-draft/${body.filename}`,
          filename: body.filename,
          content: body.content,
          sha256: 'playbook-sha256',
        });
      }
      if (url.endsWith('/component-releases/release-docker-draft') && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body));
        submittedActions = body.actions;
        draft = { ...draft, ...body, state: 'draft', status: 'draft' };
        return json(draft);
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-docker', name: 'Docker Runtime', slug: 'docker', ownerId: alice.id,
        layer: 'runtime_state', category: 'runtime', kind: 'software', requiredness: 'profile_required',
        latestRelease: draft, releases: [draft, previousRelease],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-docker');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByRole('textbox', { name: 'Playbook 文件名' })).toHaveAttribute('pattern', '[A-Za-z0-9][A-Za-z0-9._\\-]*\\.(yml|yaml)');

    await userEvent.click(screen.getByRole('button', { name: '载入编辑器' }));
    expect(await screen.findByDisplayValue(/Install Docker/)).toBeInTheDocument();
    expect(screen.getByText('内容已保存')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled();

    const playbookEditor = screen.getByRole('textbox', { name: 'Playbook 在线编辑器' });
    fireEvent.change(playbookEditor, { target: { value: 'temporary local edit' } });
    fireEvent.change(playbookEditor, { target: { value: '---\n- name: Install Docker\n  hosts: all\n  tasks: []\n' } });
    expect(screen.getByText('内容已保存')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled();

    fireEvent.change(playbookEditor, {
      target: { value: '---\n- name: Locally changed install\n  hosts: all\n  tasks: []\n' },
    });
    const confirm = vi.fn(() => true);
    vi.stubGlobal('confirm', confirm);
    await userEvent.click(screen.getByRole('button', { name: '新增动作' }));
    expect(confirm).toHaveBeenCalledWith('当前 Playbook 有未保存内容，确认放弃并新增动作？');
    expect(within(screen.getByRole('tablist', { name: 'Ansible 动作' })).getAllByRole('button', { name: '安装' }).at(-1)).toHaveClass('active');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '动作类型' }), 'verify');
    expect(screen.getByRole('textbox', { name: 'Playbook 文件名' })).toHaveValue('verify.yml');
    await userEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
    await screen.findByText('Playbook 已保存');

    await userEvent.click(screen.getByRole('button', { name: '新增动作' }));
    expect(within(screen.getByRole('tablist', { name: 'Ansible 动作' })).getAllByRole('button', { name: '安装' }).at(-1)).toHaveClass('active');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '动作类型' }), 'rollback');
    expect(screen.getByRole('textbox', { name: 'Playbook 文件名' })).toHaveValue('rollback.yml');
    const sourceRelease = screen.getByRole('combobox', { name: '来源 Release' });
    const targetRelease = screen.getByRole('combobox', { name: '目标 Release' });
    expect(within(sourceRelease).getByRole('option', { name: '26.1.0 · release-docker-draft' })).toBeInTheDocument();
    expect(within(targetRelease).getByRole('option', { name: '25.0.0 · release-docker-previous' })).toBeInTheDocument();
    await userEvent.selectOptions(sourceRelease, 'release-docker-draft');
    await userEvent.selectOptions(targetRelease, 'release-docker-previous');
    await userEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: '保存 Draft' }));

    await waitFor(() => expect(submittedActions).toHaveLength(3));
    expect(submittedActions.map((action) => action.type)).toEqual(['install', 'verify', 'rollback']);
    expect(submittedActions.map((action) => action.name)).toEqual(['install', 'verify', 'rollback']);
    expect(submittedActions.map((action) => action.playbook)).toEqual([
      'managed/docker/release-docker-draft/install.yml',
      'managed/docker/release-docker-draft/verify.yml',
      'managed/docker/release-docker-draft/rollback.yml',
    ]);
    expect(submittedActions[2]).toMatchObject({ fromReleaseId: 'release-docker-draft', toReleaseId: 'release-docker-previous' });
    expect(await screen.findByText('动作 3/3：安装、验证、回滚')).toBeInTheDocument();

    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByRole('button', { name: '安装' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '验证' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '回滚' })).toBeInTheDocument();
  });

  it('adapts the backend graph DTO into an editable React Flow node', async () => {
    installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    const node = await screen.findByText('bke-cert');
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeInTheDocument();
    fireEvent.click(node);
    expect(screen.getByDisplayValue('bootstrap_host')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'rollback' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'uninstall' })).not.toBeInTheDocument();
  });

  it('confirms before creating a revision and does not submit when cancelled', async () => {
    const released = {
      id: 'scenario-confirm-r1', scenarioId: 'scenario-confirm', revision: 1, state: 'released', status: 'released',
      nodes: [], edges: [], executionPolicy: {},
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST') {
        return json({ ...released, id: 'scenario-confirm-r2', revision: 2, state: 'draft', status: 'draft' }, 201);
      }
      if (url.endsWith('/scenarios')) return json([{
        id: 'scenario-confirm', slug: 'scenario-confirm', name: 'Confirm Scenario', ownerId: carol.id,
        currentRevisionId: released.id, currentRevision: released, revisions: [released],
      }]);
      if (url.endsWith('/environments')) return json([]);
      if (url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    const confirm = vi.fn(() => false);
    vi.stubGlobal('confirm', confirm);
    renderApp('/scenarios');

    await userEvent.click(await screen.findByRole('button', { name: '新 Revision' }));
    expect(confirm).toHaveBeenCalledWith('确认从 Revision 1 创建 Revision 2？\n新 Revision 将立即成为当前草稿。');
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(false);

    confirm.mockReturnValue(true);
    await userEvent.click(screen.getByRole('button', { name: '新 Revision' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(true));
  });

  it('shows revision history and can abandon the current draft', async () => {
    const released = {
      id: 'scenario-history-r1', scenarioId: 'scenario-history', revision: 1, state: 'released', status: 'released',
      nodes: [], edges: [], executionPolicy: {},
    };
    const draft = {
      id: 'scenario-history-r2', scenarioId: 'scenario-history', revision: 2, state: 'draft', status: 'draft',
      nodes: [], edges: [], executionPolicy: {},
    };
    const abandoned = { ...draft, state: 'abandoned', status: 'abandoned' };
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
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    const confirm = vi.fn(() => true);
    vi.stubGlobal('confirm', confirm);
    renderApp('/scenarios');

    const revisionSelect = await screen.findByRole('combobox', { name: 'Revision' });
    expect(within(revisionSelect).getByRole('option', { name: 'r2 · 草稿 · 当前' })).toBeInTheDocument();
    expect(within(revisionSelect).getByRole('option', { name: 'r1 · 已发布' })).toBeInTheDocument();
    await userEvent.selectOptions(revisionSelect, released.id);
    expect(screen.getByRole('button', { name: '环境运行' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '放弃草稿' })).not.toBeInTheDocument();
    await userEvent.selectOptions(revisionSelect, draft.id);
    await userEvent.click(screen.getByRole('button', { name: '放弃草稿' }));

    expect(confirm).toHaveBeenCalledWith('确认放弃 Revision 2 草稿？\n系统将恢复到最近的不可变 Revision，当前草稿会保留在历史记录中。');
    expect(await screen.findByText('草稿已放弃')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Revision' })).toHaveValue(released.id));
    expect(within(screen.getByRole('combobox', { name: 'Revision' })).getByRole('option', { name: 'r2 · 已放弃' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新 Revision' })).toBeInTheDocument();
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
    installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    const paletteButton = await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    await userEvent.click(paletteButton);
    expect(await screen.findByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument();

    EventSourceMock.instances.at(-1)?.emit('release.published');
    await act(async () => { await new Promise((resolve) => window.setTimeout(resolve, 450)); });

    await waitFor(() => expect(screen.getByRole('button', { name: /containerd.*已使用 2 次/ })).toBeInTheDocument());
  });

  it('does not reload scenario component metadata for a run log event', async () => {
    const fetchMock = installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    await screen.findByRole('button', { name: /containerd.*已使用 1 次/ });
    const componentCallsBefore = fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/components')).length;

    EventSourceMock.instances.at(-1)?.emit('run.log');
    await act(async () => { await new Promise((resolve) => window.setTimeout(resolve, 450)); });

    expect(fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/components'))).toHaveLength(componentCallsBefore);
  });

  it('submits only declared scenario run inputs with a test run', async () => {
    const fetchMock = installFetch({ initialUser: carol, withScenario: true });
    renderApp('/scenarios');
    await userEvent.click(await screen.findByRole('button', { name: '环境测试' }));
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '共享测试环境' }), 'environment-test');
    await userEvent.type(screen.getByLabelText('运行参数 rollback_version'), '1.0.0');
    await userEvent.click(screen.getByRole('button', { name: '开始完整测试' }));

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/scenario-revisions/scenario-openfuyao-r1/test-runs'));
      expect(call).toBeDefined();
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({ environmentId: 'environment-test', runInput: { rollback_version: '1.0.0' } });
    });
  });
});

describe('declared run input conversion', () => {
  it('keeps only declarations and preserves JSON scalar/object types', () => {
    expect(uniqueRunInputs(['replicas', 'mode', 'replicas', undefined])).toEqual(['mode', 'replicas']);
    expect(parseRunInput(['replicas', 'enabled', 'settings', 'version', 'empty'], {
      replicas: '3', enabled: 'true', settings: '{"strategy":"safe"}', version: '1.0.0', empty: ' ', ignored: 'no',
    })).toEqual({ replicas: 3, enabled: true, settings: { strategy: 'safe' }, version: '1.0.0' });
  });
});
