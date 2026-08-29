import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode } from 'react';
import { MemoryRouter, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { AppProvider } from '../context/AppContext';
import { parseRunInput, uniqueRunInputs } from '../components/RunInputFields';
import { EventSourceMock } from './setup';
import { executableActionTypes } from '../types/domain';

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
  tags: ['runtime'],
  latestRelease: { id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released', readiness: { status: 'ready', blockers: [] }, actions: [{ kind: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { kind: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { kind: 'rollback', playbook: 'rollback.yml' }] },
  releases: [{ id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released', readiness: { status: 'ready', blockers: [] }, actions: [{ kind: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { kind: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { kind: 'rollback', playbook: 'rollback.yml' }] }],
}];

function json(data: unknown, status = 200) {
  const completeReleaseDTOs = (value: unknown): unknown => {
    if (Array.isArray(value)) return value.map(completeReleaseDTOs);
    if (!value || typeof value !== 'object') return value;
    const result = Object.fromEntries(Object.entries(value).map(([key, item]) => [key, completeReleaseDTOs(item)]));
    if (typeof result.componentId === 'string' && typeof result.version === 'string' && typeof result.status === 'string' && !result.readiness) {
      result.readiness = { status: 'ready', blockers: [] };
    }
    return result;
  };
  const currentContractData = completeReleaseDTOs(data);
  const body = status >= 400 ? data : Array.isArray(data) ? { items: currentContractData } : { data: currentContractData };
  return Promise.resolve(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }));
}

function isEnvironmentList(url: string) {
  return url.endsWith('/environments') || url.endsWith('/environments?includeArchived=true');
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
    if (url.endsWith('/workbench')) return json({
      generatedAt: '2026-08-25T10:00:00Z', role: current.role,
      summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
      assets: { components: current.role === 'component_owner' ? 1 : 0, scenarios: current.role === 'scenario_owner' ? 1 : 0, environments: current.role === 'environment_owner' ? 1 : 0 },
      items: [],
    });
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
        nodes: [{ id: 'bke-cert', type: 'component', position: { x: 80, y: 80 }, data: { label: 'bke-cert', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', hostGroup: 'bootstrap_host', runInputs: ['rollback_version'] } }],
        edges: [],
      },
      revisions: [{
        id: 'scenario-openfuyao-r1',
        scenarioId: 'scenario-openfuyao',
        revision: 1,
        state: 'draft',
        nodes: [{ id: 'bke-cert', type: 'component', position: { x: 80, y: 80 }, data: { label: 'bke-cert', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', hostGroup: 'bootstrap_host', runInputs: ['rollback_version'] } }],
        edges: [],
      }],
    }] : []);
    if (url.endsWith('/catalog-repository/restore-plan')) return json({ gitCommit: 'a'.repeat(40), schemaContract: 'clusterforge-v1', catalogSha256: 'b'.repeat(64), counts: { components: 2, component_releases: 3, scenarios: 1 }, playbookCount: 4, targetComponentCount: 0, targetScenarioCount: 0, planDigest: 'c'.repeat(64) });
    if (url.endsWith('/catalog-repository/restore')) return json({ restored: true, gitCommit: 'a'.repeat(40), schemaContract: 'clusterforge-v1', catalogSha256: 'b'.repeat(64), counts: { components: 2, component_releases: 3, scenarios: 1 }, playbookCount: 4, targetComponentCount: 0, targetScenarioCount: 0, planDigest: 'c'.repeat(64) }, 201);
    if (url.endsWith('/catalog-repository/backups')) return json({ backupId: '20260829T080000Z-test', status: 'success', reason: 'manual-ui', createdAt: '2026-08-29T08:00:00Z', completedAt: '2026-08-29T08:00:01Z', publicationGeneration: 7, gitCommit: 'd'.repeat(40), gitTag: 'backup/20260829T080000Z-test' }, 201);
    if (url.endsWith('/catalog-repository/create') || url.endsWith('/catalog-repository/connect')) return json({ enabled: true, configured: true, path: '/data/private/catalog.git', branch: 'catalog', allowedRoot: '/data/private', recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }], restoreTargetKnown: true, targetCatalogEmpty: true, targetComponentCount: 0, targetScenarioCount: 0 }, url.endsWith('/catalog-repository/create') ? 201 : 200);
    if (url.endsWith('/catalog-repository')) return json({ enabled: true, configured: true, path: '/data/private/catalog.git', branch: 'catalog', allowedRoot: '/data/private', recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }], restoreTargetKnown: true, targetCatalogEmpty: true, targetComponentCount: 0, targetScenarioCount: 0 });
    if (isEnvironmentList(url)) return json([{ id: 'environment-test', name: 'Test Environment', ownerId: dave.id, currentRevision: { id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] } }]);
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

function HistoryBackButton() {
  const navigate = useNavigate();
  return <button onClick={() => navigate(-1)}>测试返回</button>;
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
    for (const label of ['我的工作', '组件', '场景', '环境', '运行', '操作说明书', '通知中心']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument();
    }
    expect(within(screen.getByRole('navigation', { name: '主导航' })).queryByRole('link', { name: '通知中心' })).not.toBeInTheDocument();
    expect(screen.getByText(/无密码身份模式/)).toBeInTheDocument();
    expect(screen.getByText('当前没有待办')).toBeInTheDocument();
    expect(screen.getByText(/首页主任务仍是处理交付待办/)).toBeInTheDocument();
    expect(screen.queryByText(/允许以“未验证”状态发布/)).not.toBeInTheDocument();
  });

  it('lets the user collapse and expand the desktop sidebar', async () => {
    renderApp();
    const collapse = await screen.findByRole('button', { name: '收起侧边栏' });
    expect(collapse).toHaveAttribute('aria-expanded', 'true');

    await userEvent.click(collapse);
    const expand = screen.getByRole('button', { name: '展开侧边栏' });
    expect(expand).toHaveAttribute('aria-expanded', 'false');
    expect(expand.closest('.app-shell')).toHaveClass('app-shell--sidebar-collapsed');

    await userEvent.click(expand);
    expect(screen.getByRole('button', { name: '收起侧边栏' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('shows catalog backup warnings on the Environment Owner workbench', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 1, actionRequired: 1, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 1 },
        items: [{
          id: 'catalog_backup:health', kind: 'catalog_backup', priority: 'critical', status: 'blocked',
          title: '发布目录灾备需要处理',
          subject: { type: 'catalog_repository', id: 'selected', name: '发布目录灾备' },
          reasons: [{
            code: 'catalog_backup.failed', message: '最近一次发布目录备份失败',
            cause: { kind: 'backup_failure', summary: 'git push failed' },
            nextAction: { label: '检查恢复点', href: '/environments#catalog-repository' },
          }],
          primaryAction: { label: '检查发布目录灾备', href: '/environments#catalog-repository' }, secondaryActions: [],
          updatedAt: '2026-08-29T09:00:00Z',
        }],
      });
      return json([]);
    }));

    renderApp();

    expect(await screen.findByRole('heading', { name: '发布目录灾备需要处理' })).toBeInTheDocument();
    expect(screen.getByText('最近一次发布目录备份失败')).toBeInTheDocument();
    expect(screen.getByText('git push failed')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /检查发布目录灾备/ })).toHaveAttribute('href', '/environments#catalog-repository');
  });

  it('lets an Environment Owner preview and confirm an empty-database Git restore', async () => {
    const fetchMock = installFetch({ initialUser: dave });
    renderApp('/disaster-recovery');
    expect(await screen.findByRole('link', { name: '灾备目录' })).toHaveAttribute('href', '/disaster-recovery');
    expect(await screen.findByRole('heading', { name: '灾备目录' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '发布目录灾备' })).toBeInTheDocument();
    expect(await screen.findByText('/data/private/catalog.git')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /从恢复点恢复空库/ }));
    expect(screen.getByRole('heading', { name: '从 Git 恢复发布目录' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '预览恢复' }));
    expect(await screen.findByText('恢复计划已锁定')).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('确认恢复发布目录'), '恢复发布目录');
    await userEvent.click(screen.getByRole('button', { name: '确认恢复空库' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/catalog-repository/restore') && String(init?.body).includes('expectedPlanDigest'))).toBe(true));
    expect(await screen.findByText('发布目录已从 Git 恢复')).toBeInTheDocument();
  });

  it('explains the backup strategy and lets an Environment Owner create an immediate recovery point', async () => {
    const fetchMock = installFetch({ initialUser: dave });
    renderApp('/disaster-recovery');

    expect(await screen.findByRole('heading', { name: '备份策略' })).toBeInTheDocument();
    expect(screen.getByText('发布后自动备份')).toBeInTheDocument();
    expect(screen.getByText('前台立即备份')).toBeInTheDocument();
    expect(screen.getByText('完整性与范围')).toBeInTheDocument();
    const disasterRecoveryActions = screen.getByRole('group', { name: '灾备操作' });
    expect(within(disasterRecoveryActions).getAllByRole('button').map((button) => button.textContent?.trim())).toEqual([
      '更换备份仓库', '创建私有仓库', '立即备份',
    ]);

    await userEvent.click(screen.getByRole('button', { name: '立即备份' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/catalog-repository/backups'), expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByText('Git 恢复点已创建')).toBeInTheDocument();
    expect(screen.getByText(/backup\/20260829T080000Z-test/)).toBeInTheDocument();
  });

  it('explains when Catalog backup is disabled and keeps mutation entry points closed', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository')) return json({
        enabled: false,
        configured: false,
        branch: 'catalog',
        allowedRoot: '',
        recoveryPoints: [],
        reasonCode: 'catalog_backup_disabled',
        reason: '发布目录灾备未在服务端启用，请由平台管理员设置 CLUSTERFORGE_BACKUP_ENABLED=true 并重启服务。',
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/disaster-recovery');

    expect(await screen.findByText('服务端未启用发布目录灾备')).toBeInTheDocument();
    expect(screen.getByText(/CLUSTERFORGE_BACKUP_ENABLED=true/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '接入已有备份仓库' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '创建私有仓库' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '立即备份' })).toBeDisabled();
    expect(screen.queryByText('暂时无法读取数据')).not.toBeInTheDocument();
  });

  it('allows first-time repository setup when Catalog backup is enabled but unconfigured', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository/create')) return json({
        enabled: true,
        configured: true,
        path: '/data/private/catalogGit',
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [],
        restoreTargetKnown: true,
        targetCatalogEmpty: true,
      }, 201);
      if (url.endsWith('/catalog-repository')) return json({
        enabled: true,
        configured: false,
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [],
        restoreTargetKnown: true,
        targetCatalogEmpty: true,
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/disaster-recovery');

    expect(await screen.findByText('允许根目录：/data/private')).toBeInTheDocument();
    const create = screen.getByRole('button', { name: '创建私有仓库' });
    expect(create).toBeEnabled();
    expect(screen.getByRole('button', { name: '从已有 Git 仓库恢复' })).toBeEnabled();
    await userEvent.click(create);
    expect(screen.getByRole('heading', { name: '创建私有 Catalog 仓库' })).toBeInTheDocument();
    expect(screen.getByText(/路径必须位于 \/data\/private 内/)).toBeInTheDocument();
    const pathInput = screen.getByRole('textbox', { name: '服务器仓库路径' });
    const submit = screen.getByRole('button', { name: '创建并接入' });

    await userEvent.type(pathInput, 'data/private/catalogGit');
    expect(screen.getByRole('alert')).toHaveTextContent('缺少开头“/”');
    expect(pathInput).toHaveAttribute('aria-invalid', 'true');
    expect(submit).toBeDisabled();

    await userEvent.clear(pathInput);
    await userEvent.type(pathInput, 'catalogGit');
    expect(screen.getByText('最终路径：')).toHaveTextContent('/data/private/catalogGit');
    expect(pathInput).toHaveAttribute('aria-invalid', 'false');
    expect(submit).toBeEnabled();

    await userEvent.clear(pathInput);
    await userEvent.type(pathInput, '/data/private/catalogGit');
    expect(screen.getByText('最终路径：')).toHaveTextContent('/data/private/catalogGit');
    expect(submit).toBeEnabled();

    await userEvent.clear(pathInput);
    await userEvent.type(pathInput, 'catalogGit');
    await userEvent.click(submit);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/catalog-repository/create'), expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ path: 'catalogGit' }),
    })));
  });

  it('guides an empty Catalog from an existing Git repository into restore-point selection', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository/connect')) return json({
        enabled: true,
        configured: true,
        path: '/data/private/catalog.git',
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }],
        restoreTargetKnown: true,
        targetCatalogEmpty: true,
        targetComponentCount: 0,
        targetScenarioCount: 0,
      });
      if (url.endsWith('/catalog-repository')) return json({
        enabled: true,
        configured: false,
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [],
        restoreTargetKnown: true,
        targetCatalogEmpty: true,
        targetComponentCount: 0,
        targetScenarioCount: 0,
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/disaster-recovery');

    await userEvent.click(await screen.findByRole('button', { name: '从已有 Git 仓库恢复' }));
    expect(screen.getByRole('heading', { name: '从已有 Git 仓库恢复' })).toBeInTheDocument();
    await userEvent.type(screen.getByRole('textbox', { name: '服务器仓库路径' }), 'catalog.git');
    await userEvent.click(screen.getByRole('button', { name: '验证并继续恢复' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/catalog-repository/connect'), expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ path: 'catalog.git' }),
    })));
    expect(await screen.findByRole('heading', { name: '从 Git 恢复发布目录' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '预览恢复' })).toBeEnabled();
  });

  it('offers repository connection without restore when the current Catalog is not empty', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository')) return json({
        enabled: true,
        configured: false,
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [],
        restoreTargetKnown: true,
        targetCatalogEmpty: false,
        targetComponentCount: 2,
        targetScenarioCount: 1,
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 2, scenarios: 1, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/disaster-recovery');

    const connect = await screen.findByRole('button', { name: '接入已有备份仓库' });
    expect(screen.queryByRole('button', { name: '从已有 Git 仓库恢复' })).not.toBeInTheDocument();
    await userEvent.click(connect);
    expect(screen.getByRole('heading', { name: '接入已有 Catalog 仓库' })).toBeInTheDocument();
  });

  it('keeps repository reconfiguration available when the selected repository is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository')) return json({
        enabled: true,
        configured: true,
        path: '/data/private/catalog.git',
        branch: 'catalog',
        allowedRoot: '/data/private',
        recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }],
        restoreTargetKnown: true,
        targetCatalogEmpty: false,
        targetComponentCount: 2,
        targetScenarioCount: 1,
        behind: true,
        currentGeneration: 4,
        backedUpGeneration: 3,
        lastError: '同步所选私有仓库失败: repository unavailable',
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/disaster-recovery');

    expect(await screen.findByText(/同步所选私有仓库失败/)).toBeInTheDocument();
    expect(screen.getByText(/当前发布目录非空：组件 2 个、场景 1 个/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '从恢复点恢复空库' })).toBeDisabled();
    const reconnect = screen.getByRole('button', { name: '更换备份仓库' });
    expect(reconnect).toBeEnabled();
    await userEvent.click(reconnect);
    expect(screen.getByRole('heading', { name: '接入已有 Catalog 仓库' })).toBeInTheDocument();
    expect(screen.getByText(/路径必须位于 \/data\/private 内/)).toBeInTheDocument();
  });

  it('keeps the legacy Environment disaster-recovery link working', async () => {
    installFetch({ initialUser: dave });
    renderApp('/environments#catalog-repository');

    expect(await screen.findByRole('heading', { name: '灾备目录' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '发布目录灾备' })).toBeInTheDocument();
  });

  it('explains a blocking work item and links to its exact resource', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-25T10:00:00Z', role: 'component_owner',
        summary: { critical: 1, actionRequired: 1, inProgress: 0, informational: 0 },
        assets: { components: 1, scenarios: 0, environments: 0 },
        items: [{
          id: 'component_draft:release-runtime-v2', kind: 'component_draft', priority: 'critical', status: 'blocked',
          title: 'Runtime v2 尚不可发布',
          subject: { type: 'component_release', id: 'release-runtime-v2', parentId: 'component-runtime', name: 'Runtime', version: 'v2' },
          reasons: [{
            code: 'release.rollback_evidence_missing', message: '当前合同缺少回滚及回滚后验证证据', evidenceRunId: 'run-runtime-failed',
            cause: { kind: 'platform_rule', summary: '发布规则要求当前合同同时具备回滚与回滚后验证证据' },
            nextAction: { label: '发起回滚验证', href: '/components?selected=component-runtime&release=release-runtime-v2&action=validate' },
          }],
          primaryAction: { label: '查看失败运行', href: '/runs?selected=run-runtime-failed' }, secondaryActions: [],
          updatedAt: '2026-08-25T09:00:00Z',
        }],
      });
      return json([]);
    }));
    renderApp();

    expect(await screen.findByRole('heading', { name: 'Runtime v2 尚不可发布' })).toBeInTheDocument();
    expect(screen.getByText('当前合同缺少回滚及回滚后验证证据')).toBeInTheDocument();
    expect(screen.getByText('发布规则要求当前合同同时具备回滚与回滚后验证证据')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /发起回滚验证/ })).toHaveAttribute('href', '/components?selected=component-runtime&release=release-runtime-v2&action=validate');
    expect(screen.getByRole('link', { name: /查看失败运行/ })).toHaveAttribute('href', '/runs?selected=run-runtime-failed');
    expect(screen.getByRole('link', { name: '查看证据 Run' })).toHaveAttribute('href', '/runs?selected=run-runtime-failed');
  });

  it('can execute the same component next-step deep link more than once', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/environments')) return json([{ id: 'environment-test', name: 'Test Environment', ownerId: dave.id, currentRevision: { id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] } }]);
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-25T10:00:00Z', role: alice.role,
        summary: { critical: 0, actionRequired: 1, inProgress: 0, informational: 0 },
        assets: { components: 1, scenarios: 0, environments: 0 },
        items: [{
          id: 'component_draft:release-containerd-2', kind: 'component_draft', priority: 'high', status: 'blocked', title: '需要重新验证',
          subject: { type: 'component_release', id: 'release-containerd-2', parentId: 'component-containerd', name: 'containerd', version: 'v2.1.1' },
          reasons: [{ code: 'release.evidence_missing', message: '验证证据缺失', cause: { kind: 'platform_rule', summary: '当前合同需要验证' }, nextAction: { label: '再次环境验证', href: '/components?selected=component-containerd&release=release-containerd-2&action=validate' } }],
          primaryAction: { label: '查看组件', href: '/components?selected=component-containerd&release=release-containerd-2' }, secondaryActions: [], updatedAt: '2026-08-25T09:00:00Z',
        }],
      });
      if (url.endsWith('/runs') || url.endsWith('/scenarios') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components?selected=component-containerd&release=release-containerd-2&action=validate');
    expect(await screen.findByRole('dialog', { name: '环境验证 v2.1.1' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    await userEvent.click(screen.getByRole('button', { name: '展开 1 项原因' }));
    await userEvent.click(screen.getByRole('link', { name: /再次环境验证/ }));
    expect(await screen.findByRole('dialog', { name: '环境验证 v2.1.1' })).toBeInTheDocument();
  });

  it('hides current-run links in run details while keeping workbench and external actions available', async () => {
    const run = {
      id: 'run-self', status: 'failed', environmentId: 'environment-test', environmentName: 'Test Environment',
      name: 'Failed Run', error: 'credential is not configured', createdAt: '2026-08-25T09:00:00Z', finishedAt: '2026-08-25T09:01:00Z',
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/runs/run-self')) return json(run);
      if (url.endsWith('/runs')) return json([run]);
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-25T10:00:00Z', role: dave.role,
        summary: { critical: 1, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 1 },
        items: [{
          id: 'run:run-self', kind: 'run', priority: 'critical', status: 'blocked', title: 'Failed Run 运行失败',
          subject: { type: 'run', id: 'run-self', parentId: 'environment-test', name: 'Failed Run', environment: 'Test Environment' },
          reasons: [
            { code: 'run.failed', message: 'credential is not configured', evidenceRunId: 'run-self', nextAction: { label: '查看失败运行', href: '/runs?selected=run-self' } },
            { code: 'run.repair', message: '环境配置需要修复', nextAction: { label: '检查环境', href: '/environments?selected=environment-test' } },
          ],
          primaryAction: { label: '查看失败诊断', href: '/runs?selected=run-self' },
          secondaryActions: [{ label: '查看环境', href: '/environments?selected=environment-test' }],
          updatedAt: '2026-08-25T09:00:00Z',
        }],
      });
      if (url.endsWith('/components') || url.endsWith('/scenarios') || isEnvironmentList(url) || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    const workbench = renderApp();
    expect(await screen.findByRole('link', { name: /查看失败诊断/ })).toHaveAttribute('href', '/runs?selected=run-self');
    expect(screen.getByRole('link', { name: /查看失败运行/ })).toHaveAttribute('href', '/runs?selected=run-self');
    workbench.unmount();

    renderApp('/runs');
    expect(await screen.findByRole('heading', { name: 'Failed Run' })).toBeInTheDocument();
    expect(screen.getByText('credential is not configured')).not.toBeVisible();
    expect(screen.queryByRole('link', { name: /查看失败诊断/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /查看失败运行/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /检查环境/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /查看环境/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '展开 2 项原因' }));
    expect(screen.getByText('credential is not configured')).toBeVisible();
    expect(screen.getByRole('link', { name: /检查环境/ })).toHaveAttribute('href', '/environments?selected=environment-test');
    expect(screen.getByRole('link', { name: /查看环境/ })).toHaveAttribute('href', '/environments?selected=environment-test');
  });

  it('clears a run action explanation when another run is selected', async () => {
    const runs = [
      { id: 'run-a', status: 'queued', environmentId: 'environment-test', environmentName: 'Test Environment', name: 'Run A', createdAt: '2026-08-25T09:00:00Z' },
      { id: 'run-b', status: 'queued', environmentId: 'environment-test', environmentName: 'Test Environment', name: 'Run B', createdAt: '2026-08-25T08:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/runs/run-a/cancel')) return json({ error: { code: 'conflict', message: 'run is already terminal', explanation: { reasons: [{ code: 'run.already_terminal', message: '该 Run 已进入终态', cause: { kind: 'platform_rule', summary: '该 Run 已结束' }, nextAction: { label: '刷新运行详情', href: '/runs?selected=run-a' } }], primaryAction: { label: '刷新运行详情', href: '/runs?selected=run-a' }, secondaryActions: [] } } }, 409);
      if (url.endsWith('/runs/run-a')) return json(runs[0]);
      if (url.endsWith('/runs/run-b')) return json(runs[1]);
      if (url.endsWith('/runs')) return json(runs);
      if (url.endsWith('/workbench')) return json({ generatedAt: '2026-08-25T10:00:00Z', role: alice.role, summary: { critical: 0, actionRequired: 0, inProgress: 2, informational: 0 }, assets: { components: 0, scenarios: 0, environments: 0 }, items: [] });
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/runs?selected=run-a');
    expect(await screen.findByRole('heading', { name: 'Run A' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(await screen.findByRole('heading', { name: '运行操作被阻断' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /Run B/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Run B' })).toBeInTheDocument());
    expect(screen.queryByRole('heading', { name: '运行操作被阻断' })).not.toBeInTheDocument();
  });

  it('changes visible owner actions after a server-backed identity switch', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: '新建组件' })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), dave.id);
    await waitFor(() => expect(screen.queryByRole('button', { name: '新建组件' })).not.toBeInTheDocument());
    expect(screen.getByText('环境 Owner')).toBeInTheDocument();
  });

  it('reloads the role-scoped workbench safely after an identity switch', async () => {
    renderApp();
    expect(await screen.findByRole('heading', { name: /早上好，Alice Component/ })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), dave.id);
    expect(await screen.findByRole('heading', { name: /早上好，Dave Environment/ })).toBeInTheDocument();
    expect(screen.getByText('当前没有待办')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '页面暂时无法显示' })).not.toBeInTheDocument();
  });

  it('keeps environment selection synchronized with browser history', async () => {
    const environments = ['a', 'b'].map((id) => ({
      id: `environment-${id}`, name: `Environment ${id.toUpperCase()}`, ownerId: dave.id,
      currentRevision: { id: `environment-${id}-r1`, environmentId: `environment-${id}`, revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
    }));
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (isEnvironmentList(url)) return json(environments);
      if (url.endsWith('/runs') || url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    render(<MemoryRouter initialEntries={['/environments?selected=environment-a', '/environments?selected=environment-b']} initialIndex={1}><HistoryBackButton /><AppProvider><App /></AppProvider></MemoryRouter>);

    expect(await screen.findByRole('heading', { name: 'Environment B' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '测试返回' }));
    expect(await screen.findByRole('heading', { name: 'Environment A' })).toBeInTheDocument();
  });

  it('keeps scenario and revision selection synchronized with browser history', async () => {
    const scenarios = ['a', 'b'].map((id) => ({
      id: `scenario-${id}`, name: `Scenario ${id.toUpperCase()}`, ownerId: carol.id, slug: `scenario-${id}`,
      currentRevisionId: `scenario-${id}-r1`,
      currentRevision: { id: `scenario-${id}-r1`, scenarioId: `scenario-${id}`, revision: 1, state: 'draft', nodes: [], edges: [] },
      revisions: [{ id: `scenario-${id}-r1`, scenarioId: `scenario-${id}`, revision: 1, state: 'draft', nodes: [], edges: [] }],
    }));
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/scenarios')) return json(scenarios);
      if (url.endsWith('/runs') || url.endsWith('/components') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    render(<MemoryRouter initialEntries={['/scenarios?selected=scenario-a&revision=scenario-a-r1', '/scenarios?selected=scenario-b&revision=scenario-b-r1']} initialIndex={1}><HistoryBackButton /><AppProvider><App /></AppProvider></MemoryRouter>);

    expect(await screen.findByDisplayValue('Scenario B')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '测试返回' }));
    expect(await screen.findByDisplayValue('Scenario A')).toBeInTheDocument();
  });

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
      return json({});
    }));
    renderApp('/components?selected=component-stale-candidate');
    const withdraw = await screen.findByRole('button', { name: '撤回候选' });
    expect(withdraw).toBeEnabled();
    await userEvent.click(withdraw);
    await waitFor(() => expect(candidateBody).toEqual({ candidate: false }));
  });

  it('shows the platform workflow overview from the operation manual entry', async () => {
    renderApp('/manual');
    expect(await screen.findByRole('heading', { name: '操作说明书' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '平台工作流程总览' })).toBeInTheDocument();
    for (const label of ['1. 组件 Release', '2. 场景 Revision', '3. 环境 Revision', '4. Run']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getAllByText(/当前为项目首个版本（V1）/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/环境仅用于测试，不是生产环境/).length).toBeGreaterThan(0);
    expect(screen.getByRole('heading', { name: '场景节点数不等于环境主机数' })).toBeInTheDocument();
    expect(screen.getByText(/实际数量以当前 Revision 为准/)).toBeInTheDocument();
    const commonButtons = screen.getByRole('region', { name: '全员公共按钮操作目录' });
    expect(commonButtons).toHaveTextContent('切换演示身份');
    expect(commonButtons).toHaveTextContent('刷新使用新版本');
    expect(commonButtons).toHaveTextContent('全部已读 / 标为已读');
    expect(commonButtons).toHaveTextContent('搜索运行日志 / 日志流筛选');
    expect(commonButtons).toHaveTextContent('复制结果 / 下载完整日志');
    expect(commonButtons).toHaveTextContent('重新加载');
    expect(screen.queryByText(/允许以未验证状态发布|可以未验证发布/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /查看我的操作手册/ })).toHaveAttribute('href', '/manual/role');
  });

  it('updates the role manual after switching the current owner identity', async () => {
    renderApp('/manual/role');
    expect(await screen.findByRole('heading', { name: '组件 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '组件 Owner 操作路径' })).toBeInTheDocument();
    let buttonDirectory = screen.getByRole('region', { name: '组件 Owner 按钮操作目录' });
    for (const label of ['新建组件', '批量导入 / 预检并导入', '保存依赖和参数', '加入候选集 / 撤回候选', '保存 Playbook', '预览执行计划 / 刷新执行计划', '上传并构建']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }
    expect(buttonDirectory).not.toHaveTextContent('允许以未验证状态发布');

    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), carol.id);

    expect(await screen.findByRole('heading', { name: '场景 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '场景 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '组件 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '场景 Owner 按钮操作目录' });
    for (const label of ['新建场景 / 创建场景', '导入模板 / 载入草稿', '导出 JSON', '保存草稿', '预览候选集并发布 / 确认原子发布', 'DAG / 节点表', '放大 / 缩小 / 适配视图', '环境测试 / 开始完整测试']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }

    await userEvent.selectOptions(screen.getByLabelText('切换演示身份'), dave.id);

    expect(await screen.findByRole('heading', { name: '环境 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '环境 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '场景 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '环境 Owner 按钮操作目录' });
    for (const label of ['新建环境 / 创建环境', '添加主机 / 删除主机', '立即检查', '一键回滚至干净状态', '移除环境 / 永久删除环境 / 确认归档环境', '恢复环境', '放弃本页更改', '保存新 Revision', '确认创建 Revision', '基于此恢复', '拒绝', '批准执行', '批量审批 / 确认批量批准']) {
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
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
      }, {
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', tags: ['network'],
        latestRelease: {
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        },
        releases: [{
          id: 'release-kube-proxy', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
          parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
          dependencies: [{ upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
        }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));
    renderApp('/components?selected=component-kube-proxy');
    expect((await screen.findAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot')).length).toBeGreaterThan(0);
    expect(screen.getByText('内部')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    expect(screen.getByRole('dialog', { name: '1.17.5 Release 详情' })).toBeInTheDocument();
    expect(screen.getAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot').length).toBeGreaterThan(1);
  });

  it('closes a component release dialog when the selected route changes', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-controller-manager', name: 'kube-controller-manager', slug: 'kube-controller-manager', ownerId: alice.id,
        layer: 'orchestration_core', tags: ['control_plane'],
        latestRelease: { id: 'release-controller-manager', componentId: 'component-controller-manager', version: 'controller-1.17.5', status: 'released' },
        releases: [{ id: 'release-controller-manager', componentId: 'component-controller-manager', version: 'controller-1.17.5', status: 'released' }],
      }, {
        id: 'component-scheduler', name: 'kube-scheduler', slug: 'kube-scheduler', ownerId: alice.id,
        layer: 'orchestration_core', tags: ['control_plane'],
        latestRelease: { id: 'release-scheduler', componentId: 'component-scheduler', version: 'scheduler-1.17.5', status: 'released' },
        releases: [{ id: 'release-scheduler', componentId: 'component-scheduler', version: 'scheduler-1.17.5', status: 'released' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components?selected=component-controller-manager');
    await userEvent.click(await screen.findByRole('button', { name: '查看详情' }));
    expect(screen.getByRole('dialog', { name: 'controller-1.17.5 Release 详情' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /kube-scheduler/ }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'kube-scheduler' })).toBeInTheDocument();
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
    const saveResponse = new Promise<Response>((resolve) => { resolveSave = resolve; });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-scheduler-draft') && init?.method === 'PUT') return saveResponse;
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
        id: 'component-kube-proxy', name: 'kube-proxy', slug: 'kube-proxy', ownerId: alice.id, layer: 'orchestration_core', tags: ['network'],
        latestRelease: { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', status: 'released', parameters: [], dependencies: [] },
        releases: [
          { id: 'release-kube-proxy-new', componentId: 'component-kube-proxy', version: '1.34.3', status: 'released', parameters: [], dependencies: [] },
          {
            id: 'release-kube-proxy-old', componentId: 'component-kube-proxy', version: '1.17.5', status: 'released',
            parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
            dependencies: [{ upstreamComponentId: 'component-kubelet', upstreamComponentName: 'kubelet', upstreamReleaseId: 'release-kubelet', upstreamVersion: '1.17.5', purpose: '复用 kubelet 安装目录', parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }] }],
          },
        ],
      }, {
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] },
        releases: [{ id: 'release-kubelet', componentId: 'component-kubelet', version: '1.17.5', status: 'released', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' }] }],
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
      id: 'release-kube-proxy-draft', componentId: 'component-kube-proxy', version: '1.17.6', status: 'draft',
      parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
      dependencies: [] as Array<Record<string, unknown>>,
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-kube-proxy-draft/contract') && init?.method === 'PUT') {
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
    expect(within(screen.getByLabelText('已发布版本')).getByRole('option', { name: '1.34.3 · Draft' })).toBeInTheDocument();
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
          upstreamComponentId: 'component-kubelet',
          upstreamReleaseId: 'release-kubelet',
          parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }],
        }],
      });
    });
  });

  it('creates a draft from the centralized contract entry', async () => {
    const released = components[0].releases[0];
    const draft = { ...released, id: 'release-containerd-draft', version: 'v2.1.2', status: 'draft', parameters: [], dependencies: [] };
    let created = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: created ? draft : released, releases: created ? [draft, released] : [released] }]);
      if (url.endsWith('/component-releases/release-containerd-2/clone-plan')) return json({ sourceReleaseId: released.id, sourceVersion: released.version, targetVersion: draft.version, planDigest: 'clone-plan', actions: [], playbooks: [], artifactCount: 0 });
      if (url.endsWith('/component-releases/release-containerd-2/clone')) { created = true; return json(draft); }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    vi.stubGlobal('confirm', vi.fn(() => true));
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '创建 Draft 编辑合同' }));
    expect(screen.getByRole('dialog', { name: '创建 Draft 编辑依赖和参数' })).toBeInTheDocument();
    expect(screen.getByText(/已发布且不可直接修改/)).toBeInTheDocument();
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.1.2');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '调整合同');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));

    expect(await screen.findByRole('button', { name: '保存依赖和参数' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('creates an empty Draft without cloning an existing release', async () => {
    const released = components[0].releases[0];
    const draft = { ...released, id: 'release-containerd-empty', version: 'v2.2.0', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [], artifacts: [], images: [] };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components/component-containerd/releases') && init?.method === 'POST') return json(draft);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建空白 Draft' }));
    expect(screen.getByRole('dialog', { name: '新建空白 Draft · containerd' })).toHaveTextContent('不继承依赖、参数、Action、Playbook、介质或镜像');
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.2.0');
    await userEvent.selectOptions(screen.getByLabelText('风险级别'), 'destructive');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '全新合同');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));

    expect(await screen.findByRole('button', { name: '保存依赖和参数' })).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/clone'))).toBe(false);
    const createCall = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/components/component-containerd/releases') && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({ version: 'v2.2.0', releaseNotes: '全新合同', riskLevel: 'destructive', status: 'draft' });
  });

  it('keeps readiness and lifecycle editing on the selected Draft when several Drafts exist', async () => {
    const base = components[0].releases[0];
    const first = { ...base, id: 'release-containerd-draft-a', version: 'v2.2.0-a', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [] };
    const second = { ...base, id: 'release-containerd-draft-b', version: 'v2.2.0-b', status: 'draft', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [] };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: first, releases: [first, second, base] }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components?selected=component-containerd&release=release-containerd-draft-b');
    expect(await screen.findByLabelText('Draft v2.2.0-b 发布就绪度')).toBeInTheDocument();
    expect(screen.queryByLabelText('Draft v2.2.0-a 发布就绪度')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '编辑版本与 Playbook' }));
    expect(screen.getByRole('dialog', { name: '配置 Draft v2.2.0-b' })).toBeInTheDocument();
  });

  it('hides empty Draft creation from non component owners', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    expect(await screen.findByRole('heading', { name: 'containerd', level: 2 })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新建空白 Draft' })).not.toBeInTheDocument();
  });

  it('shows the backend conflict when an empty Draft version already exists', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components/component-containerd/releases') && init?.method === 'POST') return json({ error: { code: 'CONFLICT', message: '版本 v2.1.1 已存在' } }, 409);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建空白 Draft' }));
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.1.1');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '重复版本');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));
    expect(await screen.findByText('版本 v2.1.1 已存在')).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '新建空白 Draft · containerd' })).toBeInTheDocument();
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
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
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
        id: 'component-kubelet', name: 'kubelet', slug: 'kubelet', ownerId: alice.id, layer: 'orchestration_core', tags: ['worker'],
        latestRelease: { id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] },
        releases: [{ id: 'release-kubelet-draft', componentId: 'component-kubelet', version: '1.17.6', status: 'draft', parameters: [{ name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'internal' }] }],
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

  it('searches and filters the component owner catalog without changing component data', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: /containerd/ })).toBeInTheDocument();
    await userEvent.type(screen.getByRole('textbox', { name: '搜索组件' }), 'missing-component');
    expect(screen.getByText('没有匹配的组件')).toBeInTheDocument();
    expect(screen.getByText(/当前详情不在目录筛选结果中/)).toBeInTheDocument();
    await userEvent.clear(screen.getByRole('textbox', { name: '搜索组件' }));
    await userEvent.click(screen.getByRole('button', { name: '有 Draft' }));
    expect(screen.getByText('没有匹配的组件')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '全部' }));
    expect(screen.getByRole('button', { name: /containerd/ })).toBeInTheDocument();
  });

  it('describes released environment validation as a state-changing Run instead of a Draft-only test', async () => {
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '环境验证' }));
    expect(screen.getByRole('dialog', { name: '环境验证 v2.1.1' })).toHaveTextContent('对已发布版本执行生命周期作业');
    expect(screen.getByRole('dialog', { name: '环境验证 v2.1.1' })).toHaveTextContent('提交后会创建 Run，并可能修改目标环境');
    expect(screen.queryByText(/直接对当前 Draft/)).not.toBeInTheDocument();
  });

  it('shows Draft readiness and links current install and rollback evidence', async () => {
    const draft = {
      ...components[0].releases[0], id: 'release-containerd-draft', version: 'v2.2.0-rc1', status: 'draft',
      readiness: { status: 'ready', blockers: [], installEvidenceRunId: 'run-install', rollbackEvidenceRunId: 'run-rollback' }, dependencies: [], parameters: [],
      actions: [{ kind: 'install', playbook: 'install.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: draft, releases: [draft] }]);
      if (url.endsWith('/runs')) return json([{
        id: 'run-install', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'install', environmentId: 'environment-test', environmentName: 'Six node lab', finishedAt: '2026-08-24T01:00:00Z',
        steps: [{ id: 'install', name: 'install', action: 'install', status: 'succeeded' }, { id: 'verify', name: 'verify', action: 'verify', status: 'succeeded' }],
      }, {
        id: 'run-rollback', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'rollback', environmentId: 'environment-test', environmentName: 'Six node lab', finishedAt: '2026-08-24T02:00:00Z',
        steps: [{ id: 'rollback', name: 'rollback', action: 'rollback', status: 'succeeded' }, { id: 'verify-after', name: 'verify', action: 'verify', status: 'succeeded' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    const readiness = await screen.findByLabelText('Draft v2.2.0-rc1 发布就绪度');
    expect(readiness).toHaveTextContent('4/4');
    expect(within(readiness).getAllByRole('link', { name: /Six node lab/ })).toHaveLength(2);
    expect(within(readiness).getByRole('button', { name: '预览影响并发布' })).toBeEnabled();
  });

  it('does not count rollback-only runs as Draft delivery evidence', async () => {
    const draft = {
      ...components[0].releases[0], id: 'release-containerd-rollback-only', version: 'v2.2.0-rc2', status: 'draft',
      readiness: { status: 'blocked', blockers: [{ code: 'rollback_evidence_missing', message: '当前合同缺少回滚及回滚后验证证据', actionUrl: '/components?action=validate' }], installEvidenceRunId: 'run-install' }, dependencies: [], parameters: [],
      actions: [{ kind: 'install', playbook: 'install.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: draft, releases: [draft] }]);
      if (url.endsWith('/runs')) return json([{
        id: 'run-install', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'install', environmentId: 'environment-test',
        steps: [{ id: 'install', name: 'install', action: 'install', status: 'succeeded' }, { id: 'verify', name: 'verify', action: 'verify', status: 'succeeded' }],
      }, {
        id: 'run-rollback-only', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'rollback', environmentId: 'environment-test',
        steps: [{ id: 'rollback', name: 'rollback', action: 'rollback', status: 'succeeded' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    const readiness = await screen.findByLabelText('Draft v2.2.0-rc2 发布就绪度');
    expect(readiness).toHaveTextContent('3/4');
    expect(within(readiness).getByRole('button', { name: '预览影响并发布' })).toBeDisabled();
  });

  it('previews downstream impact before deprecating a released component version', async () => {
    let deprecated = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/component-releases/release-containerd-2/impact')) return json({ componentOwners: [], scenarioOwners: [], scenarios: [], paths: [] });
      if (url.endsWith('/component-releases/release-containerd-2/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...components[0].releases[0], status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '废弃' }));
    expect(deprecated).toBe(false);
    expect(screen.getByRole('dialog', { name: '废弃 v2.1.1' })).toHaveTextContent('这是影响下游选择的状态变更');
    const confirm = screen.getByRole('button', { name: '确认废弃版本' });
    await waitFor(() => expect(confirm).toBeEnabled());
    await userEvent.click(confirm);
    await waitFor(() => expect(deprecated).toBe(true));
  });

  it('blocks deprecation when a scenario Run has locked the component version', async () => {
    let deprecated = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/component-releases/release-containerd-2/impact')) return json({
        componentOwners: [], scenarioOwners: [{ id: carol.id, name: carol.name }],
        scenarios: [{ id: 'scenario-kubernetes', name: 'Kubernetes 集群' }], paths: [['containerd']], scenarioRunCount: 2,
      });
      if (url.endsWith('/component-releases/release-containerd-2/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...components[0].releases[0], status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '废弃' }));
    const dialog = screen.getByRole('dialog', { name: '废弃 v2.1.1' });
    expect(dialog).toHaveTextContent('该组件版本不能废弃');
    expect(dialog).toHaveTextContent('已经被 2 个场景 Run 锁定');
    expect(within(dialog).getByRole('button', { name: '确认废弃版本' })).toBeDisabled();
    expect(deprecated).toBe(false);
  });

  it('opens the component batch import flow with the renamed entry', async () => {
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '批量导入' }));
    expect(screen.getByRole('dialog', { name: '批量导入组件' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '预检并导入' })).toBeInTheDocument();
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
      if (url.endsWith('/component-releases/release-draft-deprecate/impact')) return json({ componentOwners: [], scenarioOwners: [], scenarios: [], paths: [] });
      if (url.endsWith('/component-releases/release-draft-deprecate/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...draft, status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '废弃草稿' }));
    expect(screen.getByRole('dialog', { name: '废弃草稿 2.0.0-rc1' })).toHaveTextContent('Playbook、介质');
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(deprecated).toBe(false);

    await userEvent.click(screen.getByRole('button', { name: '废弃草稿' }));
    const confirm = screen.getByRole('button', { name: '确认废弃草稿' });
    await waitFor(() => expect(confirm).toBeEnabled());
    await userEvent.click(confirm);
    await waitFor(() => expect(deprecated).toBe(true));
  });

  it('shows released details and Playbook content without Draft edit shortcuts', async () => {
    const released = {
      id: 'release-readonly', componentId: 'component-readonly', version: '1.2.3', status: 'released', riskLevel: 'medium',
      releaseNotes: 'immutable release details', parameters: [], dependencies: [],
      actions: [{ name: 'install', kind: 'install', playbook: 'managed/readonly/install.yml', hostGroup: 'workers', timeoutSeconds: 900, requiredCredentials: ['SSH_KEY'] }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-readonly', name: 'Readonly Component', slug: 'readonly-component', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'], latestRelease: released, releases: [released],
      }]);
      if (url.includes('/component-releases/release-readonly/playbook?path=')) return json({
        path: 'managed/readonly/install.yml', filename: 'install.yml', content: '---\n- hosts: workers\n  tasks: []\n', sha256: 'abc123',
      });
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    }));

    renderApp('/components');
    expect(await screen.findByRole('button', { name: '查看详情' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建 Draft 编辑合同' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /创建 Draft 后编辑/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '编辑直接依赖' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    const dialog = screen.getByRole('dialog', { name: '1.2.3 Release 详情' });
    expect(dialog).toHaveTextContent('immutable release details');
    expect(dialog).toHaveTextContent('SSH_KEY');
    expect(await within(dialog).findByText(/hosts: workers/)).toBeInTheDocument();
  });

  it('shows each release environment constraints such as architecture', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
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
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    expect(screen.getAllByText('架构').length).toBeGreaterThan(1);
    expect(screen.getAllByText('x86/amd64').length).toBeGreaterThan(1);
  });

  it('lets the owner choose environment constraints when creating a version', async () => {
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
      if (url.endsWith('/component-releases/release-containerd-2/clone-plan')) {
        return json({ sourceReleaseId: 'release-containerd-2', sourceVersion: 'v2.1.1', targetVersion: 'v2.2.0', planDigest: 'clone-plan', actions: [], playbooks: [], artifactCount: 0 });
      }
      if (url.endsWith('/component-releases/release-containerd-2/clone')) {
        return json({
          id: 'release-containerd-3', componentId: 'component-containerd', version: 'v2.2.0', status: 'draft',
          environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE'], ipFamily: ['IPv4'] },
        });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('confirm', vi.fn(() => true));
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '创建 Draft 编辑合同' }));
    expect(screen.getByRole('dialog', { name: '创建 Draft 编辑依赖和参数' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'x86/amd64' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'ARM/arm64' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'SUSE' })).toBeChecked();
    await userEvent.click(screen.getByRole('checkbox', { name: 'ARM/arm64' }));
    await userEvent.click(screen.getByRole('checkbox', { name: 'IPv4' }));
    await userEvent.click(screen.getByRole('checkbox', { name: '18.04 / 24.04（混合）' }));
    await userEvent.type(screen.getByPlaceholderText('v1.1.0'), 'v2.2.0');
    await userEvent.type(screen.getByPlaceholderText('说明变化和下游注意事项'), '增加 ARM 适配');
    await userEvent.click(screen.getByRole('button', { name: '创建 Draft' }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/clone') && init?.method === 'POST');
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        version: 'v2.2.0',
        releaseNotes: '增加 ARM 适配',
        environmentConstraints: { architecture: ['amd64', 'arm64'], operatingSystem: ['SUSE'], operatingSystemVersion: ['18.04 / 24.04'], ipFamily: ['IPv4'] },
      });
    });
  });

  it('submits the simplified component metadata contract', async () => {
    const fetchMock = installFetch();
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '新建组件' }));
    await userEvent.type(screen.getByPlaceholderText('例如 containerd'), 'storage driver');
    await userEvent.type(screen.getByPlaceholderText('containerd'), 'storage-driver');
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '组件层级' }), 'cluster_service');
    await userEvent.type(screen.getByRole('textbox', { name: '标签' }), 'storage, optional');
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
    await userEvent.type(screen.getByPlaceholderText('例如 containerd'), 'demo');
    await userEvent.type(screen.getByPlaceholderText('containerd'), 'demo');
    await userEvent.click(screen.getByRole('button', { name: '创建组件' }));
    expect(await screen.findByText(/权限不足：只有资源 Owner 可以修改组件/)).toBeInTheDocument();
  });

  it('previews rollback versions, invalidates stale plans, and submits rollback-only with the digest', async () => {
    const draft = {
      id: 'release-runtime-draft', componentId: 'component-runtime', version: '2.0.0-rc1',
      status: 'draft', releaseNotes: 'Rollback candidate', parameters: [], dependencies: [],
      actions: [
        { name: 'install', kind: 'install', playbook: 'managed/runtime/install.yml' },
        { name: 'verify', kind: 'verify', playbook: 'managed/runtime/verify.yml' },
        { name: 'rollback', kind: 'rollback', playbook: 'managed/runtime/rollback.yml', fromReleaseId: 'release-runtime-draft', toReleaseId: 'release-runtime-stable' },
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
          deliveryRequirements: [],
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
        layer: 'runtime_state', tags: ['runtime'],
        latestRelease: draft, releases: [draft, {
          id: 'release-runtime-stable', componentId: 'component-runtime', version: '1.9.0',
          status: 'released', releaseNotes: 'Stable', parameters: [], dependencies: [],
          actions: [{ name: 'verify', kind: 'verify', playbook: 'managed/runtime/verify.yml', hostGroup: 'runtime_nodes' }],
        }],
      }]);
      if (url.endsWith('/environments')) return json([{ id: 'environment-test', name: 'Six node test', ownerId: dave.id, status: 'ready' }]);
      if (url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-runtime');
    await userEvent.click((await screen.findAllByRole('button', { name: '环境验证' }))[0]);
    expect(screen.getByRole('option', { name: '安装验证（主动作 + verify）' })).toBeEnabled();
    expect(screen.getByRole('option', { name: '回退验证（rollback + 可选目标版本 verify）' })).toBeEnabled();
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '验证模式' }), 'rollback');
    expect(screen.getByText('回退验证将修改环境状态')).toBeInTheDocument();
    expect(screen.getByText('来源：2.0.0-rc1 · Draft')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '1.9.0 · released · 合同目标' })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '目标环境' }), 'environment-test');
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));

    expect(await screen.findByRole('region', { name: '完整执行计划' })).toHaveTextContent('所属版本 2.0.0-rc1');
    expect(screen.getByRole('region', { name: '完整执行计划' })).toHaveTextContent('所属版本 1.9.0');
    expect(screen.getByRole('button', { name: '确认提交回退验证' })).toBeEnabled();

    await userEvent.selectOptions(screen.getByRole('combobox', { name: '回退验证策略' }), 'rollback_only');
    expect(screen.queryByRole('region', { name: '完整执行计划' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认提交回退验证' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));
    expect(await screen.findByRole('region', { name: '完整执行计划' })).toHaveTextContent('managed/runtime/rollback.yml');
    expect(screen.getByRole('region', { name: '完整执行计划' })).not.toHaveTextContent('managed/runtime/verify.yml');
    await userEvent.click(screen.getByRole('button', { name: '确认提交回退验证' }));

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
      currentRevision: { id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], variables, credentialRefs: [] },
    });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-test/variables') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body)).variables;
        return json(environment(submitted ?? {}));
      }
      if (isEnvironmentList(url)) return json([environment({})]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    const environmentActions = await screen.findByRole('group', { name: '环境操作' });
    expect(within(environmentActions).getAllByRole('button').map((button) => button.textContent?.trim())).toEqual([
      '安全导出', '导出含引用', '移除环境', '一键回滚至干净状态',
    ]);
    await userEvent.click(await screen.findByRole('tab', { name: '环境变量' }));
    await userEvent.click(screen.getByRole('button', { name: '添加变量' }));
    await userEvent.type(screen.getByRole('textbox', { name: '环境变量名' }), 'image_registry');
    await userEvent.type(screen.getByRole('textbox', { name: /环境变量 IMAGE_REGISTRY 的值/ }), '192.168.88.54:5000/');
    await userEvent.click(screen.getByRole('button', { name: '保存新 Revision' }));
    await userEvent.type(screen.getByRole('textbox', { name: '变更原因' }), '配置测试镜像仓库');
    await userEvent.click(screen.getByRole('button', { name: '确认创建 Revision' }));

    await waitFor(() => expect(submitted).toEqual({ IMAGE_REGISTRY: '192.168.88.54:5000/' }));
    expect(await screen.findByText('环境 Revision 已更新')).toBeInTheDocument();
  });

  it('runs TCP and SSH checks from one button and displays classified errors', async () => {
    const environment = {
      id: 'environment-test', name: 'Test Environment', ownerId: dave.id,
      currentRevision: {
        id: 'environment-test-r1', environmentId: 'environment-test', revision: 1, facts: {},
        hosts: [{ name: 'node-1', address: '192.0.2.10', port: 22, user: 'root', groups: ['all'] }],
        variables: { IMAGE_REGISTRY: '192.0.2.20:5000' }, credentialRefs: [],
      },
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-test/connectivity-checks') && init?.method === 'POST') return json({
        tcpCheck: {
          id: 'tcp-1', environmentId: environment.id, environmentRevisionId: 'environment-test-r1', status: 'degraded', checkedAt: '2026-08-29T10:00:00Z',
          results: [
            { kind: 'host', name: 'node-1', address: '192.0.2.10:22', reachable: true, latencyMs: 8 },
            { kind: 'dependency', name: 'IMAGE_REGISTRY', address: '192.0.2.20:5000', reachable: false, latencyMs: 2001, error: 'TCP 连接失败' },
          ],
        },
        sshCheck: {
          id: 'ssh-1', environmentId: environment.id, environmentRevisionId: 'environment-test-r1', status: 'degraded', durationMs: 1234, checkedAt: '2026-08-29T10:00:01Z',
          results: [{ kind: 'host', name: 'node-1', address: '192.0.2.10:22', user: 'root', status: 'unreachable', errorCode: 'ssh_authentication_failed', message: 'SSH 用户或凭据认证失败' }],
        },
      });
      if (isEnvironmentList(url)) return json([environment]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    const checkButton = await screen.findByRole('button', { name: '立即检查' });
    expect(screen.getAllByRole('button', { name: '立即检查' })).toHaveLength(1);
    await userEvent.click(checkButton);

    const tcpResults = await screen.findByRole('region', { name: 'TCP 端点检查结果' });
    const sshResults = screen.getByRole('region', { name: 'SSH 和 Ansible 检查结果' });
    expect(within(tcpResults).getByText('TCP 连接失败')).toBeInTheDocument();
    expect(within(sshResults).getByText('SSH 用户或凭据认证失败')).toBeInTheDocument();
    expect(within(sshResults).getByText('ssh_authentication_failed')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/connectivity-checks'), expect.objectContaining({ method: 'POST' }));
  });

  it('permanently deletes an environment that has never been used', async () => {
    let environments = [{
      id: 'environment-disposable', name: 'Disposable Environment', ownerId: dave.id,
      currentRevision: { id: 'environment-disposable-r1', environmentId: 'environment-disposable', revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
      revisions: [{ id: 'environment-disposable-r1', environmentId: 'environment-disposable', revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] }],
    }];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-disposable/lifecycle')) return json({ revisionCount: 1, runCount: 0, activeRunCount: 0, imageBuildCount: 0, activeImageBuildCount: 0, installationCount: 0, archived: false, canDelete: true, canArchive: true });
      if (url.endsWith('/environments/environment-disposable') && init?.method === 'DELETE') {
        environments = [];
        return json({ deleted: true });
      }
      if (isEnvironmentList(url)) return json(environments);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await userEvent.click(await screen.findByRole('button', { name: '移除环境' }));
    const dialog = await screen.findByRole('dialog', { name: '永久删除环境' });
    expect(dialog).toHaveTextContent('从未产生 Run、镜像构建或安装基线');
    const submit = within(dialog).getByRole('button', { name: '永久删除环境' });
    expect(submit).toBeDisabled();
    await userEvent.type(within(dialog).getByRole('textbox', { name: '确认环境名称' }), 'Disposable Environment');
    await userEvent.click(submit);

    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/environments/environment-disposable') && init?.method === 'DELETE')).toBe(true));
    expect(await screen.findByText('环境已删除')).toBeInTheDocument();
    expect(await screen.findByText('没有可见环境')).toBeInTheDocument();
  });

  it('archives a historical environment and can restore it', async () => {
    let environment: Record<string, unknown> = {
      id: 'environment-history', name: 'Historical Environment', ownerId: dave.id,
      currentRevision: { id: 'environment-history-r2', environmentId: 'environment-history', revision: 2, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
    };
    const lifecycle = () => ({ revisionCount: 2, runCount: 3, activeRunCount: 0, imageBuildCount: 1, activeImageBuildCount: 0, installationCount: 0, archived: Boolean(environment.archivedAt), canDelete: false, canArchive: !environment.archivedAt });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-history/lifecycle')) return json(lifecycle());
      if (url.endsWith('/environments/environment-history/archive') && init?.method === 'POST') {
        environment = { ...environment, archivedAt: '2026-08-28T12:00:00Z', status: 'offline' };
        return json(environment);
      }
      if (url.endsWith('/environments/environment-history/unarchive') && init?.method === 'POST') {
        const { archivedAt: _archivedAt, ...restored } = environment;
        environment = { ...restored, status: 'ready' };
        return json(environment);
      }
      if (isEnvironmentList(url)) return json([environment]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await userEvent.click(await screen.findByRole('button', { name: '移除环境' }));
    let dialog = await screen.findByRole('dialog', { name: '归档环境' });
    expect(dialog).toHaveTextContent('已有历史证据，只能归档');
    await userEvent.type(within(dialog).getByRole('textbox', { name: '确认环境名称' }), 'Historical Environment');
    await userEvent.click(within(dialog).getByRole('button', { name: '确认归档环境' }));

    expect(await screen.findByText('环境已归档')).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByText('已归档').length).toBeGreaterThan(0));
    await userEvent.click(screen.getByRole('button', { name: '恢复环境' }));
    dialog = await screen.findByRole('dialog', { name: '恢复归档环境' });
    await userEvent.type(within(dialog).getByRole('textbox', { name: '确认环境名称' }), 'Historical Environment');
    await userEvent.click(within(dialog).getByRole('button', { name: '恢复环境' }));

    expect(await screen.findByText('环境已恢复')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('该环境已归档，仅保留配置和历史证据；不能创建 Revision、健康检查、构建或 Run。需要再次使用时先恢复环境。')).not.toBeInTheDocument());
  });

  it('previews and submits a whole-cluster clean rollback with exact-name confirmation', async () => {
    let submitted: Record<string, string> | undefined;
    const environment = {
      id: 'environment-test', name: 'Test Environment', ownerId: dave.id, schedulingStatus: 'idle',
      currentRevision: { id: 'environment-test-r6', environmentId: 'environment-test', revision: 6, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
    };
    const plan = {
      environmentId: environment.id, environmentName: environment.name, environmentRevisionId: 'environment-test-r6',
      sources: [{ runId: 'run-source-install', kind: 'scenario_test', scenarioRevisionId: 'scenario-clean-r1', componentCount: 15 }], componentCount: 15, nodeCount: 21,
      destructive: true, requiresApproval: true, planDigest: 'rollback-plan-digest',
      deliveryRequirements: [],
      steps: [{
        order: 1, componentId: 'component-coredns', componentName: 'CoreDNS', releaseId: 'release-coredns', releaseVersion: '1.6.5-u1',
        action: 'rollback', playbook: 'managed/coredns/rollback.yml', limit: 'control_plane', needsApproval: true,
        backupRef: '/var/lib/clusterforge/backups/environment-test/component-coredns/release-coredns/run-source-install',
        backupInstallRunId: 'run-source-install', backupCapturedAt: '2026-08-24T12:00:00Z', backupPlaybookSha256: 'a'.repeat(64),
      }],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/environments/environment-test/cluster-rollback-plan') && init?.method === 'POST') return json(plan);
      if (url.endsWith('/environments/environment-test/cluster-rollback-runs') && init?.method === 'POST') {
        submitted = JSON.parse(String(init.body));
        return json({ id: 'run-cluster-rollback', kind: 'environment_rollback', status: 'awaiting_approval', environmentId: environment.id, scenarioRevisionId: 'scenario-clean-r1', destructive: true });
      }
      if (isEnvironmentList(url)) return json([environment]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await userEvent.click(await screen.findByRole('button', { name: '一键回滚至干净状态' }));
    const rollbackDialog = await screen.findByRole('dialog', { name: '一键回滚整个集群' });
    expect(rollbackDialog).toHaveClass('modal--wide');
    expect(await within(rollbackDialog).findByRole('region', { name: '整集群回滚计划' })).toHaveTextContent('CoreDNS · rollback');
    const confirm = screen.getByRole('textbox', { name: '确认回滚环境名称' });
    const submit = screen.getByRole('button', { name: '创建回滚 Run（待审批）' });
    expect(submit).toBeDisabled();
    await userEvent.type(confirm, environment.name);
    expect(submit).toBeEnabled();
    await userEvent.click(submit);

    await waitFor(() => expect(submitted).toEqual({ expectedPlanDigest: plan.planDigest, confirmEnvironmentName: environment.name }));
    expect(await screen.findByText('整集群回滚 Run 已创建')).toBeInTheDocument();
  });

  it('locks the selected environment when submitting a Dockerfile image build', async () => {
    const draft = {
      id: 'release-image-draft', componentId: 'component-image', version: '1.0.0-rc1',
      status: 'draft', releaseNotes: 'Image candidate', parameters: [], dependencies: [], actions: [],
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
        layer: 'runtime_state', tags: ['runtime'], latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/environments')) return json([{
        id: 'environment-build', name: 'Build Environment', ownerId: dave.id,
        currentRevision: { id: 'environment-build-r3', environmentId: 'environment-build', revision: 3, facts: {}, hosts: [], variables: { IMAGE_REGISTRY: '192.168.88.54:5000' }, credentialRefs: [] },
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
      id: 'release-credential-draft', componentId: 'component-credential', version: '1.0.0-rc1',
      status: 'draft' as const, releaseNotes: 'Credential draft', parameters: [], dependencies: [],
      actions: [{ id: 'action-install', name: 'install', kind: 'install' as const, playbook: 'managed/credential/install.yml', requiredCredentials: ['K8S_BOOTSTRAP_TOKEN'] }],
    };
    let submitted: Record<string, unknown> | undefined;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-credential-draft') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body));
        draft = { ...draft, ...submitted, status: 'draft' } as typeof draft;
        return json(draft);
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-credential', name: 'Credential component', slug: 'credential', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'],
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
      id: 'release-docker-previous', componentId: 'component-docker', version: '25.0.0',
      status: 'released', releaseNotes: 'Previous Docker Runtime', parameters: [], dependencies: [], actions: [],
    };
    let draft = {
      id: 'release-docker-draft', componentId: 'component-docker', version: '26.1.0',
      status: 'draft', releaseNotes: 'Docker Runtime draft', parameters: [], dependencies: [],
      actions: [{ name: 'install', kind: 'install', playbook: 'managed/docker/release-docker-draft/install.yml', timeoutSeconds: 1800, riskLevel: 'low' }],
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
        draft = { ...draft, ...body, status: 'draft' };
        return json(draft);
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-docker', name: 'Docker Runtime', slug: 'docker', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'],
        latestRelease: draft, releases: [draft, previousRelease],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-docker');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByRole('textbox', { name: 'Playbook 文件名' })).toHaveAttribute('pattern', '[A-Za-z0-9][A-Za-z0-9._\\-]*\\.(yml|yaml)');
    await userEvent.click(screen.getByRole('checkbox', { name: /幂等安装，同时作为升级作业/ }));

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
    expect(submittedActions.map((action) => action.kind)).toEqual(['install', 'verify', 'rollback']);
    expect(submittedActions[0].idempotent).toBe(true);
    expect(submittedActions.map((action) => action.name)).toEqual(['install', 'verify', 'rollback']);
    expect(submittedActions.map((action) => action.playbook)).toEqual([
      'managed/docker/release-docker-draft/install.yml',
      'managed/docker/release-docker-draft/verify.yml',
      'managed/docker/release-docker-draft/rollback.yml',
    ]);
    expect(submittedActions[2]).toMatchObject({ fromReleaseId: 'release-docker-draft', toReleaseId: 'release-docker-previous' });
    expect(await screen.findByText('动作 3/3：安装、验证、回退')).toBeInTheDocument();

    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByRole('button', { name: '安装' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '验证' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '回滚' })).toBeInTheDocument();
  });

  it('downloads the current scenario Revision as JSON instead of using the clipboard', async () => {
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
      await userEvent.click(await screen.findByRole('button', { name: '导出 JSON' }));
      expect(downloadedAs).toBe('openfuyao-management-cluster-build-r1.json');
      expect(exportedBlob?.type).toBe('application/json;charset=utf-8');
      const exportedText = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(exportedBlob!);
      });
      expect(JSON.parse(exportedText)).toMatchObject({ nodes: [{ id: 'bke-cert' }], edges: [] });
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
    const node = await screen.findByText('bke-cert');
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeInTheDocument();
    fireEvent.click(node);
    expect(screen.getByDisplayValue('bootstrap_host')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'rollback' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'uninstall' })).not.toBeInTheDocument();
  });

  it('confirms before creating a revision and does not submit when cancelled', async () => {
    const released = {
      id: 'scenario-confirm-r1', scenarioId: 'scenario-confirm', revision: 1, state: 'released',
      nodes: [], edges: [],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(carol);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/scenarios/scenario-confirm/revision-clone-plan') && init?.method === 'POST') {
        return json({ scenarioId: 'scenario-confirm', sourceRevisionId: released.id, sourceRevision: 1, nextRevision: 2, nodeCount: 0, edgeCount: 0, planDigest: 'scenario-clone-plan' });
      }
      if (url.endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST') {
        return json({ ...released, id: 'scenario-confirm-r2', revision: 2, state: 'draft' }, 201);
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

    await userEvent.click(await screen.findByRole('button', { name: '复制为新 Revision' }));
    await waitFor(() => expect(confirm).toHaveBeenCalledWith('复制预览\nRevision 1 → Revision 2\n0 个节点 · 0 条依赖\n\n确认创建当前 Draft？'));
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(false);

    confirm.mockReturnValue(true);
    await userEvent.click(screen.getByRole('button', { name: '复制为新 Revision' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-confirm/revisions') && init?.method === 'POST')).toBe(true));
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
    expect(screen.getByRole('button', { name: '复制为新 Revision' })).toBeInTheDocument();
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
      return json({});
    });
    vi.stubGlobal('fetch', fetchMock);
    const confirm = vi.fn(() => false);
    vi.stubGlobal('confirm', confirm);
    renderApp('/scenarios');

    await userEvent.click(await screen.findByRole('button', { name: '删除场景' }));
    expect(confirm).toHaveBeenCalledWith('确认永久删除场景“Disposable Scenario”？\n将删除整个场景、1 个未发布 Revision 和相关个人运行参数预设。\n\n仅从未发布且从未产生 Run 的场景允许删除；此操作不可恢复。');
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-delete') && init?.method === 'DELETE')).toBe(false);

    confirm.mockReturnValue(true);
    await userEvent.click(screen.getByRole('button', { name: '删除场景' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/scenarios/scenario-delete') && init?.method === 'DELETE')).toBe(true));
    expect(await screen.findByText('场景已删除')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('暂无场景')).toBeInTheDocument());
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

  it('renders actionable missing-CredentialRef details in the component validation modal', async () => {
    const baseFetch = installFetch();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/component-releases/release-containerd-2/test-plan') && init?.method === 'POST') {
        return json({ error: {
          code: 'invalid_request',
          message: 'environment is missing required CredentialRefs: K8S_ENCRYPTION_KEY',
          explanation: {
            reasons: [{
              code: 'environment.credentials_missing',
              message: 'environment is missing required CredentialRefs: K8S_ENCRYPTION_KEY',
              cause: { kind: 'platform_rule', summary: '目标 Environment Revision 缺少执行计划要求的 CredentialRef' },
              nextAction: { label: '查看目标环境凭据', href: '/environments?selected=environment-test&tab=credentials' },
            }],
            primaryAction: { label: '查看目标环境凭据', href: '/environments?selected=environment-test&tab=credentials' },
            secondaryActions: [],
          },
        } }, 400);
      }
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-containerd&release=release-containerd-2');
    await userEvent.click(await screen.findByRole('button', { name: '环境验证' }));
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '目标环境' }), 'environment-test');
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));

    expect(await screen.findByRole('heading', { name: '验证操作被阻断' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /查看目标环境凭据/ })).toHaveAttribute('href', '/environments?selected=environment-test&tab=credentials');
    await userEvent.click(screen.getByRole('button', { name: '展开 1 项原因' }));
    expect(screen.getAllByText(/K8S_ENCRYPTION_KEY/)).toHaveLength(2);
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
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '共享测试环境' }), 'environment-test');

    expect(screen.getByRole('alert')).toHaveTextContent('当前 DAG 为空');
    expect(screen.getByRole('button', { name: '开始完整测试' })).toBeDisabled();
    expect(fetchMock.mock.calls.some(([request, requestInit]) => String(request).endsWith('/scenario-revisions/scenario-empty-r1/test-runs') && requestInit?.method === 'POST')).toBe(false);
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

describe('action capability conversion', () => {
  it('exposes upgrade once when install is idempotent', () => {
    expect(executableActionTypes([{ type: 'install', playbook: 'install.yml', idempotent: true }])).toEqual(['install', 'upgrade']);
    expect(executableActionTypes([{ type: 'install', playbook: 'install.yml' }])).toEqual(['install']);
    expect(executableActionTypes([
      { type: 'install', playbook: 'install.yml', idempotent: true },
      { type: 'upgrade', playbook: 'upgrade.yml' },
    ])).toEqual(['install', 'upgrade']);
  });
});
