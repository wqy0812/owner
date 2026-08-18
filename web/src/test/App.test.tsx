import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  it('shows the dashboard summary and all primary navigation entries', async () => {
    renderApp();
    expect(await screen.findByRole('heading', { name: /早上好/ })).toBeInTheDocument();
    for (const label of ['概览', '组件', '场景', '环境', '运行', '通知']) {
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
    await userEvent.selectOptions(screen.getByLabelText('查看版本合同'), 'release-kube-proxy-new');
    expect(screen.getByText('没有直接依赖')).toBeInTheDocument();
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
    await userEvent.click(await screen.findByRole('button', { name: '配置参数' }));
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
    expect(screen.getAllByText('本层暂无组件').length).toBeGreaterThan(0);
		expect(screen.getByText('0 个参数映射')).toBeInTheDocument();
    expect(screen.getByText('0 个公开参数')).toBeInTheDocument();
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
