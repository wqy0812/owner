import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { RunDiagnosticsPanel, RunLogDownload } from '../components/RunDiagnosticsPanel';
import type { Run, RunDiagnostics } from '../types/domain';

vi.mock('../context/AppContext', () => ({ useApp: () => ({ user: { id: 'owner' }, refreshTokens: {}, notify: vi.fn() }), displayError: (reason: unknown) => String(reason) }));
const run: Run = { id: 'failed-run', environmentId: 'env', status: 'failed', createdBy: 'owner' };
const diagnostics: RunDiagnostics = { runId: run.id, status: 'failed', capturedAt: '', lastLogId: 500, logCount: 500, items: [
  { stepId: 'failed-step', logId: 2, component: 'CoreDNS', phase: 'post', host: 'master-4', task: 'rollback-post', message: 'The connection to the server :6443 was refused', source: 'event', stdout: 'Traceback\nRuntimeError: connection refused', stderr: 'Shared connection closed', exitCode: 1, raw: 'original event' },
  { logId: 3, host: 'master-5', message: 'another host failed', source: 'event' },
] };
beforeEach(() => vi.restoreAllMocks());

it('shows the specific cause first and expands full diagnostics without rereading on a detail refresh', async () => {
  const load = vi.spyOn(api, 'runDiagnostics').mockResolvedValue(diagnostics);
  const { rerender } = render(<RunDiagnosticsPanel run={run} retryBusy={false} onRetryRun={vi.fn()} />);
  expect(await screen.findByText(diagnostics.items[0].message)).toBeVisible();
  expect(screen.getByText(/Traceback.*RuntimeError: connection refused/)).not.toBeVisible();
  await userEvent.click(screen.getByText('展开完整诊断'));
  expect(screen.getByText(/Traceback.*RuntimeError: connection refused/)).toBeVisible();
  expect(screen.getAllByRole('button', { name: '定位失败步骤' })).toHaveLength(2);
  rerender(<RunDiagnosticsPanel run={{ ...run }} retryBusy={false} onRetryRun={vi.fn()} />);
  expect(load).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByText('其他失败记录 · 1'));
  expect(screen.getByText('another host failed')).toBeVisible();
});
it('reports a diagnostic read failure and retries', async () => {
  vi.spyOn(api, 'runDiagnostics').mockRejectedValueOnce(new Error('日志不可读')).mockResolvedValue(diagnostics);
  render(<RunDiagnosticsPanel run={run} retryBusy={false} onRetryRun={vi.fn()} />);
  expect(await screen.findByText(/诊断日志读取失败/)).toBeVisible();
  await userEvent.click(screen.getByRole('button', { name: /重试/ }));
  expect(await screen.findByText(diagnostics.items[0].message)).toBeVisible();
});
it('aborts a pending download and clears its busy state when switching runs', async () => {
  let signal: AbortSignal | undefined;
  vi.spyOn(api, 'downloadRunLogs').mockImplementation((_id, requestSignal) => {
    signal = requestSignal;
    return new Promise(() => {});
  });
  const { rerender } = render(<RunLogDownload run={run} />);
  await userEvent.click(screen.getByRole('button', { name: '下载完整日志包' }));
  expect(screen.getByRole('button', { name: '正在生成日志包…' })).toBeDisabled();
  rerender(<RunLogDownload run={{ ...run, id: 'next-run' }} />);
  expect(signal?.aborted).toBe(true);
  expect(screen.getByRole('button', { name: '下载完整日志包' })).toBeEnabled();
});
it('downloads the server bundle, gives feedback and releases its object URL after the click', async () => {
  const create = vi.fn(() => 'blob:logs'); const revoke = vi.fn();
  vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = revoke; });
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  let finish!: (blob: Blob) => void;
  const download = vi.spyOn(api, 'downloadRunLogs').mockRejectedValueOnce(new Error('archive unreadable')).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  render(<RunLogDownload run={run} />);
  await userEvent.click(screen.getByRole('button', { name: '下载完整日志包' }));
  expect(await screen.findByText(/日志包下载失败/)).toBeVisible();
  await userEvent.click(screen.getByRole('button', { name: '重试下载完整日志包' }));
  expect(screen.getByRole('button', { name: '正在生成日志包…' })).toBeDisabled();
  vi.useFakeTimers();
  await act(async () => finish(new Blob(['server bundle'], { type: 'application/gzip' })));
  expect(click).toHaveBeenCalledTimes(1);
  expect(download).toHaveBeenCalledTimes(2); expect(revoke).not.toHaveBeenCalled();
  expect((click.mock.instances[0] as unknown as HTMLAnchorElement).download).toBe('failed-run-logs.tar.gz');
  vi.advanceTimersByTime(30000);
  expect(revoke).toHaveBeenCalledWith('blob:logs');
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
