import { useEffect, useRef, useState } from 'react';
import { AlertTriangle, Copy, Download } from 'lucide-react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { Run, RunDiagnostic } from '../types/domain';
import { ErrorBlock, formatTime } from './Primitives';
import { phaseLabel } from './JobPlanPreview';

function diagnosticText(item: RunDiagnostic) {
  return [item.component, item.phase && phaseLabel(item.phase), item.task, item.host && `主机：${item.host}`, item.message, item.exitCode !== undefined && `退出码：${item.exitCode}`, item.stdout && `stdout\n${item.stdout}`, item.stderr && `stderr\n${item.stderr}`, item.logId && `日志 #${item.logId}`].filter(Boolean).join('\n');
}
function DiagnosticDetails({ item }: { item: RunDiagnostic }) {
  return <div className="run-diagnostic__details">
    <dl>{item.task && <div><dt>任务</dt><dd>{item.task}</dd></div>}{item.phase && <div><dt>阶段</dt><dd>{phaseLabel(item.phase)}</dd></div>}{item.host && <div><dt>主机</dt><dd>{item.host}</dd></div>}{item.exitCode !== undefined && <div><dt>退出码</dt><dd>{item.exitCode}</dd></div>}</dl>
    {item.stdout && <section><strong>stdout</strong><pre>{item.stdout}</pre></section>}
    {item.stderr && <section><strong>stderr</strong><pre>{item.stderr}</pre></section>}
    {item.raw && <details><summary>原始日志{item.logId ? ` #${item.logId}` : ''}</summary><pre>{item.raw}</pre></details>}
    {item.truncated && <p>诊断片段较长，完整内容请下载日志包查看。</p>}
    {item.stepId && <button className="button button--quiet" onClick={() => document.getElementById(`run-step-${item.stepId}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })}>定位失败步骤</button>}
  </div>;
}
export function RunDiagnosticsPanel({ run, retryBusy, onRetryRun }: { run: Run; retryBusy: boolean; onRetryRun: () => void }) {
  const { user, notify } = useApp();
  const enabled = ['failed', 'interrupted', 'cancelled'].includes(run.status);
  // A terminal transition triggers one read; log SSE refreshes do not rescan
  // the complete history on every event.
  const query = useApiData(signal => enabled ? api.runDiagnostics(run.id, signal) : Promise.resolve(undefined), [user.id, run.id, run.status, run.finishedAt], []);
  if (!enabled) return null;
  if (query.error) return <section className="panel run-diagnostic-status"><ErrorBlock message={`诊断日志读取失败：${query.error}`} onRetry={() => void query.reload()} /></section>;
  if (!query.data) return <section className="panel run-diagnostic-status" role="status">正在读取关键错误…</section>;
  const [primary, ...others] = query.data.items;
  if (!primary) return null;
  return <article className="failure-summary run-diagnostic" role="alert">
    <AlertTriangle size={24} />
    <div className="run-diagnostic__body">
      <strong>{run.status === 'interrupted' ? '运行被中断' : run.status === 'cancelled' ? '运行已取消' : '关键错误'}{primary.component ? ` · ${primary.component}` : ''}</strong>
      <p className="run-diagnostic__reason">{primary.message}</p>
      <small>{primary.host ? `主机 ${primary.host} · ` : ''}{primary.task ? `${primary.task} · ` : ''}{formatTime(run.finishedAt)}</small>
      <details><summary>展开完整诊断</summary><DiagnosticDetails item={primary} /></details>
      {others.length > 0 && <details><summary>其他失败记录 · {others.length}</summary>{others.map((item, index) => <section className="run-diagnostic__other" key={`${item.logId}-${index}`}><strong>{item.host || item.component || '执行错误'}</strong><p>{item.message}</p><DiagnosticDetails item={item} /></section>)}</details>}
      {!!query.data.omitted && <p>另有 {query.data.omitted} 条错误记录，请在完整日志包中查看。</p>}
      <div className="run-diagnostic__actions">
        <button className="button button--quiet" onClick={async () => { try { await navigator.clipboard.writeText(`Run ${run.id}\n${query.data!.items.map(diagnosticText).join('\n\n')}`); notify('success', '诊断已复制'); } catch (error) { notify('error', '复制失败', displayError(error)); } }}><Copy size={14} /> 复制诊断</button>
        {primary.stepId && <button className="button button--quiet" onClick={() => document.getElementById(`run-step-${primary.stepId}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })}>定位失败步骤</button>}
        {run.createdBy === user.id && ['failed', 'interrupted'].includes(run.status) && <button className="button button--primary" disabled={retryBusy} onClick={onRetryRun}>{retryBusy ? '正在校验…' : '预览安全续跑'}</button>}
      </div>
    </div>
  </article>;
}

export function RunLogDownload({ run }: { run: Run }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const controller = useRef<AbortController>();
  const { user } = useApp();
  useEffect(() => {
    setBusy(false);
    setError(undefined);
    return () => controller.current?.abort();
  }, [run.id, user.id]);
  async function download() {
    if (busy) return;
    const request = new AbortController(); controller.current = request;
    setBusy(true); setError(undefined);
    try {
      const blob = await api.downloadRunLogs(run.id, request.signal);
      if (request.signal.aborted) return;
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url; link.download = `${run.id}-logs.tar.gz`;
      document.body.appendChild(link); link.click(); link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 30000);
    } catch (reason) { if (!request.signal.aborted) setError(displayError(reason)); }
    finally { if (!request.signal.aborted) setBusy(false); }
  }
  return <section className="panel run-log-download" aria-label="完整日志下载">
    <div><strong>完整运行日志</strong><p>{run.finishedAt ? '完整脱敏日志、执行步骤与诊断结果' : '下载截至当前时刻的脱敏日志快照，包内标明截止位置'}</p></div>
    <button className="button button--secondary" disabled={busy} onClick={() => void download()}><Download size={15} />{busy ? '正在生成日志包…' : error ? '重试下载完整日志包' : '下载完整日志包'}</button>
    {error && <p className="run-log-download__error" role="alert">日志包下载失败：{error}</p>}
  </section>;
}
