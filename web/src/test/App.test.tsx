import { StrictMode } from 'react';
import { MemoryRouter, useNavigate } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { AppProvider } from '../context/AppContext';
import { optionsFor, selectOption } from './antdInteractions';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { act, fireEvent, render, screen, waitFor, within } from './render';
import { pendingResponse } from './testLifecycle';

import { alice, carol, components, dave, installFetch, installReadModelFixtures, isEnvironmentList, json } from './fixtures/appFixtures';

function renderApp(path = '/', modernReadModels = false) {
  if (!modernReadModels) installReadModelFixtures();
  return render(<MemoryRouter initialEntries={[path]}><AppProvider><App /></AppProvider></MemoryRouter>);
}

function HistoryBackButton() {
  const navigate = useNavigate();
  return <button onClick={() => navigate(-1)}>测试返回</button>;
}


beforeEach(() => { installFetch(); });

describe("platform shell and RBAC UI", () => {
  it('waits for identity initialization before requesting protected resources', async () => {
    let resolveSwitch!: (response: Response) => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/users')) return json([alice, dave, carol]);
      if (url.endsWith('/session/me')) return json({ error: { code: 'UNAUTHORIZED', message: '未登录' } }, 401);
      if (url.endsWith('/session/switch')) return pendingResponse(resolve => { resolveSwitch = resolve; }, init?.signal);
      if (url.endsWith('/components')) return json(components);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    installReadModelFixtures();
    render(<StrictMode><MemoryRouter initialEntries={['/components']}><AppProvider><App /></AppProvider></MemoryRouter></StrictMode>);
    expect(screen.getByRole('status')).toHaveTextContent(/正在初始化 .*身份/);
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
    for (const label of ['我的工作', '组件', '集群', '环境', '运行', '平台说明书', '通知中心']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument();
    }
    expect(within(screen.getByRole('navigation', { name: '主导航' })).queryByRole('link', { name: '通知中心' })).not.toBeInTheDocument();
    expect(screen.getByText(/无密码身份模式/)).toBeInTheDocument();
    expect(screen.getByText('当前没有待办')).toBeInTheDocument();
    expect(screen.getByText(/首页主任务仍是处理交付待办/)).toBeInTheDocument();
    expect(screen.queryByText(/允许以“未验证”状态发布/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '集群' })).toHaveAttribute('href', '/scenarios');
  });

  it('shows each switchable identity as one role and one plain name', async () => {
    renderApp();
    const switcher = await screen.findByLabelText('切换演示身份');
    const labels = within(await optionsFor(switcher)).getAllByRole('option').map((option) => option.textContent);
    expect(labels).toEqual([
      '组件 Owner · 林晓',
      '组件 Owner · 周工',
      '环境 Owner · 王维',
      '平台 Owner · 赵宁',
      '集群 Owner · 陈晨',
    ]);
    for (const label of labels) expect(label?.split('·')).toHaveLength(2);
    expect(switcher).not.toHaveTextContent(/Runtime|Kubernetes|集群交付|基础设施|平台管理/);
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

  it('explains a blocking work item and links to its exact resource', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
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
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
    }));

    renderApp('/components?selected=component-containerd&release=release-containerd-2&action=validate');
    expect(await screen.findByRole('dialog', { name: '环境验证 v2.1.1' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    await userEvent.click(screen.getByRole('button', { name: '展开 1 项原因' }));
    await userEvent.click(screen.getByRole('link', { name: /再次环境验证/ }));
    expect(await screen.findByRole('dialog', { name: '环境验证 v2.1.1' })).toBeInTheDocument();
  });

  it('changes visible owner actions after a server-backed identity switch', async () => {
    renderApp('/components');
    expect(await screen.findByRole('button', { name: '新建组件' })).toBeInTheDocument();
    await selectOption(screen.getByLabelText('切换演示身份'), '环境 Owner · 王维');
    await waitFor(() => expect(screen.queryByRole('button', { name: '新建组件' })).not.toBeInTheDocument());
    expect(screen.getByLabelText('切换演示身份').closest('.ant-select')).toHaveTextContent('环境 Owner · 王维');
    expect(screen.getByText('当前以环境 Owner · 王维身份浏览。')).toBeInTheDocument();
  });

  it('reloads the role-scoped workbench safely after an identity switch', async () => {
    renderApp();
    expect(await screen.findByRole('heading', { name: /早上好，林晓/ })).toBeInTheDocument();
    await selectOption(screen.getByLabelText('切换演示身份'), '环境 Owner · 王维');
    expect(await screen.findByRole('heading', { name: /早上好，王维/ })).toBeInTheDocument();
    expect(screen.getByText('当前没有待办')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '页面暂时无法显示' })).not.toBeInTheDocument();
  });

  it('keeps environment selection synchronized with browser history', async () => {
    const environments = ['a', 'b'].map((id) => ({
      id: `environment-${id}`, name: `Environment ${id.toUpperCase()}`, ownerId: dave.id,
      currentRevision: { id: `environment-${id}-r1`, environmentId: `environment-${id}`, revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
    }));
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (isEnvironmentList(url)) return json(environments);
      if (url.endsWith('/runs') || url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
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
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/scenarios')) return json(scenarios);
      if (url.endsWith('/runs') || url.endsWith('/components') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    render(<MemoryRouter initialEntries={['/scenarios?selected=scenario-a&revision=scenario-a-r1', '/scenarios?selected=scenario-b&revision=scenario-b-r1']} initialIndex={1}><HistoryBackButton /><AppProvider><App /></AppProvider></MemoryRouter>);

    await waitFor(() => expect(screen.getByRole('combobox', { name: '当前场景' }).closest('.ant-select')).toHaveTextContent('Scenario B'));
    await userEvent.click(screen.getByRole('button', { name: '测试返回' }));
    await waitFor(() => expect(screen.getByRole('combobox', { name: '当前场景' }).closest('.ant-select')).toHaveTextContent('Scenario A'));
  });

  it('opens the shared platform capabilities and navigates to the workflow', async () => {
    renderApp('/manual');
    expect(await screen.findByRole('heading', { name: '平台功能与核心概念' })).toBeInTheDocument();
    for (const heading of ['核心对象与关系', '组件与版本管理', '场景编排与发布', '环境与主机管理', '运行、审批与日志', '平台治理与审核', '灾备与恢复']) {
      expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    }
    await userEvent.click(screen.getByRole('link', { name: /工作流总览/ }));
    expect(await screen.findByRole('heading', { name: '平台工作流程总览' })).toBeInTheDocument();
  });

  it('shows the platform workflow overview from the platform manual entry', async () => {
    renderApp('/manual/workflow');
    expect(await screen.findByRole('heading', { name: '平台说明书' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '平台工作流程总览' })).toBeInTheDocument();
    for (const label of ['1. 组件 Release', '2. 场景版本', '3. 环境版本', '4. Run']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getAllByText(/当前为项目首个版本（V1）/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/环境仅用于测试，不是生产环境/).length).toBeGreaterThan(0);
    expect(screen.getByRole('heading', { name: '场景节点数不等于环境主机数' })).toBeInTheDocument();
    expect(screen.getByText(/实际数量以当前版本为准/)).toBeInTheDocument();
    const commonButtons = screen.getByRole('region', { name: '全员公共按钮操作目录' });
    expect(commonButtons).toHaveTextContent('切换演示身份');
    expect(commonButtons).toHaveTextContent('灾备目录');
    expect(commonButtons).toHaveTextContent('刷新使用新版本');
    expect(commonButtons).toHaveTextContent('全部已读 / 标为已读');
    expect(commonButtons).toHaveTextContent('搜索运行日志 / 日志流筛选');
    expect(commonButtons).toHaveTextContent('复制结果 / 下载已加载日志');
    expect(commonButtons).toHaveTextContent('重新加载');
    expect(screen.queryByText(/允许以未验证状态发布|可以未验证发布/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /查看我的操作手册/ })).toHaveAttribute('href', '/manual/role');
  });

  it('updates the role manual after switching the current owner identity', async () => {
    renderApp('/manual/role');
    expect(await screen.findByRole('heading', { name: '组件 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '组件 Owner 操作路径' })).toBeInTheDocument();
    let buttonDirectory = screen.getByRole('region', { name: '组件 Owner 按钮操作目录' });
    for (const label of ['新建组件', '全新发布线', '空白创建 / 复制现有版本', '批量导入 / 预检并导入', '保存参数合同 / 保存直接依赖', '加入候选集 / 撤回候选', '保存 Playbook', '预览执行计划 / 刷新执行计划', '上传并构建']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }
    expect(buttonDirectory).not.toHaveTextContent('允许以未验证状态发布');

    await selectOption(screen.getByLabelText('切换演示身份'), '集群 Owner · 陈晨');

    expect(await screen.findByRole('heading', { name: '集群 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '集群 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '组件 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '集群 Owner 按钮操作目录' });
    for (const label of ['新建场景 / 创建场景', '新增版本', '新增分支', '导入模板 / 载入草稿', '导出 JSON', '保存草稿', '预览候选集并发布 / 确认原子发布', 'DAG / 节点表 / 参数总览', '放大 / 缩小 / 适配视图', '环境测试 / 环境运行', '提交安装测试 / 提交升级测试']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }

    await selectOption(screen.getByLabelText('切换演示身份'), '环境 Owner · 王维');

    expect(await screen.findByRole('heading', { name: '环境 Owner 操作手册' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '环境 Owner 操作路径' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '集群 Owner 操作路径' })).not.toBeInTheDocument();
    buttonDirectory = screen.getByRole('region', { name: '环境 Owner 按钮操作目录' });
    for (const label of ['新建环境 / 创建环境', '添加节点 / 编辑节点 / 移除节点', '主机组管理 / 应用更改', '立即检查', '一键回滚至干净状态', '移除环境 / 永久删除环境 / 确认归档环境', '恢复环境', '放弃本页更改', '保存新版本', '确认创建版本', '基于此恢复', '拒绝', '批准执行', '批量审批 / 确认批量批准', '创建私有仓库 / 创建并接入', '立即备份', '从恢复点恢复空库 / 预览恢复 / 确认恢复空库']) {
      expect(buttonDirectory).toHaveTextContent(label);
    }
  });

  it('documents deployment handoff and version refresh steps by role', async () => {
    renderApp('/manual/deployment');
    expect(await screen.findAllByRole('heading', { name: '部署与版本切换交接' })).toHaveLength(2);
    for (const heading of ['平台部署人员', '组件 Owner', '集群 Owner', '环境 Owner', '交接证据']) {
      expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    }
    expect(screen.getByText(/旧应用区域已经 inert/)).toBeInTheDocument();
    expect(screen.getByText(/旧 planDigest 不再使用/)).toBeInTheDocument();
    expect(screen.getByRole('region', { name: '部署交接检查清单' })).toHaveTextContent('业务记录计数未被只读验收改变');
  });

  it('closes a component release dialog when the selected route changes', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
    }));

    renderApp('/components?selected=component-controller-manager');
    await userEvent.click(await screen.findByRole('button', { name: '查看详情' }));
    expect(screen.getByRole('dialog', { name: 'controller-1.17.5 Release 详情' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /kube-scheduler/ }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'kube-scheduler' })).toBeInTheDocument();
  });
});

describe("scenario workspace loading and settings", () => {
  it('redirects the removed reference rebuild route without showing its navigation entry', async () => {
    installFetch({ initialUser: carol });
    renderApp('/reference-rebuild');
    expect(await screen.findByRole('heading', { name: '场景编排' })).toBeVisible();
    expect(screen.queryByRole('link', { name: '参考重建' })).not.toBeInTheDocument();
    expect(screen.queryByText('从旧备份重建')).not.toBeInTheDocument();
  });
});

it('lets an 环境 Owner preview and confirm an empty-database Git restore', async () => {
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

it('hides current-run links in run details while keeping workbench and external actions available', async () => {
  const run = {
    id: 'run-self', status: 'failed', environmentId: 'environment-test', environmentName: 'Test Environment',
    name: 'Failed Run', error: 'credential is not configured', createdAt: '2026-08-25T09:00:00Z', finishedAt: '2026-08-25T09:01:00Z',
  };
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
    return defaultResponse(input, init);
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
