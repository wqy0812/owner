import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, expect, it, vi } from 'vitest';
import { UIProvider } from '../components/UIProvider';
import { AppProvider, useApp } from '../context/AppContext';
import { admin, installFetch, json, platformOptionCategories } from './fixtures/appFixtures';
import { EventSourceMock } from './setup';
import { deferred } from './testLifecycle';

beforeEach(() => { vi.useFakeTimers(); installFetch({ initialUser: admin }); });
const wrapper = ({ children }: { children: ReactNode }) => <UIProvider><AppProvider>{children}</AppProvider></UIProvider>;
async function flush() { await act(() => vi.advanceTimersByTimeAsync(0)); }

it('keeps loaded platform options while mutation and SSE refreshes coalesce', async () => {
  const fallback = globalThis.fetch;
  let refresh!: ReturnType<typeof deferred<Response>>;
  let directoryReads = 0;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith('/platform-option-categories')) {
      if (++directoryReads === 1) return json(platformOptionCategories);
      refresh = deferred<Response>(init?.signal);
      return refresh.promise;
    }
    return fallback(input, init);
  }));
  const { result } = renderHook(() => useApp(), { wrapper });
  await flush();
  expect(result.current.platformOptionCategories).toHaveLength(7);
  act(() => {
    result.current.scheduleRefresh('platform-options');
    result.current.scheduleRefresh('platform-options');
    EventSourceMock.instances.at(-1)!.emit('platform_options.updated');
  });
  await act(() => vi.advanceTimersByTimeAsync(399));
  expect(directoryReads).toBe(1);
  await act(() => vi.advanceTimersByTimeAsync(1));
  expect(directoryReads).toBe(2);
  expect(result.current.platformOptionCategories[0].label).toBe('架构');
  expect(result.current.platformOptionsLoading).toBe(false);
  await act(() => vi.advanceTimersByTimeAsync(500));
  expect(directoryReads).toBe(2);
  await act(async () => refresh.resolve(await json(platformOptionCategories)));
  expect(result.current.platformOptionsError).toBeUndefined();
});

it('does not reload scenario component metadata for a run log event', async () => {
  const { result } = renderHook(() => useApp(), { wrapper });
  await flush();
  const before = { ...result.current.refreshTokens };
  act(() => EventSourceMock.instances.at(-1)!.emit('run.log', { runId: 'parallel-run', logId: 123 }));
  await act(() => vi.advanceTimersByTimeAsync(1000));
  expect(result.current.refreshTokens).toEqual(before);
});

it('aligns business state once when the SSE connection reopens', async () => {
  const { result, unmount } = renderHook(() => useApp(), { wrapper });
  await flush();
  const source = EventSourceMock.instances.at(-1)!;
  const before = result.current.refreshTokens.components;
  act(() => source.onopen?.(new Event('open')));
  await act(() => vi.advanceTimersByTimeAsync(400));
  expect(result.current.refreshTokens.components).toBe(before);
  act(() => { source.onerror?.(new Event('error')); source.onopen?.(new Event('open')); });
  await act(() => vi.advanceTimersByTimeAsync(400));
  expect(result.current.refreshTokens.components).toBe(before + 1);
  act(() => source.emit('release.published'));
  unmount();
  expect(source.readyState).toBe(EventSourceMock.CLOSED);
  expect(vi.getTimerCount()).toBe(0);
});
