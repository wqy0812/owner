import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppProvider } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';

interface PendingRequest {
  signal: AbortSignal;
  resolve: (value: string) => void;
  reject: (reason: unknown) => void;
}

function Query({ scope, pending }: { scope: string; pending: PendingRequest[] }) {
  const { data } = useApiData((signal) => new Promise<string>((resolve, reject) => {
    pending.push({ signal, resolve, reject });
    signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true });
  }), [scope], 'components');
  return <output>{data ?? 'empty'}</output>;
}

afterEach(() => vi.unstubAllGlobals());

describe('useApiData', () => {
  it('aborts the previous scoped request and never exposes its data to the next scope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { id: 'component-alice', name: 'Alice', role: 'component_owner' },
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
});
