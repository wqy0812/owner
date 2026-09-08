import { beforeEach, describe, expect, it, vi } from 'vitest';
import { moreAction, selectOption } from './antdInteractions';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { screen, waitFor, within } from './render';

import { ComponentsPage } from '../pages/ComponentsPage';
import { alice, carol, components, dave, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(ComponentsPage, '/components');

beforeEach(() => { installFetch(); });

describe("ComponentVerification", () => {
  it('describes released environment validation as a state-changing Run instead of a Draft-only test', async () => {
    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: '环境验证' }));
    expect(screen.getByRole('dialog', { name: '环境验证 v2.1.1' })).toHaveTextContent('先预览锁定计划，确认提交后创建单个 Ansible 作业');
    expect(screen.getByRole('dialog', { name: '环境验证 v2.1.1' })).toHaveTextContent('回滚按其绑定的检查确认恢复基线');
    expect(screen.queryByText(/直接对当前 Draft/)).not.toBeInTheDocument();
  });

  it('shows Draft readiness and links current install and rollback evidence', async () => {
    const draft = {
      ...components[0].releases[0], id: 'release-containerd-draft', version: 'v2.2.0-rc1', status: 'draft',
      readiness: { status: 'ready', blockers: [], installEvidenceRunId: 'run-install', rollbackEvidenceRunId: 'run-rollback' }, dependencies: [], parameters: [],
      actions: [{ kind: 'install', playbook: 'install.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        ...components[0], latestRelease: draft, releases: [draft], readContext: {
          workItems: [], parameterConsumers: [], evidence: {
            [draft.id]: {
              currentInstall: { id: 'run-install', status: 'succeeded', environmentId: 'environment-test', environmentName: 'Six node lab', createdAt: '2026-08-24T01:00:00Z', matchesContract: true },
              currentRollback: { id: 'run-rollback', status: 'succeeded', environmentId: 'environment-test', environmentName: 'Six node lab', createdAt: '2026-08-24T02:00:00Z', matchesContract: true }, currentById: {},
            }
          }
        }
      }]);
      if (url.endsWith('/runs')) return json([{
        id: 'run-install', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'install', environmentId: 'environment-test', environmentName: 'Six node lab', finishedAt: '2026-08-24T01:00:00Z',
        steps: [{ id: 'install', name: 'install', action: 'install', status: 'succeeded' }, { id: 'verify', name: 'verify', action: 'verify', status: 'succeeded' }],
      }, {
        id: 'run-rollback', kind: 'component_test', status: 'succeeded', componentReleaseId: draft.id, action: 'rollback', environmentId: 'environment-test', environmentName: 'Six node lab', finishedAt: '2026-08-24T02:00:00Z',
        steps: [{ id: 'rollback', name: 'rollback', action: 'rollback', status: 'succeeded' }, { id: 'verify-after', name: 'verify', action: 'verify', status: 'succeeded' }],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    const readiness = await screen.findByLabelText('Draft v2.2.0-rc1 发布就绪度');
    expect(readiness).toHaveTextContent('5/5');
    expect(within(readiness).queryByRole('link', { name: /Six node lab/ })).not.toBeInTheDocument();
    expect(within(readiness).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '5');
    await userEvent.click(within(readiness).getByRole('button', { name: '安装与验证 · 已完成' }));
    expect(within(readiness).getByRole('link', { name: /Six node lab/ })).toHaveAttribute('href', '/runs?selected=run-install');
    await userEvent.click(within(readiness).getByRole('button', { name: '回退与回退后验证 · 已完成' }));
    expect(within(readiness).getByRole('link', { name: /Six node lab/ })).toHaveAttribute('href', '/runs?selected=run-rollback');
    await userEvent.click(within(readiness).getByRole('button', { name: '收起详情' }));
    expect(within(readiness).queryByRole('link', { name: /Six node lab/ })).not.toBeInTheDocument();
    expect(within(readiness).getByRole('button', { name: '预览影响并发布' })).toBeEnabled();
  });

  it('shows all component and scenario Run evidence grouped by stable scenario', async () => {
    const fallback = installFetch();
    const release = { ...components[0].releases[0], readiness: { status: 'ready', blockers: [], installEvidenceRunId: 'run-component-current' } };
    const evidence = [
      { id: 'run-scenario-a-latest', kind: 'scenario_run', status: 'succeeded', scenarioId: 'scenario-a', scenarioName: '场景 A', scenarioRevisionId: 'scenario-a-r2', environmentId: 'environment-test', environmentName: 'Kubernetes 测试集群 1', createdAt: '2026-08-31T05:00:00Z' },
      { id: 'run-scenario-a-test', kind: 'scenario_test', status: 'failed', scenarioId: 'scenario-a', scenarioName: '场景 A', scenarioRevisionId: 'scenario-a-r1', environmentId: 'environment-test', environmentName: 'Kubernetes 测试集群 1', createdAt: '2026-08-31T04:00:00Z' },
      { id: 'run-scenario-b', kind: 'scenario_run', status: 'running', scenarioId: 'scenario-b', scenarioName: '场景 B', scenarioRevisionId: 'scenario-b-r1', environmentId: 'environment-test', environmentName: 'Kubernetes 测试集群 2', createdAt: '2026-08-31T03:00:00Z' },
      { id: 'run-component-current', kind: 'component_test', status: 'succeeded', componentReleaseId: release.id, action: 'install', environmentId: 'environment-test', environmentName: 'Kubernetes 测试集群 1', createdAt: '2026-08-31T02:00:00Z' },
      { id: 'run-component-history', kind: 'component_test', status: 'cancelled', componentReleaseId: release.id, action: 'rollback', environmentId: 'environment-test', environmentName: 'Kubernetes 测试集群 1', createdAt: '2026-08-31T01:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/components')) return json([{ ...components[0], latestRelease: release, releases: [release] }]);
      if (url.endsWith(`/component-releases/${release.id}/run-evidence`)) return json(evidence);
      return fallback(input, init);
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: 'Run 证据' }));
    const dialog = await screen.findByRole('dialog', { name: 'Run 证据 v2.1.1' });
    expect(dialog).toHaveTextContent('2 个场景');
    expect(dialog).toHaveTextContent('3');
    expect(dialog).toHaveTextContent('当前安装证据');
    expect(dialog).toHaveTextContent('历史证据');
    expect(dialog).toHaveTextContent('场景 A');
    expect(dialog).toHaveTextContent('1 次正式运行 · 1 次完整测试');
    expect(dialog).toHaveTextContent('场景 B');
    expect(dialog).toHaveTextContent('1 次正式运行 · 0 次完整测试');
    expect(within(dialog).getAllByText('执行中')).toHaveLength(2);
    const scenarioA = within(dialog).getByText('场景 A').closest('details');
    if (!scenarioA) throw new Error('missing grouped Scenario A evidence');
    await userEvent.click(within(scenarioA).getByText('场景 A'));
    const links = within(scenarioA).getAllByRole('link');
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute('href', '/runs?selected=run-scenario-a-latest');
    expect(links[1]).toHaveAttribute('href', '/runs?selected=run-scenario-a-test');
    expect(within(scenarioA).getByText('失败')).toBeInTheDocument();
  });

  it('shows empty Release Run evidence and retries a failed evidence request', async () => {
    const fallback = installFetch();
    let attempts = 0;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/component-releases/release-containerd-2/run-evidence')) {
        attempts++;
        if (attempts === 1) return json({ error: { code: 'INTERNAL', message: 'temporary evidence failure' } }, 500);
        return json([]);
      }
      return fallback(input, init);
    }));

    renderApp('/components');
    await userEvent.click(await screen.findByRole('button', { name: 'Run 证据' }));
    const dialog = await screen.findByRole('dialog', { name: 'Run 证据 v2.1.1' });
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('temporary evidence failure');
    await userEvent.click(within(dialog).getByRole('button', { name: '重试' }));
    expect(await within(dialog).findByText('暂无组件验证 Run')).toBeInTheDocument();
    expect(within(dialog).getByText('尚未被场景运行使用')).toBeInTheDocument();
    expect(attempts).toBe(2);
  });

  it('only shows the Release Run evidence entry to the owning component Owner', async () => {
    installFetch({ initialUser: dave });
    renderApp('/components');
    expect(await screen.findByRole('button', { name: /containerd/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Run 证据' })).not.toBeInTheDocument();
  });

  it('does not count rollback-only runs as Draft delivery evidence', async () => {
    const draft = {
      ...components[0].releases[0], id: 'release-containerd-rollback-only', version: 'v2.2.0-rc2', status: 'draft',
      readiness: { status: 'blocked', blockers: [{ code: 'rollback_evidence_missing', message: '当前合同缺少回滚及回滚后验证证据', actionUrl: '/components?action=validate' }], installEvidenceRunId: 'run-install' }, dependencies: [], parameters: [],
      actions: [{ kind: 'install', playbook: 'install.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    const readiness = await screen.findByLabelText('Draft v2.2.0-rc2 发布就绪度');
    expect(readiness).toHaveTextContent('4/5');
    expect(within(readiness).getByRole('button', { name: '预览影响并发布' })).toBeDisabled();
  });

  it('previews downstream impact before deprecating a released component version', async () => {
    let deprecated = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json(components);
      if (url.endsWith('/component-releases/release-containerd-2/impact?operation=deprecate')) return json({ changeKind: 'deprecation', componentOwners: [], scenarioOwners: [], scenarios: [], paths: [] });
      if (url.endsWith('/component-releases/release-containerd-2/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...components[0].releases[0], status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components');
    await moreAction('废弃');
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
      if (url.endsWith('/component-releases/release-containerd-2/impact?operation=deprecate')) return json({
        changeKind: 'deprecation',
        componentOwners: [], scenarioOwners: [{ id: carol.id, name: carol.name }],
        scenarios: [{ id: 'scenario-kubernetes', name: 'Kubernetes 集群' }], paths: [['containerd']], scenarioRunCount: 2,
      });
      if (url.endsWith('/component-releases/release-containerd-2/deprecate') && init?.method === 'POST') {
        deprecated = true;
        return json({ ...components[0].releases[0], status: 'deprecated' });
      }
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    await moreAction('废弃');
    const dialog = screen.getByRole('dialog', { name: '废弃 v2.1.1' });
    expect(dialog).toHaveTextContent('该组件版本不能废弃');
    expect(dialog).toHaveTextContent('已经被 2 个场景 Run 锁定');
    expect(within(dialog).getByRole('button', { name: '确认废弃版本' })).toBeDisabled();
    expect(deprecated).toBe(false);
  });

  it('opens the component batch import flow with the renamed entry', async () => {
    renderApp('/components');
    await moreAction('批量导入', '组件目录更多操作');
    expect(screen.getByRole('dialog', { name: '批量导入组件' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '预检并导入' })).toBeInTheDocument();
  });

  it('previews rollback with its bound checks and submits the selected action and locked digest', async () => {
    const draft = {
      id: 'release-runtime-draft', componentId: 'component-runtime', version: '2.0.0-rc1', status: 'draft', releaseNotes: 'Rollback candidate',
      actions: [
        { id: 'check-before', name: '确认可回滚', kind: 'check', playbook: 'tasks/checks/check-before.yml' },
        { id: 'check-restored', name: '确认旧版本恢复', kind: 'check', playbook: 'tasks/checks/check-restored.yml' },
        { id: 'rollback-runtime', name: '回滚运行时', kind: 'rollback', playbook: 'tasks/rollback.yml', preCheckActionId: 'check-before', postCheckActionId: 'check-restored', fromReleaseId: 'release-runtime-draft', toReleaseId: 'release-runtime-stable' },
      ],
    };
    const previews: Array<Record<string, unknown>> = [];
    let submitted: Record<string, unknown> | undefined;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-runtime-draft/test-plan') && init?.method === 'POST') {
        previews.push(JSON.parse(String(init.body)));
        return json({
          environmentId: 'environment-test', environmentRevisionId: 'environment-test-r1', destructive: true, requiresApproval: true, planDigest: 'digest-bound-rollback', deliveryRequirements: [], steps: [
            { phase: 'pre', action: 'check', actionId: 'check-before' }, { phase: 'execute', action: 'rollback', actionId: 'rollback-runtime' }, { phase: 'post', action: 'check', actionId: 'check-restored' },
          ].map((step, index) => ({ ...step, order: index + 1, componentId: 'component-runtime', componentName: 'Runtime', releaseId: draft.id, releaseVersion: draft.version, playbook: `tasks/${step.actionId}.yml`, limit: 'runtime_nodes', needsApproval: true }))
        });
      }
      if (url.endsWith('/component-releases/release-runtime-draft/test-runs') && init?.method === 'POST') {
        submitted = JSON.parse(String(init.body));
        return json({ id: 'run-rollback-draft', kind: 'component_test', status: 'awaiting_approval', environmentId: 'environment-test', destructive: true });
      }
      if (url.endsWith('/components')) return json([{ id: 'component-runtime', name: 'Runtime', slug: 'runtime', ownerId: alice.id, layer: 'runtime_state', tags: [], latestRelease: draft, releases: [draft] }]);
      if (url.endsWith('/environments')) return json([{ id: 'environment-test', name: 'Six node test', ownerId: dave.id, status: 'ready' }]);
      if (url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));
    renderApp('/components?selected=component-runtime');
    await userEvent.click((await screen.findAllByRole('button', { name: '环境验证' }))[0]);
    await selectOption(screen.getByRole('combobox', { name: '验证动作' }), /回滚运行时/);
    await selectOption(screen.getByRole('combobox', { name: '目标环境' }), 'Six node test');
    expect(screen.getByRole('button', { name: '确认提交验证' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));
    const plan = await screen.findByRole('region', { name: '完整执行计划' });
    for (const phase of ['前置检查', '执行动作', '后置检查']) expect(plan).toHaveTextContent(`Runtime · ${phase}`);
    await userEvent.click(screen.getByRole('button', { name: '确认提交验证' }));
    await waitFor(() => expect(submitted).toMatchObject({ environmentId: 'environment-test', mode: 'install_verify', actionId: 'rollback-runtime', expectedPlanDigest: 'digest-bound-rollback' }));
    expect(previews).toEqual([{ environmentId: 'environment-test', mode: 'install_verify', actionId: 'rollback-runtime' }]);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

it('renders actionable missing-CredentialRef details in the component validation modal', async () => {
  const baseFetch = installFetch();
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/component-releases/release-containerd-2/test-plan') && init?.method === 'POST') {
      return json({
        error: {
          code: 'invalid_request',
          message: 'environment is missing required CredentialRefs: K8S_ENCRYPTION_KEY',
          explanation: {
            reasons: [{
              code: 'environment.credentials_missing',
              message: 'environment is missing required CredentialRefs: K8S_ENCRYPTION_KEY',
              cause: { kind: 'platform_rule', summary: '目标环境版本缺少执行计划要求的 CredentialRef' },
              nextAction: { label: '查看目标环境凭据', href: '/environments?selected=environment-test&tab=credentials' },
            }],
            primaryAction: { label: '查看目标环境凭据', href: '/environments?selected=environment-test&tab=credentials' },
            secondaryActions: [],
          },
        }
      }, 400);
    }
    return baseFetch(input, init);
  });
  vi.stubGlobal('fetch', fetchMock);

  renderApp('/components?selected=component-containerd&release=release-containerd-2');
  await userEvent.click(await screen.findByRole('button', { name: '环境验证' }));
  await selectOption(screen.getByRole('combobox', { name: '验证动作' }), /upgrade/);
  await selectOption(screen.getByRole('combobox', { name: '目标环境' }), /测试环境|Test Environment/);
  await userEvent.click(screen.getByRole('button', { name: '预览执行计划' }));

  expect(await screen.findByRole('heading', { name: '执行准备被阻断' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /查看目标环境凭据/ })).toHaveAttribute('href', '/environments?selected=environment-test&tab=credentials');
  await userEvent.click(screen.getByRole('button', { name: '展开 1 项原因' }));
  expect(screen.getAllByText(/K8S_ENCRYPTION_KEY/)).toHaveLength(2);
});
