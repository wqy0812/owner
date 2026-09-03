import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppProvider, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';

interface PendingRequest {
  signal: AbortSignal;
  resolve: (value: string) => void;
  reject: (reason: unknown) => void;
}

function Query({ scope, pending, abortable = true }: { scope: string; pending: PendingRequest[]; abortable?: boolean }) {
  const { signalRefresh } = useApp();
  const { data, error, reload, loading } = useApiData((signal) => new Promise<string>((resolve, reject) => {
    pending.push({ signal, resolve, reject });
    if (abortable) signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true });
  }), [scope], 'components');
  return <><output>{data ?? 'empty'}</output><span>{error}</span><span>{loading ? 'loading' : 'idle'}</span>
    <button onClick={() => signalRefresh('components')}>refresh</button>
    <button onClick={() => signalRefresh('runs')}>unrelated</button>
    <button onClick={() => void reload()}>reload</button>
  </>;
}

afterEach(() => vi.unstubAllGlobals());

function stubSession() {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({
    data: { id: 'component-owner-a', name: 'Alice', role: 'component_owner' },
  }), { headers: { 'Content-Type': 'application/json' } }))));
}

describe('useApiData', () => {
  it('aborts the previous scoped request and never exposes its data to the next scope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { id: 'component-owner-a', name: 'Alice', role: 'component_owner' },
    }), { headers: { 'Content-Type': 'application/json' } })));
    const pending: PendingRequest[] = [];
    const view = render(<AppProvider><Query scope="alice" pending={pending} /></AppProvider>);

    await waitFor(() => expect(pending).toHaveLength(1));
    await act(async () => pending[0].resolve('alice data'));
    expect(await screen.findByText('alice data')).toBeInTheDocument();

    view.rerender(<AppProvider><Query scope="dave" pending={pending} /></AppProvider>);
    expect(screen.queryByText('alice data')).not.toBeInTheDocument();
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(pending[0].signal.aborted).toBe(true);
  });

  it('finishes slow reads and coalesces a burst of refreshes into one follow-up', async () => {
    stubSession();
    const pending: PendingRequest[] = [];
    render(<AppProvider><Query scope="first" pending={pending} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    for (let i = 0; i < 5; i++) act(() => screen.getByText('refresh').click());
    expect(pending).toHaveLength(1);
    expect(pending[0].signal.aborted).toBe(false);

    await act(async () => pending[0].resolve('first result'));
    expect(screen.getByText('first result')).toBeInTheDocument();
    expect(pending).toHaveLength(2);
    await act(async () => pending[1].resolve('fresh result'));
    expect(screen.getByText('fresh result')).toBeInTheDocument();
    expect(screen.getByText('idle')).toBeInTheDocument();
    act(() => screen.getByText('unrelated').click());
    expect(pending).toHaveLength(2);
  });

  it('discards a queued refresh when the scope changes and cancels on unmount', async () => {
    stubSession();
    const pending: PendingRequest[] = [];
    const view = render(<AppProvider><Query scope="first" pending={pending} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => screen.getByText('refresh').click());
    view.rerender(<AppProvider><Query scope="second" pending={pending} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(pending[0].signal.aborted).toBe(true);
    await act(async () => pending[1].resolve('second result'));
    expect(pending).toHaveLength(2);
    expect(screen.getByText('second result')).toBeInTheDocument();

    act(() => screen.getByText('refresh').click());
    expect(pending).toHaveLength(3);
    view.unmount();
    expect(pending[2].signal.aborted).toBe(true);
  });

  it('recovers from an error with the queued refresh and keeps explicit reload immediate', async () => {
    stubSession();
    const pending: PendingRequest[] = [];
    render(<AppProvider><Query scope="first" pending={pending} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => screen.getByText('refresh').click());
    await act(async () => pending[0].reject(new Error('temporary failure')));
    expect(pending).toHaveLength(2);
    await act(async () => pending[1].resolve('recovered result'));
    expect(screen.queryByText('temporary failure')).not.toBeInTheDocument();
    expect(screen.getByText('recovered result')).toBeInTheDocument();

    act(() => screen.getByText('refresh').click());
    act(() => screen.getByText('reload').click());
    expect(pending).toHaveLength(4);
    expect(pending[2].signal.aborted).toBe(true);
    await act(async () => pending[3].resolve('reloaded result'));
    expect(screen.getByText('reloaded result')).toBeInTheDocument();
  });

  it('ignores a late response from an old scope even when its loader ignores cancellation', async () => {
    stubSession();
    const pending: PendingRequest[] = [];
    const view = render(<AppProvider><Query scope="first" pending={pending} abortable={false} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => screen.getByText('refresh').click());
    view.rerender(<AppProvider><Query scope="second" pending={pending} abortable={false} /></AppProvider>);
    await waitFor(() => expect(pending).toHaveLength(2));
    act(() => screen.getByText('refresh').click());
    await act(async () => pending[0].resolve('late private result'));
    expect(screen.queryByText('late private result')).not.toBeInTheDocument();
    expect(pending).toHaveLength(2);
    await act(async () => pending[1].resolve('current result'));
    expect(screen.getByText('current result')).toBeInTheDocument();
    expect(pending).toHaveLength(3);
    await act(async () => pending[2].resolve('current refreshed result'));
    expect(screen.getByText('current refreshed result')).toBeInTheDocument();
  });
});
