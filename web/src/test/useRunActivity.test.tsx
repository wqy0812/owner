import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { RunActivityEvents } from '../context/runActivityEvents';
import { useRunActivity } from '../hooks/useRunActivity';
import type { RunActivity } from '../types/domain';

const state = vi.hoisted(() => ({ user: { id: 'alice' }, scheduleRefresh: vi.fn(), bus: undefined as unknown as RunActivityEvents }));
vi.mock('../context/AppContext', () => ({ useApp: () => ({ user: state.user, scheduleRefresh: state.scheduleRefresh, runActivityEvents: state.bus }), displayError: (e: Error) => e.message }));
const page = (runId: string, id = 1, status: RunActivity['status'] = 'running'): RunActivity => ({ runId, status, logs: [{ id, runId, stepId: '', stream: 'stdout', message: `log ${id}`, createdAt: '' }], nextAfterId: id, hasMore: false, archived: false, waitingObservations: [] });
beforeEach(() => { vi.useFakeTimers(); state.user = { id: 'alice' }; state.bus = new RunActivityEvents(); state.scheduleRefresh.mockReset(); });
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
async function flush() { await act(async () => { await Promise.resolve(); }); }

it('routes log bursts by Run ID without refreshing global queries', async () => {
  const read = vi.spyOn(api, 'runActivity').mockImplementation(async id => page(id));
  const a = renderHook(() => useRunActivity('a', 'running'));
  renderHook(() => useRunActivity('b', 'running')); await flush(); read.mockClear();
  act(() => { for (let i = 0; i < 30; i++) state.bus.emit('a', { type: 'log', logId: 2 }); });
  await act(() => vi.advanceTimersByTimeAsync(400));
  expect(read).toHaveBeenCalledTimes(1); expect(read.mock.calls[0].slice(0, 2)).toEqual(['a', 1]);
  expect(state.scheduleRefresh).not.toHaveBeenCalled(); expect(a.result.current.data?.logs).toHaveLength(1);
});

it('coalesces in-flight events and pulls every cursor page before terminal reconciliation', async () => {
  let finish!: (v: RunActivity) => void;
  const read = vi.spyOn(api, 'runActivity').mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }))
    .mockResolvedValueOnce({ ...page('a', 2), hasMore: true }).mockResolvedValue(page('a', 3, 'succeeded'));
  const view = renderHook(() => useRunActivity('a', 'running'));
  act(() => { state.bus.emit('a', { type: 'log', logId: 2 }); state.bus.emit('a', { type: 'state' }); });
  expect(read).toHaveBeenCalledTimes(1);
  await act(async () => finish(page('a'))); await act(() => vi.advanceTimersByTimeAsync(400));
  expect(read.mock.calls.map(call => call[1])).toEqual([undefined, 1, 2]);
  expect(view.result.current.data?.logs.map(log => log.id)).toEqual([1, 2, 3]);
  expect(state.scheduleRefresh).toHaveBeenCalledTimes(1);
});

it('retains observations and cursor on failure, then resumes on reconnect', async () => {
  const first = { ...page('a'), waitingObservations: [{ stepId: 's', host: 'h', task: 'ready', waiting: { observed: 'Pending' } }] };
  const read = vi.spyOn(api, 'runActivity').mockResolvedValueOnce(first).mockRejectedValueOnce(new Error('offline')).mockResolvedValue(page('a', 2));
  const view = renderHook(() => useRunActivity('a', 'running')); await flush();
  act(() => state.bus.refresh()); await flush();
  expect(view.result.current.error).toBe('offline'); expect(view.result.current.data?.waitingObservations).toEqual(first.waitingObservations);
  act(() => state.bus.refresh()); await flush();
  expect(read.mock.calls.map(call => call[1])).toEqual([undefined, 1, 1]);
  expect(view.result.current.error).toBeUndefined(); expect(view.result.current.data?.logs).toHaveLength(2);
});

it('aborts old Run and identity requests and ignores their late responses', async () => {
  let finish!: (v: RunActivity) => void;
  const read = vi.spyOn(api, 'runActivity').mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockResolvedValue(page('b'));
  const view = renderHook(({ id }) => useRunActivity(id, 'running'), { initialProps: { id: 'a' } });
  const oldSignal = read.mock.calls[0][2]!;
  view.rerender({ id: 'b' }); await flush(); expect(oldSignal.aborted).toBe(true);
  await act(async () => finish(page('a', 99))); expect(view.result.current.data?.runId).toBe('b');
  state.user = { id: 'bob' }; state.bus = new RunActivityEvents(); view.rerender({ id: 'b' }); await flush();
  expect(read.mock.calls.at(-1)?.[1]).toBeUndefined();
});

it('repairs a lost terminal event by visible polling and bounds the log buffer', async () => {
  const many = { ...page('a', 2000), logs: Array.from({ length: 2000 }, (_, i) => page('a', i + 1).logs[0]) };
  const read = vi.spyOn(api, 'runActivity').mockResolvedValueOnce(many).mockResolvedValue(page('a', 2001, 'succeeded'));
  const view = renderHook(() => useRunActivity('a', 'running')); await flush();
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(read).toHaveBeenCalledTimes(2); expect(view.result.current.data?.status).toBe('succeeded');
  expect(view.result.current.data?.logs).toHaveLength(2000); expect(view.result.current.data?.logs[0].id).toBe(2);
  expect(state.scheduleRefresh).toHaveBeenCalledTimes(1);
});

it('pauses hidden page reads and catches up immediately when visibility is restored', async () => {
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
  const read = vi.spyOn(api, 'runActivity').mockResolvedValueOnce(page('a')).mockResolvedValue(page('a', 2));
  const view = renderHook(() => useRunActivity('a', 'running')); await flush();
  hidden.mockReturnValue(true);
  act(() => state.bus.emit('a', { type: 'log', logId: 2 }));
  await act(() => vi.advanceTimersByTimeAsync(30000));
  expect(read).toHaveBeenCalledTimes(1);
  hidden.mockReturnValue(false); act(() => state.bus.refresh()); await flush();
  expect(read.mock.calls.at(-1)?.[1]).toBe(1); expect(view.result.current.data?.nextAfterId).toBe(2);
});
