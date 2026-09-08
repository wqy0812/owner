import { beforeEach, describe, expect, it, vi } from 'vitest';
import { user as userEvent } from './interactions';
import { act, screen } from './render';
import { pendingResponse } from './testLifecycle';

import { ComponentsPage } from '../pages/ComponentsPage';
import { alice, components, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(ComponentsPage, '/components');

beforeEach(() => { installFetch(); });
const summary = { id: 'component-containerd', name: 'containerd', slug: 'containerd', ownerId: alice.id, ownerName: alice.name, description: 'runtime', layer: 'runtime_state', tags: ['runtime'], releaseCount: 1, hasDraft: false, needsAttention: false };

describe("scoped lightweight read models", () => {
  it('keeps the directory on detail failure and retries without full runs, Workbench or catalog contracts', async () => {
    const fallback = installFetch(); let fail = true;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/v1/components') return json([summary]);
      if (url === '/api/v1/components/component-containerd') return fail ? json({ error: { code: 'unavailable', message: '详情暂时不可用' } }, 503) : json({ ...components[0], readContext: { evidence: {}, workItems: [], parameterConsumers: [] } });
      return fallback(input, init);
    }); vi.stubGlobal('fetch', fetchMock);
    renderApp('/components', true);
    expect(await screen.findByText('详情暂时不可用')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /containerd/ })).toBeInTheDocument();
    fail = false; await userEvent.click(screen.getByRole('button', { name: /重试/ }));
    expect(await screen.findByRole('heading', { name: 'containerd', level: 2 })).toBeInTheDocument();
    expect(fetchMock.mock.calls.map(([input]) => String(input)).filter((url) => url === '/api/v1/workbench' || url.startsWith('/api/v1/runs') || url.includes('view=contracts'))).toEqual([]);
  });

  it('aborts an obsolete component detail and ignores its late response', async () => {
    const fallback = installFetch(); let obsoleteSignal: AbortSignal | undefined; let finishOld!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/v1/components') return json([summary, { ...summary, id: 'component-second', name: 'Second runtime', slug: 'second' }]);
      if (url === '/api/v1/components/component-containerd') { obsoleteSignal = init?.signal ?? undefined; return pendingResponse((resolve) => { finishOld = resolve; }, init?.signal, { ignoreAbort: true }); }
      if (url === '/api/v1/components/component-second') return json({ ...components[0], id: 'component-second', name: 'Second runtime', releases: [], readContext: { evidence: {}, workItems: [], parameterConsumers: [] } });
      return fallback(input, init);
    }));
    renderApp('/components', true);
    await userEvent.click(await screen.findByRole('button', { name: /Second runtime/ }));
    expect(await screen.findByRole('heading', { name: 'Second runtime', level: 2 })).toBeInTheDocument();
    expect(obsoleteSignal?.aborted).toBe(true);
    await act(async () => { finishOld(await json({ ...components[0], readContext: { evidence: {}, workItems: [], parameterConsumers: [] } })); });
    expect(screen.queryByRole('heading', { name: 'containerd', level: 2 })).not.toBeInTheDocument();
  });
});
