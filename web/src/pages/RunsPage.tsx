import { useEffect, useMemo, useState } from 'react';
import { Ban, Check, CheckCircle2, ChevronRight, CircleDashed, Clock3, ListFilter, PlayCircle, ScrollText, ShieldAlert, Square, X } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { Run } from '../types/domain';

const ACTIVE = new Set(['queued', 'awaiting_approval', 'running']);

export function RunsPage() {
  const { user, notify, refreshTokens, signalRefresh } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: runs, loading, error, isRefreshing, reload } = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const [filter, setFilter] = useState<'all' | 'active' | 'finished'>('all');
  const [detail, setDetail] = useState<Run>();
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string>();
  const [detailRetry, setDetailRetry] = useState(0);
  const [busy, setBusy] = useState<string>();
  const selectedId = searchParams.get('selected') ?? runs?.[0]?.id;

  useEffect(() => {
    if (!selectedId) { setDetail(undefined); setDetailError(undefined); return; }
    const controller = new AbortController();
    setDetailLoading(true);
    setDetailError(undefined);
    api.run(selectedId, controller.signal).then((run) => {
      if (!controller.signal.aborted) setDetail(run);
    }).catch((reason) => {
      if (!controller.signal.aborted) {
        setDetail(undefined);
        setDetailError(displayError(reason));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setDetailLoading(false);
    });
    return () => controller.abort();
  }, [detailRetry, refreshTokens.runs, selectedId]);

  const filtered = useMemo(() => (runs ?? []).filter((run) => filter === 'all' || (filter === 'active' ? ACTIVE.has(run.status) : !ACTIVE.has(run.status))), [filter, runs]);

  async function action(kind: 'cancel' | 'approve' | 'reject') {
    if (!detail) return; setBusy(kind);
    try {
      if (kind === 'cancel') await api.cancelRun(detail.id);
      else if (detail.approval?.id) await (kind === 'approve' ? api.approve(detail.approval.id) : api.reject(detail.approval.id));
      notify('success', kind === 'approve' ? '已批准执行' : kind === 'reject' ? '已拒绝执行' : '取消请求已发送'); signalRefresh(['runs', 'environments', 'scenarios']);
    } catch (reason) { notify('error', '操作失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  const progress = detail?.progress ?? (detail?.status === 'succeeded' ? 100 : detail?.steps?.length ? Math.round(detail.steps.filter((step) => step.status === 'succeeded').length / detail.steps.length * 100) : 0);

  return <div className="page">
    <PageHeader eyebrow="Ansible executions" title="运行中心" description="跟踪排队、审批、执行步骤和实时脱敏日志；每个环境按 FIFO 串行执行。" />
    <RefreshNotice loading={isRefreshing} error={runs ? error : undefined} onRetry={() => void reload()} />
    {loading && !runs ? <LoadingBlock label="正在加载运行队列…" /> : error && !runs ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="runs-layout">
      <aside className="run-sidebar panel">
        <div className="run-filter"><ListFilter size={16} />{(['all', 'active', 'finished'] as const).map((value) => <button key={value} className={filter === value ? 'active' : ''} onClick={() => setFilter(value)}>{value === 'all' ? '全部' : value === 'active' ? '进行中' : '已结束'}</button>)}</div>
        <div className="run-cards">{filtered.map((run) => <button key={run.id} className={`run-card${selectedId === run.id ? ' active' : ''}`} onClick={() => setSearchParams({ selected: run.id })}><span className={`run-card__status run-card__status--${run.status}`}>{run.status === 'succeeded' ? <CheckCircle2 size={17} /> : run.status === 'awaiting_approval' ? <ShieldAlert size={17} /> : <CircleDashed size={17} />}</span><div><strong>{run.name ?? run.scenarioName ?? run.componentName ?? `Run ${run.id.slice(0, 8)}`}</strong><small>{run.environmentName ?? '未知环境'} · {formatTime(run.createdAt)}</small><span><StatusPill status={run.status} />{run.queuePosition ? <em>队列 #{run.queuePosition}</em> : null}</span></div><ChevronRight size={16} /></button>)}</div>
        {!filtered.length && <EmptyState title="当前筛选无运行" />}
      </aside>
      {detailLoading && !detail ? <section className="panel"><LoadingBlock label="正在加载运行详情…" /></section> : detailError ? <section className="panel"><ErrorBlock message={detailError} onRetry={() => setDetailRetry((value) => value + 1)} /></section> : detail ? <section className="run-detail detail-stack">
        <article className="panel run-hero">
          <div><div className={`run-symbol run-symbol--${detail.status}`}><PlayCircle size={23} /></div><div><div className="eyebrow">{detail.kind?.replaceAll('_', ' ') ?? 'scenario run'} · {detail.id.slice(0, 12)}</div><h2>{detail.name ?? detail.scenarioName ?? detail.componentName}</h2><p>{detail.environmentName} · 发起人 {detail.createdByName ?? detail.createdBy ?? '—'} · {formatTime(detail.createdAt)}</p></div></div>
          <div className="run-actions"><StatusPill status={detail.status} />{ACTIVE.has(detail.status) && detail.status !== 'awaiting_approval' && <button disabled={busy === 'cancel'} className="button button--danger-soft" onClick={() => void action('cancel')}><Square size={14} /> 取消</button>}</div>
          <div className="progress-track"><span style={{ width: `${progress}%` }} /><small>{progress}%</small></div>
        </article>
        {detail.status === 'awaiting_approval' && <article className="approval-banner"><ShieldAlert size={24} /><div><strong>危险作业等待环境 Owner 审批</strong><p>{detail.approval?.riskReason ?? '动作包含 recovery / clean / destroy / uninstall，可能改变或删除目标环境数据。'}</p></div>{user.role === 'environment_owner' ? <div><button disabled={Boolean(busy)} className="button button--quiet" onClick={() => void action('reject')}><X size={15} /> 拒绝</button><button disabled={Boolean(busy)} className="button button--primary" onClick={() => void action('approve')}><Check size={15} /> 批准执行</button></div> : <span>仅环境 Owner 可审批</span>}</article>}
        <article className="panel"><header className="panel__header"><div><span className="panel__icon"><Clock3 size={18} /></span><div><h2>执行步骤</h2><p>Preflight → syntax-check → list-hosts → execute → verify</p></div></div></header>{detail.steps?.length ? <div className="step-timeline">{detail.steps.map((step, index) => <div key={step.id} className={`step step--${step.status}`}><span className="step__index">{step.status === 'succeeded' ? <Check size={14} /> : step.status === 'failed' ? <X size={14} /> : index + 1}</span><span className="step__line" /><div><div><strong>{step.name}</strong><StatusPill status={step.status} /></div><p>{step.componentName ? `${step.componentName} · ` : ''}{step.action ?? ''}{step.summary ? ` · ${step.summary}` : ''}</p><small>{formatTime(step.startedAt)}{step.finishedAt ? ` → ${formatTime(step.finishedAt)}` : ''}</small></div></div>)}</div> : <EmptyState title="步骤尚未生成" description={detail.status === 'awaiting_approval' ? '审批通过后进入环境队列。' : 'Planner 正在生成执行步骤。'} />}</article>
        <article className="panel log-panel"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><ScrollText size={18} /></span><div><h2>实时日志</h2><p>stdout / stderr · 凭据自动脱敏</p></div></div><span className="live-badge"><span /> LIVE</span></header><pre>{detail.logTail?.length ? detail.logTail.join('\n') : `[platform] Run ${detail.id}\n[platform] status=${detail.status}\n[platform] 等待 Ansible 输出…`}</pre></article>
      </section> : <section className="panel"><EmptyState title="选择一个运行" description="查看步骤、审批和日志详情。" /></section>}
    </div>}
  </div>;
}
