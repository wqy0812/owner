import { beforeEach, expect, it, vi } from 'vitest';
import { DashboardPage } from '../pages/DashboardPage';
import { dave, defaultResponse, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
import { screen } from './render';
const renderApp = pageRenderer(DashboardPage, '/');
beforeEach(() => { installFetch(); });


it('shows catalog backup warnings on the 环境 Owner workbench', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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
    return defaultResponse(input, init);
  }));

  renderApp();

  expect(await screen.findByRole('heading', { name: '发布目录灾备需要处理' })).toBeInTheDocument();
  expect(screen.getByText('最近一次发布目录备份失败')).toBeInTheDocument();
  expect(screen.getByText('git push failed')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /检查发布目录灾备/ })).toHaveAttribute('href', '/environments#catalog-repository');
});
