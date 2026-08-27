import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Check, CheckCircle2, ChevronRight, CircleDashed, Clock3, Copy, Download, ListFilter, PlayCircle, ScrollText, Search, ShieldAlert, Square, X } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { Run, WorkExplanation } from '../types/domain';

const ACTIVE = new Set(['queued', 'awaiting_approval', 'running']);

export function runFailureSummary(run: Run): { title: string; detail: string; stepId?: string; host?: string } | undefined {
  if (!['failed', 'interrupted'].includes(run.status)) return undefined;
  const failed = run.steps?.find((step) => step.status === 'failed');
  const logLines = (run.logTail ?? []).flatMap((entry) => entry.split('\n'));
  const diagnostic = [...logLines].reverse().find((line) => /fatal:|FAILED!|unreachable|non-zero return code/i.test(line));
  const cleaned = diagnostic?.replace(/^\[(?:stdout|stderr|system)\]\s*/i, '').trim();
  const host = cleaned?.match(/(?:fatal|unreachable):\s*\[([^\]]+)\]/i)?.[1];
  return {
    title: failed ? `${failed.componentName ?? failed.name} · ${failed.action ?? '执行'}失败` : run.status === 'interrupted' ? '运行被中断' : '运行失败',
    detail: (cleaned || failed?.summary || '未返回结构化错误，请查看历史日志。').slice(0, 360),
    stepId: failed?.id,
    host,
  };
}

export function RunsPage() {
  const { user, notify, refreshTokens, signalRefresh } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: runs, loading, error, isRefreshing, reload } = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench');
  const [filter, setFilter] = useState<'all' | 'active' | 'finished'>('all');
  const [detail, setDetail] = useState<Run>();
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string>();
  const [detailRetry, setDetailRetry] = useState(0);
  const [busy, setBusy] = useState<string>();
  const [actionExplanation, setActionExplanation] = useState<WorkExplanation>();
  const [batchOpen, setBatchOpen] = useState(false);
  const [batchReason, setBatchReason] = useState('同一维护窗口批量批准，按环境 FIFO 串行执行');
  const [logQuery, setLogQuery] = useState('');
  const [logStream, setLogStream] = useState<'all' | 'stdout' | 'stderr' | 'system'>('all');
  const actionInFlight = useRef(false);
  const logRef = useRef<HTMLPreElement>(null);
  const selectedId = searchParams.get('selected') ?? runs?.[0]?.id;

  useEffect(() => {
    setActionExplanation(undefined);
  }, [selectedId]);

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

  const selectedSummary = runs?.find((run) => run.id === selectedId);
  const runWorkItem = workbench?.items.find((item) => item.subject.type === 'run' && item.subject.id === selectedId);
  useEffect(() => {
    if (!selectedSummary) return;
    setDetail((current) => current?.id === selectedSummary.id ? { ...current, ...selectedSummary } : current);
  }, [selectedSummary]);

  const filtered = useMemo(() => (runs ?? []).filter((run) => filter === 'all' || (filter === 'active' ? ACTIVE.has(run.status) : !ACTIVE.has(run.status))), [filter, runs]);
  const pendingApprovals = useMemo(() => (runs ?? []).filter((run) => run.status === 'awaiting_approval' && run.approval?.id), [runs]);

  async function action(kind: 'cancel' | 'approve' | 'reject') {
    if (!detail || actionInFlight.current) return;
    actionInFlight.current = true;
    setBusy(kind);
    setActionExplanation(undefined);
    try {
      let updated: Run | undefined;
      if (kind === 'cancel') updated = await api.cancelRun(detail.id);
      else if (detail.approval?.id) updated = await (kind === 'approve' ? api.approve(detail.approval.id) : api.reject(detail.approval.id));
      if (!updated) throw new Error('审批记录已失效，请刷新后重试。');
      setDetail(updated);
      notify('success', kind === 'approve' ? '已批准执行' : kind === 'reject' ? '已拒绝执行' : '取消请求已发送'); signalRefresh(['runs', 'environments', 'scenarios', 'workbench']);
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '操作失败', displayError(reason)); } finally { actionInFlight.current = false; setBusy(undefined); }
  }

  async function approveBatch() {
    if (!pendingApprovals.length || !batchReason.trim()) return;
    setBusy('batch-approve');
    try {
      await api.batchDecideApprovals(pendingApprovals.map((run) => run.approval!.id), 'approved', batchReason.trim());
      setBatchOpen(false);
      notify('success', `已批量批准 ${pendingApprovals.length} 个运行`, '运行已按各环境 FIFO 队列排队，不会并发占用同一环境。');
      signalRefresh(['runs', 'environments', 'scenarios', 'workbench']);
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '批量审批失败', displayError(reason)); }
    finally { setBusy(undefined); }
  }

  async function retrySafeRange() {
    if (!detail) return;
    setBusy('retry');
    try {
      const plan = await api.previewRunRetry(detail.id);
      const remaining = plan.remainingSteps.map((step) => `${step.order}. ${step.componentName} · ${step.action}`).join('\n');
      if (!window.confirm(`安全续跑预览\n跳过 ${plan.skippedSteps} 个已成功步骤\n继续 ${plan.remainingSteps.length} 个步骤${plan.requiresApproval ? '\n提交后需要重新审批' : ''}\n\n${remaining}\n\n确认创建关联 Run？`)) return;
      const created = await api.retryRun(detail.id, plan.planDigest);
      notify('success', '安全续跑 Run 已创建', `原 Run 保持不变，新 Run 从第 ${plan.startStep + 1} 步继续。`);
      signalRefresh(['runs', 'workbench', 'environments', 'scenarios']);
      setSearchParams({ selected: created.id });
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '无法安全续跑', displayError(reason)); }
    finally { setBusy(undefined); }
  }

  const progress = detail?.progress ?? (detail?.status === 'succeeded' ? 100 : detail?.steps?.length ? Math.round(detail.steps.filter((step) => step.status === 'succeeded').length / detail.steps.length * 100) : 0);
  const failure = detail ? runFailureSummary(detail) : undefined;
  const succeededSteps = detail?.steps?.filter((step) => step.status === 'succeeded').length ?? 0;
  const progressLabel = detail && !ACTIVE.has(detail.status) && detail.status !== 'succeeded' && detail.steps?.length ? `${succeededSteps}/${detail.steps.length} 步完成` : `${progress}%`;
  const allLogLines = useMemo(() => (detail?.logTail ?? []).flatMap((entry) => entry.split('\n')), [detail?.logTail]);
  const visibleLogLines = useMemo(() => allLogLines.filter((line) => {
    const streamMatches = logStream === 'all' || line.toLowerCase().startsWith(`[${logStream}]`);
    return streamMatches && (!logQuery.trim() || line.toLowerCase().includes(logQuery.trim().toLowerCase()));
  }), [allLogLines, logQuery, logStream]);

  useEffect(() => {
    if (detail && ACTIVE.has(detail.status) && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [detail, visibleLogLines.length]);

  function downloadLogs() {
    if (!detail) return;
    const url = URL.createObjectURL(new Blob([allLogLines.join('\n')], { type: 'text/plain;charset=utf-8' }));
    const anchor = document.createElement('a');
    anchor.href = url; anchor.download = `${detail.id}.log`; anchor.click();
    URL.revokeObjectURL(url);
  }

  return <div className="page">
    <PageHeader eyebrow="Ansible executions" title="运行中心" description="跟踪排队、审批、执行步骤和实时脱敏日志；每个环境按 FIFO 串行执行。" actions={user.role === 'environment_owner' && pendingApprovals.length ? <button className="button button--primary" onClick={() => setBatchOpen(true)}><ShieldAlert size={16} /> 批量审批 {pendingApprovals.length}</button> : undefined} />
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
          <div className="progress-track"><span style={{ width: `${progress}%` }} /><small>{progressLabel}</small></div>
        </article>
        <StatusExplanationPanel item={runWorkItem} />
        <StatusExplanationPanel explanation={actionExplanation} title="运行操作被阻断" />
        {failure && <article className="failure-summary" role="alert"><AlertTriangle size={24} /><div><strong>{failure.title}</strong><p>{failure.detail}</p><small>{failure.host ? `失败主机：${failure.host} · ` : ''}完成于 {formatTime(detail.finishedAt)}{detail.retryOfRunId ? ` · 续跑 #${detail.retryAttempt ?? 1}` : ''}</small></div><div><button className="button button--quiet" onClick={() => void navigator.clipboard?.writeText(`${failure.title}\n${failure.detail}\nRun ${detail.id}`)}><Copy size={14} /> 复制诊断</button>{failure.stepId && <button className="button button--danger-soft" onClick={() => document.getElementById(`run-step-${failure.stepId}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })}>定位失败步骤</button>}{detail.createdBy === user.id && ['failed', 'interrupted'].includes(detail.status) && detail.kind !== 'environment_rollback' && <button className="button button--primary" disabled={busy === 'retry'} onClick={() => void retrySafeRange()}>{busy === 'retry' ? '正在校验…' : '预览安全续跑'}</button>}</div></article>}
        {detail.status === 'awaiting_approval' && <article className="approval-banner"><ShieldAlert size={24} /><div><strong>危险作业等待环境 Owner 审批</strong><p>{detail.approval?.riskReason ?? '动作包含 recovery / clean / destroy / uninstall，可能改变或删除目标环境数据。'}</p></div>{user.role === 'environment_owner' ? <div><button disabled={Boolean(busy)} className="button button--quiet" onClick={() => void action('reject')}><X size={15} /> 拒绝</button><button disabled={Boolean(busy)} className="button button--primary" onClick={() => void action('approve')}><Check size={15} /> 批准执行</button></div> : <span>仅环境 Owner 可审批</span>}</article>}
        {(detail.artifactTransfers?.length || detail.imageTransfers?.length) ? <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><ShieldAlert size={18} /></span><div><h2>跨仓库平移</h2><p>以下内容属于本次审批范围；目标已存在相同指纹时不会重复传输。</p></div></div></header><div className="backup-list">{detail.imageTransfers?.map((item) => <section key={item.targetDigest}><strong>镜像：{item.sourceRegistry} → {item.targetRegistry}</strong><p>{item.targetDigest}</p></section>)}{detail.artifactTransfers?.map((item) => <section key={`${item.targetStation}-${item.relativePath}`}><strong>介质 {item.alias}：{item.sourceStation} → {item.targetStation}</strong><p>{item.relativePath}</p><small>sha256:{item.sha256}</small></section>)}</div></article> : null}
        <article className="panel"><header className="panel__header"><div><span className="panel__icon"><Clock3 size={18} /></span><div><h2>执行步骤</h2><p>Preflight → syntax-check → list-hosts → execute → verify</p></div></div></header>{detail.steps?.length ? <div className="step-timeline">{detail.steps.map((step, index) => <div id={`run-step-${step.id}`} key={step.id} className={`step step--${step.status}`}><span className="step__index">{step.status === 'succeeded' ? <Check size={14} /> : step.status === 'failed' ? <X size={14} /> : index + 1}</span><span className="step__line" /><div><div><strong>{step.name}</strong><StatusPill status={step.status} /></div><p>{step.componentName ? `${step.componentName} · ` : ''}{step.action ?? ''}{step.summary ? ` · ${step.summary}` : ''}</p><small>{formatTime(step.startedAt)}{step.finishedAt ? ` → ${formatTime(step.finishedAt)}` : ''}</small></div></div>)}</div> : <EmptyState title="步骤尚未生成" description={detail.status === 'awaiting_approval' ? '审批通过后进入环境队列。' : 'Planner 正在生成执行步骤。'} />}</article>
        {detail.backups?.length ? <article className="panel"><header className="panel__header"><div><span className="panel__icon"><ShieldAlert size={18} /></span><div><h2>备份基线</h2><p>每个引用只绑定到创建它的安装 Run；回滚不会按版本猜测目录。</p></div></div></header><div className="backup-list">{detail.backups.map((backup) => <section key={`${backup.nodeId ?? backup.componentId}-${backup.backupRef}`}><div><strong>{backup.componentName ?? backup.componentId} · {backup.action}</strong><StatusPill status={backup.action === 'rollback' ? 'rollback' : 'captured'}>{backup.action === 'rollback' ? '用于回滚' : '已锁定'}</StatusPill></div><p>{backup.backupRef}</p><small>来自 Run {backup.installRunId} · 捕获于 {formatTime(backup.capturedAt)} · Playbook {backup.playbookSha256.slice(0, 16)}…</small></section>)}</div></article> : null}
        {detail.resolvedParametersByNode && Object.keys(detail.resolvedParametersByNode).length > 0 && <article className="panel"><header className="panel__header"><div><span className="panel__icon"><ListFilter size={18} /></span><div><h2>解析参数</h2><p>下游参数值及其上游来源，不包含 CredentialRef 实际值</p></div></div></header><div className="resolved-parameters">{Object.entries(detail.resolvedParametersByNode).map(([nodeId, parameters]) => <section key={nodeId}><strong>{nodeId}</strong>{Object.entries(parameters).map(([name, item]) => <div key={name} className="parameter-preview__item"><span>{name}</span><small>{item.value === undefined ? '（空）' : String(item.value)}</small>{item.upstreamParameter ? <small className="parameter-lineage">{item.targetParameter ?? name} 来自节点 {item.sourceNodeId ?? '上游'} 的公开参数 {item.upstreamParameter}</small> : <small>{item.source ?? 'local'}{item.sourceNodeId ? ` · ${item.sourceNodeId}` : ''}</small>}</div>)}</section>)}</div></article>}
        <article className="panel log-panel"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><ScrollText size={18} /></span><div><h2>{ACTIVE.has(detail.status) ? '实时日志' : '历史日志'}</h2><p>stdout / stderr / system · 凭据自动脱敏</p></div></div><span className={`live-badge${ACTIVE.has(detail.status) ? '' : ' live-badge--history'}`}><span /> {ACTIVE.has(detail.status) ? 'LIVE' : 'ARCHIVED'}</span></header><div className="log-toolbar"><label><Search size={14} /><input aria-label="搜索运行日志" value={logQuery} onChange={(event) => setLogQuery(event.target.value)} placeholder="搜索主机、任务或错误" /></label><select aria-label="日志流筛选" value={logStream} onChange={(event) => setLogStream(event.target.value as typeof logStream)}><option value="all">全部流</option><option value="stdout">stdout</option><option value="stderr">stderr</option><option value="system">system</option></select><span>{visibleLogLines.length}/{allLogLines.length} 行</span><button className="icon-text" onClick={() => void navigator.clipboard?.writeText(visibleLogLines.join('\n'))}><Copy size={14} /> 复制结果</button><button className="icon-text" onClick={downloadLogs}><Download size={14} /> 下载完整日志</button></div><pre ref={logRef}>{allLogLines.length ? visibleLogLines.join('\n') || '没有匹配的日志行。' : `[platform] Run ${detail.id}\n[platform] status=${detail.status}\n[platform] 等待 Ansible 输出…`}</pre></article>
      </section> : <section className="panel"><EmptyState title="选择一个运行" description="查看步骤、审批和日志详情。" /></section>}
    </div>}
    {batchOpen && <Modal title={`批量批准 ${pendingApprovals.length} 个危险运行`} description="平台会先原子消费整批审批，再按每个环境的 FIFO 队列串行执行；任何一项已失效都会整批拒绝。" onClose={() => setBatchOpen(false)}><div className="modal-body"><div className="batch-approval-list">{pendingApprovals.map((run) => <div key={run.id}><strong>{run.name ?? run.scenarioName ?? run.componentName}</strong><span>{run.environmentName} · {run.approval?.riskReason}</span></div>)}</div><label><span>审批理由</span><textarea rows={3} value={batchReason} onChange={(event) => setBatchReason(event.target.value)} /></label></div><footer className="modal-actions"><button className="button button--quiet" onClick={() => setBatchOpen(false)}>取消</button><button className="button button--primary" disabled={busy === 'batch-approve' || !batchReason.trim()} onClick={() => void approveBatch()}><Check size={15} /> {busy === 'batch-approve' ? '批准中…' : '确认批量批准'}</button></footer></Modal>}
  </div>;
}
