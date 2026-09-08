import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultResponse } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { act, screen, waitFor, within } from './render';
import { EventSourceMock } from './setup';

import { RunsPage } from '../pages/RunsPage';
import { admin, alice, dave, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(RunsPage, '/runs');

beforeEach(() => { installFetch(); });

describe("scoped lightweight read models", () => {
  it('shows candidates beyond all list pages while preserving the 100-item atomic approval limit', async () => {
    const fallback = installFetch({ initialUser: dave });
    const candidates = Array.from({ length: 120 }, (_, i) => ({ id: `candidate-${i}`, approvalId: `approval-${i}`, name: `Candidate ${i}`, environmentId: 'environment-test', environmentName: 'Test', status: 'awaiting_approval', createdAt: '' }));
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://test');
      if (url.pathname === '/api/v1/approvals/batch-candidates') return json(candidates);
      if (url.pathname === '/api/v1/approvals/batch') return json([]);
      if (url.pathname === '/api/v1/runs') return new Response(JSON.stringify({ items: [], page: 1, pageSize: 50, total: 0 }), { headers: { 'Content-Type': 'application/json' } });
      return fallback(input, init);
    }); vi.stubGlobal('fetch', fetchMock);
    renderApp('/runs', true);
    await userEvent.click(await screen.findByRole('button', { name: '批量审批 120' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getAllByRole('checkbox')).toHaveLength(120);
    expect(within(dialog).getAllByRole('checkbox').filter((box) => (box as HTMLInputElement).checked)).toHaveLength(100);
    expect(within(dialog).getByRole('checkbox', { name: '审批候选 candidate-119' })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: '审批候选 candidate-0' }));
    await userEvent.click(within(dialog).getByRole('checkbox', { name: '审批候选 candidate-119' }));
    await userEvent.click(within(dialog).getByRole('button', { name: '确认批量批准' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input) === '/api/v1/approvals/batch')).toBe(true));
    const call = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/approvals/batch')!;
    const ids = JSON.parse(String(call[1]?.body)).approvalIds;
    expect(ids).toHaveLength(100); expect(ids).toContain('approval-119'); expect(ids).not.toContain('approval-0');
  });

  it('loads a cross-page deep link, preserves detail on summary refresh and clamps an empty last page', async () => {
    const fallback = installFetch(); let total = 105;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://test');
      if (url.pathname === '/api/v1/runs') {
        const page = Number(url.searchParams.get('page')); const size = Number(url.searchParams.get('pageSize'));
        return new Response(JSON.stringify({ page, pageSize: size, total, items: page <= Math.ceil(total / size) ? [{ id: 'listed', name: 'Listed summary', status: 'succeeded', environmentId: 'environment-test', environmentName: 'Test', createdAt: '' }] : [] }), { headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname === '/api/v1/runs/old-history/activity') return json({ runId: 'old-history', status: 'succeeded', nextAfterId: 0, hasMore: false, archived: false, logs: [], waitingObservations: [] });
      if (url.pathname === '/api/v1/runs/old-history') return json({ id: 'old-history', name: 'Old historical detail', status: 'succeeded', environmentId: 'environment-test', environmentName: 'Test', steps: [{ id: 'detail-step', name: 'Preserved full step', status: 'succeeded' }] });
      return fallback(input, init);
    }); vi.stubGlobal('fetch', fetchMock);
    renderApp('/runs?page=3&selected=old-history', true);
    expect(await screen.findByText('Preserved full step')).toBeInTheDocument();
    total = 51;
    act(() => EventSourceMock.instances.at(-1)?.emit('run.updated'));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes('page=2&'))).toBe(true));
    expect(screen.getByText('Preserved full step')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '已结束' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes('page=1&') && String(input).includes('filter=finished'))).toBe(true));
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === '/api/v1/runs')).toHaveLength(0);
  });
});

describe("RunsPageIntegration", () => {
  it('reveals the failed step before scrolling from diagnostics and resets tabs for another run', async () => {
    const fallback = installFetch();
    const runs = [
      { id: 'failed', name: 'Failed Run', status: 'failed', environmentId: 'environment-test', environmentName: 'Test', createdAt: '', requestedBy: alice.id, steps: [{ id: 'failed-step', name: 'Failed install', status: 'failed' }] },
      { id: 'next', name: 'Next Run', status: 'succeeded', environmentId: 'environment-test', environmentName: 'Test', createdAt: '', steps: [] },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://test');
      if (url.pathname === '/api/v1/runs') return new Response(JSON.stringify({ items: runs, page: 1, pageSize: 50, total: 2 }), { headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/runs/failed/diagnostics') return json({ runId: 'failed', status: 'failed', capturedAt: '', lastLogId: 1, logCount: 1, items: [{ stepId: 'failed-step', source: 'event', message: 'Installation failed' }] });
      const run = runs.find(item => url.pathname === `/api/v1/runs/${item.id}` || url.pathname === `/api/v1/runs/${item.id}/activity`);
      if (run) return url.pathname.endsWith('/activity') ? json({ runId: run.id, status: run.status, nextAfterId: 0, hasMore: false, archived: false, logs: [], waitingObservations: [] }) : json(run);
      return fallback(input, init);
    }));
    const scroll = vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(function (this: HTMLElement) {
      expect(this.id).toBe('run-step-failed-step');
      expect(this).toBeVisible();
    });
    renderApp('/runs?selected=failed', true);
    await userEvent.click(await screen.findByRole('tab', { name: '诊断' }));
    expect(document.getElementById('run-step-failed-step')).not.toBeVisible();
    await userEvent.click((await screen.findAllByRole('button', { name: '定位失败步骤' })).at(-1)!);
    await waitFor(() => expect(scroll).toHaveBeenCalledWith({ behavior: 'smooth', block: 'center' }));
    expect(screen.getByRole('tab', { name: '步骤与日志' })).toHaveAttribute('aria-selected', 'true');
    await userEvent.click(screen.getByRole('tab', { name: '诊断' }));
    await userEvent.click(screen.getByRole('button', { name: /Next Run/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Next Run' })).toBeVisible());
    expect(screen.getByRole('tab', { name: '步骤与日志' })).toHaveAttribute('aria-selected', 'true');
  });

  it('clears a run action explanation when another run is selected', async () => {
    const runs = [
      { id: 'run-a', status: 'queued', environmentId: 'environment-test', environmentName: 'Test Environment', name: 'Run A', createdAt: '2026-08-25T09:00:00Z' },
      { id: 'run-b', status: 'queued', environmentId: 'environment-test', environmentName: 'Test Environment', name: 'Run B', createdAt: '2026-08-25T08:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/runs/run-a/cancel')) return json({ error: { code: 'conflict', message: 'run is already terminal', explanation: { reasons: [{ code: 'run.already_terminal', message: '该 Run 已进入终态', cause: { kind: 'platform_rule', summary: '该 Run 已结束' }, nextAction: { label: '刷新运行详情', href: '/runs?selected=run-a' } }], primaryAction: { label: '刷新运行详情', href: '/runs?selected=run-a' }, secondaryActions: [] } } }, 409);
      if (/\/runs\/run-[ab]\/activity$/.test(url)) return json({ runId: url.split('/').at(-2), status: 'queued', nextAfterId: 0, hasMore: false, archived: false, logs: [], waitingObservations: [] });
      if (url.endsWith('/runs/run-a')) return json(runs[0]);
      if (url.endsWith('/runs/run-b')) return json(runs[1]);
      if (url.endsWith('/runs')) return json(runs);
      if (url.endsWith('/workbench')) return json({ generatedAt: '2026-08-25T10:00:00Z', role: alice.role, summary: { critical: 0, actionRequired: 0, inProgress: 2, informational: 0 }, assets: { components: 0, scenarios: 0, environments: 0 }, items: [] });
      if (url.endsWith('/components') || url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/runs?selected=run-a');
    expect(await screen.findByRole('heading', { name: 'Run A' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(await screen.findByRole('heading', { name: '运行操作被阻断' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /Run B/ }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Run B' })).toBeInTheDocument());
    expect(screen.queryByRole('heading', { name: '运行操作被阻断' })).not.toBeInTheDocument();
  });
});

describe("RunsPage", () => {
  it('opens Run history management on demand for the platform admin', async () => {
    const fetchMock = installFetch({ initialUser: admin });
    renderApp('/runs', true);
    await screen.findByRole('button', { name: '运行历史管理' });
    expect(screen.queryByRole('dialog', { name: '运行历史管理' })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/run-retention'))).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: '运行历史管理' }));
    const dialog = screen.getByRole('dialog', { name: '运行历史管理' });
    expect(dialog).toBeInTheDocument();
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/run-retention'))).toBe(true));
    await userEvent.click(within(dialog).getByRole('button', { name: '关闭' }));
    expect(screen.queryByRole('dialog', { name: '运行历史管理' })).not.toBeInTheDocument();

  });
});
