import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { screen, waitFor, within } from './render';

import { DisasterRecoveryPage } from '../pages/DisasterRecoveryPage';
import { dave, installFetch, isEnvironmentList, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(DisasterRecoveryPage, '/disaster-recovery');

beforeEach(() => { installFetch(); });

describe("DisasterRecoveryPage", () => {




  it('explains the backup strategy and lets an 环境 Owner create an immediate recovery point', async () => {
    const fetchMock = installFetch({ initialUser: dave });
    renderApp('/disaster-recovery');

    expect(await screen.findByRole('heading', { name: '备份策略' })).toBeInTheDocument();
    expect(screen.getByText('发布后自动备份')).toBeInTheDocument();
    expect(screen.getByText('前台立即备份')).toBeInTheDocument();
    expect(screen.getByText('完整性与范围')).toBeInTheDocument();
    const disasterRecoveryActions = screen.getByRole('group', { name: '灾备操作' });
    await within(disasterRecoveryActions).findByRole('button', { name: '更换备份仓库' });
    expect(within(disasterRecoveryActions).getAllByRole('button').map((button) => button.textContent?.trim())).toEqual([
      '更换备份仓库', '创建私有仓库', '立即备份',
    ]);

    await userEvent.click(screen.getByRole('button', { name: '立即备份' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/catalog-repository/backups'), expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByText('Git 恢复点已创建')).toBeInTheDocument();
    expect(screen.getByText(/backup\/20260829T080000Z-test/)).toBeInTheDocument();
  });

  it('explains when Catalog backup is disabled and keeps mutation entry points closed', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(dave);
      if (url.endsWith('/catalog-repository')) return json({
        enabled: false,
        configured: false,
        branch: 'catalog',
        allowedRoot: '',
        recoveryPoints: [],
        reasonCode: 'catalog_backup_disabled',
        reason: '发布目录灾备未在服务端启用，请由平台 Owner 设置 CLUSTERFORGE_BACKUP_ENABLED=true 并重启服务。',
      });
      if (url.endsWith('/workbench')) return json({
        generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
        summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
        assets: { components: 0, scenarios: 0, environments: 0 }, items: [],
      });
      if (isEnvironmentList(url) || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
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
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
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
      return defaultResponse(input, init);
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
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
    }));

    renderApp('/disaster-recovery');

    const connect = await screen.findByRole('button', { name: '接入已有备份仓库' });
    expect(screen.queryByRole('button', { name: '从已有 Git 仓库恢复' })).not.toBeInTheDocument();
    await userEvent.click(connect);
    expect(screen.getByRole('heading', { name: '接入已有 Catalog 仓库' })).toBeInTheDocument();
  });

  it('keeps repository reconfiguration available when the selected repository is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
      return defaultResponse(input, init);
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
});
