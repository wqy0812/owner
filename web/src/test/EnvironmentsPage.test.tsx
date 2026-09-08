import { beforeEach, describe, expect, it, vi } from 'vitest';
import { moreAction, selectOption } from './antdInteractions';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { screen, waitFor, within } from './render';

import { EnvironmentsPage } from '../pages/EnvironmentsPage';
import { dave, installFetch, isEnvironmentList, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(EnvironmentsPage, '/environments');

beforeEach(() => { installFetch(); });

describe("EnvironmentsPage", () => {
  it('lets owners explicitly clear removed component fields before saving an environment revision', async () => {
    const key = 'release:draft-capacity:capacity';
    const originalValues = { [key]: 'small' };
    const fetchMock = installFetch({ initialUser: dave, parameterFields: [], environmentParameters: originalValues });
    renderApp('/environments?selected=environment-test&tab=parameters');
    const remove = await screen.findByRole('button', { name: `移除失效参数 ${key}` });
    expect(screen.getByRole('region', { name: '失效环境参数' })).toHaveTextContent('1 项待清理');
    expect(screen.getByRole('region', { name: '失效环境参数' })).toHaveTextContent('small');
    expect(screen.getByRole('button', { name: '保存新版本' })).toBeDisabled();
    await userEvent.click(remove);
    await userEvent.click(screen.getByRole('button', { name: '放弃本页更改' }));
    expect(screen.getByRole('button', { name: `移除失效参数 ${key}` })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: `移除失效参数 ${key}` }));
    await userEvent.click(screen.getByRole('button', { name: '保存新版本' }));
    await userEvent.type(within(screen.getByRole('dialog')).getByRole('textbox', { name: '变更原因' }), '移除已删除的组件字段');
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '确认创建版本' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/environment-test/parameters') && init?.method === 'PUT')).toBe(true));
    const call = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/environment-test/parameters') && init?.method === 'PUT')!;
    expect(JSON.parse(String(call[1]?.body)).values).toEqual({});
    expect(originalValues).toEqual({ [key]: 'small' });
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
      if (url.endsWith('/environment-variable-definitions')) return json([{ id: 'image-registry', name: 'IMAGE_REGISTRY', label: '镜像仓库', description: '组件镜像仓库', usage: 0 }]);
      if (url.endsWith('/environment-parameter-fields')) return json([]);
      if (isEnvironmentList(url)) return json([environment({})]);
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    const environmentActions = await screen.findByRole('group', { name: '环境操作' });
    await userEvent.click(within(environmentActions).getByRole('button', { name: '更多操作' }));
    expect((await screen.findAllByRole('menuitem')).map(item => item.textContent?.trim())).toEqual([
      '安全导出', '导出含凭据', '移除环境', '重置环境',
    ]);
    await userEvent.keyboard('{Escape}');
    await userEvent.click(await screen.findByRole('tab', { name: '环境变量' }));
    await userEvent.click(screen.getByRole('button', { name: '添加变量' }));
    await selectOption(screen.getByRole('combobox', { name: '环境变量名' }), /IMAGE_REGISTRY/);
    await userEvent.type(screen.getByRole('textbox', { name: /环境变量 IMAGE_REGISTRY 的值/ }), 'registry.example.test:5000/');
    await userEvent.click(screen.getByRole('button', { name: '保存新版本' }));
    await userEvent.type(screen.getByRole('textbox', { name: '变更原因' }), '配置测试镜像仓库');
    await userEvent.click(screen.getByRole('button', { name: '确认创建版本' }));

    await waitFor(() => expect(submitted).toEqual({ IMAGE_REGISTRY: 'registry.example.test:5000/' }));
    expect(await screen.findByText('环境版本已更新')).toBeInTheDocument();
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
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    const checkButton = await screen.findByRole('button', { name: '立即检查' });
    expect(screen.getAllByRole('button', { name: '立即检查' })).toHaveLength(1);
    await userEvent.click(checkButton);

    const tcpResults = await screen.findByRole('region', { name: 'TCP 端点检查结果' });
    const sshResults = screen.getByRole('region', { name: 'SSH 检查结果' });
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
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await moreAction('移除环境');
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
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await moreAction('移除环境');
    let dialog = await screen.findByRole('dialog', { name: '归档环境' });
    expect(dialog).toHaveTextContent('已有历史证据，只能归档');
    await userEvent.type(within(dialog).getByRole('textbox', { name: '确认环境名称' }), 'Historical Environment');
    await userEvent.click(within(dialog).getByRole('button', { name: '确认归档环境' }));

    expect(await screen.findByText('环境已归档')).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByText('已归档').length).toBeGreaterThan(0));
    await moreAction('恢复环境');
    dialog = await screen.findByRole('dialog', { name: '恢复归档环境' });
    await userEvent.type(within(dialog).getByRole('textbox', { name: '确认环境名称' }), 'Historical Environment');
    await userEvent.click(within(dialog).getByRole('button', { name: '恢复环境' }));

    expect(await screen.findByText('环境已恢复')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('该环境已归档，仅保留配置和历史证据；不能创建版本、健康检查、构建或 Run。需要再次使用时先恢复环境。')).not.toBeInTheDocument());
  });

  it('shows an empty rollback state when no installation or recovery baselines exist', async () => {
    const fetchMock = installFetch({ initialUser: dave });
    const baseFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => {
      if (String(input).endsWith('/environments/environment-test/cluster-rollback-plan')) return json({
        environmentId: 'environment-test', environmentName: 'Test Environment', environmentRevisionId: 'environment-test-r1',
        targetHosts: [], sources: [], componentCount: 0, nodeCount: 0,
        destructive: false, requiresApproval: false, planDigest: '', steps: [], deliveryRequirements: [],
      });
      return baseFetch(input, init);
    });
    renderApp('/environments');
    await moreAction('重置环境');
    const dialog = await screen.findByRole('dialog', { name: '重置环境' });
    expect(await within(dialog).findByText('暂无需要重置的集群组件')).toBeInTheDocument();
    expect(dialog).toHaveTextContent('平台没有记录需要恢复的集群安装基线或未完成操作，无需创建重置 Run。');
    for (const message of ['暂时无法读取数据', '重置操作被阻断', '重置会修改集群节点', '指定版本']) {
      expect(dialog).not.toHaveTextContent(message);
    }
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: '创建重置 Run（待审批）' })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('textbox', { name: '确认重置环境名称' })).not.toBeInTheDocument();
    await userEvent.click(within(dialog.querySelector('footer')!).getByRole('button', { name: '关闭' }));
    expect(screen.queryByRole('dialog', { name: '重置环境' })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/cluster-rollback-runs'))).toBe(false);
  });

  it('keeps rollback request failures visible and allows retrying into an empty state', async () => {
    const fetchMock = installFetch({ initialUser: dave });
    const baseFetch = fetchMock.getMockImplementation()!;
    let previewCount = 0;
    fetchMock.mockImplementation(async (input, init) => {
      if (String(input).endsWith('/environments/environment-test/cluster-rollback-plan')) {
        previewCount += 1;
        if (previewCount === 1) return json({ error: { code: 'INTERNAL', message: '读取安装基线失败' } }, 500);
        return json({
          environmentId: 'environment-test', environmentName: 'Test Environment', environmentRevisionId: 'environment-test-r1',
          targetHosts: [], sources: [], componentCount: 0, nodeCount: 0,
          destructive: false, requiresApproval: false, planDigest: '', steps: [], deliveryRequirements: [],
        });
      }
      return baseFetch(input, init);
    });
    renderApp('/environments');
    await moreAction('重置环境');
    const dialog = await screen.findByRole('dialog', { name: '重置环境' });
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('读取安装基线失败');
    expect(within(dialog).queryByText('暂无需要重置的集群组件')).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: '重试' }));
    expect(await within(dialog).findByText('暂无需要重置的集群组件')).toBeInTheDocument();
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
    expect(previewCount).toBe(2);
  });

  it('previews and submits restoration to backup baselines with exact-name confirmation', async () => {
    let submitted: Record<string, string> | undefined;
    const environment = {
      id: 'environment-test', name: 'Test Environment', ownerId: dave.id, schedulingStatus: 'idle',
      currentRevision: { id: 'environment-test-r6', environmentId: 'environment-test', revision: 6, facts: {}, hosts: [], variables: {}, credentialRefs: [] },
    };
    const plan = {
      environmentId: environment.id, environmentName: environment.name, environmentRevisionId: 'environment-test-r6',
      targetHosts: [{ name: 'master-1', address: '192.0.2.10', groups: ['control_plane'] }, { name: 'node-1', address: '192.0.2.11', groups: ['workers'] }],
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
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/environments');
    await moreAction('重置环境');
    const rollbackDialog = await screen.findByRole('dialog', { name: '重置环境' });
    expect(rollbackDialog).toHaveClass('desktop-modal--wide');
    expect(rollbackDialog).toHaveTextContent('恢复全部集群节点，保留 File Station 和镜像仓库');
    expect(within(rollbackDialog).queryByRole('combobox')).not.toBeInTheDocument();
    expect(within(rollbackDialog).queryByRole('button', { name: '预览所选范围' })).not.toBeInTheDocument();
    const targetHosts = within(rollbackDialog).getByRole('region', { name: '重置目标主机' });
    expect(within(targetHosts).getAllByRole('listitem')).toHaveLength(2);
    expect(within(targetHosts).getByText('master-1')).toBeInTheDocument();
    expect(within(targetHosts).getByText('192.0.2.10')).toBeInTheDocument();
    expect(await within(rollbackDialog).findByRole('region', { name: '完整执行计划' })).toHaveTextContent('CoreDNS · 执行动作');
    const confirm = screen.getByRole('textbox', { name: '确认重置环境名称' });
    const submit = screen.getByRole('button', { name: '创建重置 Run（待审批）' });
    expect(submit).toBeDisabled();
    await userEvent.type(confirm, environment.name);
    expect(submit).toBeEnabled();
    await userEvent.click(submit);

    await waitFor(() => expect(submitted).toEqual({ expectedPlanDigest: plan.planDigest, confirmEnvironmentName: environment.name }));
    expect(await screen.findByText('集群重置 Run 已创建')).toBeInTheDocument();
  });
});
