import { activeWorkbench, activeRun } from '../hooks/activeWork';
import { RunDiagnosticsPanel, RunLogDownload } from '../components/RunDiagnosticsPanel';
import {HostGroupName} from '../components/HostGroupName';
import { phaseLabel } from '../components/JobPlanPreview';
import { RunCleanupModal } from '../components/RunCleanupModal';
import { RunRetentionPanel } from '../components/RunRetentionPanel';
import { RunDeliveryPanel } from '../components/RunDeliveryPanel';
import { RunWaitingPanel } from '../components/RunWaitingPanel';
import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { Archive, Check, CheckCircle2, ChevronRight, CircleDashed, Clock3, Copy, Download, History, ListFilter, PlayCircle, ScrollText, Search, ShieldAlert, Square, Trash2, X } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime, formatFileSize } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { displayError, useApp } from '../context/AppContext';
import { useRunActivity } from '../hooks/useRunActivity';
import { useApiData } from '../hooks/useApiData';
import type { Run, WorkExplanation } from '../types/domain';
import type { RunSummary } from '../types/domain';

const ACTIVE = new Set(['queued', 'awaiting_approval', 'running']);

export function scenarioRunModeLabel(mode: Run['executionMode'], kind?: Run['kind']) {
  const label = mode === 'upgrade' ? '场景升级' : mode === 'baseline_verify' ? '恢复后基线复核' : mode === 'install' ? '场景安装' : '';
  return label && kind === 'scenario_test' ? `${label}测试` : label;
}
export function scenarioStepLabel(step: NonNullable<Run['steps']>[number]) {
  if (step.sourceType === 'scenario_acceptance') return '场景业务验收';
  const stage = step.stage === 'source_verify' ? '来源组件验证' : step.stage === 'target_verify' ? '目标集群验证' : step.stage === 'change' ? '组件变更' : '';
  return [stage, phaseLabel(step.phase)].filter(Boolean).join(' · ');
}

export function RunsPage() {
  const { user, notify, signalRefresh } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const rawPage = Number(searchParams.get('page') ?? 1);
  const page = Number.isSafeInteger(rawPage) && rawPage > 0 && rawPage <= 1000000 ? rawPage : 1;
  const rawFilter = searchParams.get('filter');
  const filter = rawFilter === 'active' || rawFilter === 'finished' ? rawFilter : 'all';
  const archive = searchParams.get('archive') === 'archived' ? 'archived' : searchParams.get('archive') === 'all' ? 'all' : 'unarchived';
  const { data: runPage, loading, error, isRefreshing, reload, setData: setRunPage } = useApiData((signal) => api.runs({ page, pageSize: 50, filter, archive }, signal), [user.id, page, filter, archive], 'runs', page => page.items.some(activeRun));
  const runs = runPage?.items;
  const candidateQuery = useApiData((signal) => user.role === 'environment_owner' ? api.batchApprovalCandidates(signal) : Promise.resolve([]), [user.id, user.role], 'runs', items => items.some(activeRun));
  const [batchSelection, setBatchSelection] = useState<RunSummary[]>([]);
  const [batchCandidates, setBatchCandidates] = useState<RunSummary[]>([]);
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench', activeWorkbench);
  const [busy, setBusy] = useState<string>();
  const [actionExplanation, setActionExplanation] = useState<WorkExplanation>();
  const [batchOpen, setBatchOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [cleanupIds, setCleanupIds] = useState<string[]>();
  const [batchReason, setBatchReason] = useState('同一维护窗口批量批准，按环境 FIFO 串行执行');
  const [approvalReason, setApprovalReason] = useState('维护窗口内执行');
  const [deliveryModes, setDeliveryModes] = useState<Record<string, 'direct' | 'transfer'>>({});
  const [logQuery, setLogQuery] = useState('');
  const [logStream, setLogStream] = useState<'all' | 'stdout' | 'stderr' | 'system'>('all');
  const actionInFlight = useRef(false);
  const logRef = useRef<HTMLPreElement>(null);
  const selectedId = searchParams.get('selected') ?? runs?.[0]?.id;
  const { data: detail, loading: detailLoading, error: detailError, reload: reloadDetail, setData: setDetail } = useApiData<Run | undefined>(
    (signal) => selectedId ? api.run(selectedId, signal) : Promise.resolve(undefined),
    [user.id, selectedId],
    'runs',
  );

  useEffect(() => {
    setActionExplanation(undefined);
    setDeliveryModes({});
  }, [selectedId]);

  const activity = useRunActivity(selectedId, detail?.status);

  const runWorkItem = workbench?.items.find((item) => item.subject.type === 'run' && item.subject.id === selectedId);
  const filtered = runs ?? [];
  const batchableApprovals = candidateQuery.data ?? [];
  function setPage(nextPage: number, nextFilter = filter) {
    const next = new URLSearchParams(searchParams); next.set('page', String(nextPage)); next.set('filter', nextFilter); setSearchParams(next);
  }
  function selectRun(id: string) { const next = new URLSearchParams(searchParams); next.set('selected', id); setSearchParams(next); }
  useEffect(() => {
    if (!runPage) return;
    const last = Math.max(1, Math.ceil(runPage.total / 50));
    if (page > last) { const next = new URLSearchParams(searchParams); next.set('page', String(last)); setSearchParams(next, { replace: true }); }
  }, [runPage, page, searchParams, setSearchParams]);
  useEffect(() => { setHistoryOpen(false); setCleanupIds(undefined); setBatchOpen(false); setBatchSelection([]); setBatchCandidates([]); }, [user.id]);

  function handleDeletedRuns(ids: string[]) {
    setRunPage(current => current ? { ...current, items: current.items.filter(run => !ids.includes(run.id)), total: Math.max(0, current.total - ids.length) } : current);
    if (!selectedId || !ids.includes(selectedId)) return;
    setDetail(undefined);
    const next = new URLSearchParams(searchParams);
    const remaining = runs?.find(run => !ids.includes(run.id));
    if (remaining) next.set('selected', remaining.id);
    else next.delete('selected');
    setSearchParams(next, { replace: true });
  }

  async function action(kind: 'cancel' | 'approve' | 'reject') {
    if (!detail || actionInFlight.current) return;
    actionInFlight.current = true;
    setBusy(kind);
    setActionExplanation(undefined);
    try {
      let updated: Run | undefined;
      if (kind === 'cancel') updated = await api.cancelRun(detail.id);
      else if (detail.approval?.id) updated = await (kind === 'approve'
        ? api.approve(detail.approval.id, approvalReason.trim(), (detail.deliveryRequirements ?? []).map((item) => ({ requirementId: item.id, mode: deliveryModes[item.id]! })))
        : api.reject(detail.approval.id, approvalReason.trim()));
      if (!updated) throw new Error('审批记录已失效，请刷新后重试。');
      setDetail(updated);
      notify('success', kind === 'approve' ? '已批准执行' : kind === 'reject' ? '已拒绝执行' : '取消请求已发送'); signalRefresh(['runs', 'environments', 'scenarios', 'workbench']);
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '操作失败', displayError(reason)); } finally { actionInFlight.current = false; setBusy(undefined); }
  }

  async function approveBatch() {
    if (!batchSelection.length || !batchReason.trim() || actionInFlight.current) return;
    actionInFlight.current = true;
    setBusy('batch-approve');
    try {
      await api.batchDecideApprovals(batchSelection.map((run) => run.approvalId!), 'approved', batchReason.trim());
      setBatchOpen(false);
      notify('success', `已批量批准 ${batchSelection.length} 个运行`, '运行已按各环境 FIFO 队列排队，不会并发占用同一环境。');
      signalRefresh(['runs', 'environments', 'scenarios', 'workbench']);
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '批量审批失败', displayError(reason)); }
    finally { actionInFlight.current = false; setBusy(undefined); }
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
      selectRun(created.id);
    } catch (reason) { setActionExplanation(actionableExplanation(reason)); notify('error', '无法安全续跑', displayError(reason)); }
    finally { setBusy(undefined); }
  }

  const progress = detail?.progress ?? (detail?.status === 'succeeded' ? 100 : detail?.steps?.length ? Math.round(detail.steps.filter((step) => step.status === 'succeeded').length / detail.steps.length * 100) : 0);
  const succeededSteps = detail?.steps?.filter((step) => step.status === 'succeeded').length ?? 0;
  const progressLabel = detail && !ACTIVE.has(detail.status) && detail.status !== 'succeeded' && detail.steps?.length ? `${succeededSteps}/${detail.steps.length} 步完成` : `${progress}%`;
  const allLogLines = useMemo(() => (activity.data?.logs ?? []).flatMap((entry) => `[${entry.stream}] ${entry.message}`.split('\n')), [activity.data?.logs]);
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
    window.setTimeout(() => URL.revokeObjectURL(url), 30000);
  }

  return <div className="page">
    <PageHeader eyebrow="Ansible executions" title="运行中心" description="跟踪排队、审批、执行步骤和实时脱敏日志；每个环境按 FIFO 串行执行。" actions={<>{user.role === 'platform_admin' && <button type="button" className="button button--quiet" onClick={() => setHistoryOpen(true)}><History size={16} /> 运行历史管理</button>}{user.role === 'environment_owner' && batchableApprovals.length ? <button className="button button--primary" disabled={Boolean(candidateQuery.error) || candidateQuery.loading} onClick={() => { setBatchCandidates([...batchableApprovals]); setBatchSelection(batchableApprovals.slice(0, 100)); setBatchOpen(true); }}><ShieldAlert size={16} /> 批量审批 {batchableApprovals.length}</button> : undefined}</>} />
    {candidateQuery.error ? <ErrorBlock message={candidateQuery.error} onRetry={() => void candidateQuery.reload()} /> : null}
    <div className="run-filter" aria-label="运行历史视图">{(['unarchived','archived','all'] as const).map(value=><button key={value} className={archive===value?'active':''} onClick={()=>{const next=new URLSearchParams(searchParams);next.set('archive',value);next.set('page','1');setSearchParams(next)}}>{value==='unarchived'?'当前记录':value==='archived'?'历史归档':'全部记录'}</button>)}</div>
    {user.role === 'platform_admin' && historyOpen && <Modal size="wide" title="运行历史管理" description="管理当前页成功记录归档、失败记录删除与自动保留策略。" onClose={() => setHistoryOpen(false)}><RunRetentionPanel runs={runs ?? []} onDeleted={handleDeletedRuns} /></Modal>}
    {user.role === 'platform_admin' && cleanupIds && <RunCleanupModal runIds={cleanupIds} onClose={() => setCleanupIds(undefined)} onDeleted={handleDeletedRuns} />}
    <RefreshNotice loading={isRefreshing} error={runs ? error : undefined} onRetry={() => void reload()} />
    {loading && !runs ? <LoadingBlock label="正在加载运行队列…" /> : error && !runs ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="runs-layout">
      <aside className="run-sidebar panel">
        <div className="run-filter"><ListFilter size={16} />{(['all', 'active', 'finished'] as const).map((value) => <button key={value} className={filter === value ? 'active' : ''} onClick={() => setPage(1, value)}>{value === 'all' ? '全部' : value === 'active' ? '进行中' : '已结束'}</button>)}</div>
        <div className="run-cards">{filtered.map((run) => <button key={run.id} className={`run-card${selectedId === run.id ? ' active' : ''}`} onClick={() => selectRun(run.id)}><span className={`run-card__status run-card__status--${run.status}`}>{run.status === 'succeeded' ? <CheckCircle2 size={17} /> : run.status === 'awaiting_approval' ? <ShieldAlert size={17} /> : <CircleDashed size={17} />}</span><div><strong>{run.name || `Run ${run.id.slice(0, 8)}`}</strong><small>{run.environmentName ?? '未知环境'} · {formatTime(run.createdAt)}</small><span><StatusPill status={run.status} />{run.archiveStatus === "archived" ? <small>归档于 {formatTime(run.archivedAt)} · {formatFileSize(run.archiveSizeBytes ?? 0)}</small> : null}{run.queuePosition ? <em>队列 #{run.queuePosition}</em> : null}</span></div><ChevronRight size={16} /></button>)}</div>
        <nav className="run-pagination" aria-label="运行分页"><button className="button button--quiet" disabled={page <= 1 || loading} onClick={() => setPage(page - 1)}>上一页</button><span>第 {page} / {Math.max(1, Math.ceil((runPage?.total ?? 0) / 50))} 页 · 共 {runPage?.total ?? 0} 条</span><button className="button button--quiet" disabled={page * 50 >= (runPage?.total ?? 0) || loading} onClick={() => setPage(page + 1)}>下一页</button></nav>
        {!filtered.length && <EmptyState title="当前筛选无运行" />}
      </aside>
      {detailLoading && !detail ? <section className="panel"><LoadingBlock label="正在加载运行详情…" /></section> : detailError ? <section className="panel"><ErrorBlock message={detailError} onRetry={() => void reloadDetail()} /></section> : detail ? <section className="run-detail detail-stack">
        <article className="panel run-hero">
          <div><div className={`run-symbol run-symbol--${detail.status}`}><PlayCircle size={23} /></div><div><div className="eyebrow">{scenarioRunModeLabel(detail.executionMode, detail.kind) || detail.kind?.replaceAll('_', ' ') || 'scenario run'} · {detail.id.slice(0, 12)}</div><h2>{detail.name ?? detail.scenarioName ?? detail.componentName}</h2><p>{detail.environmentName} · 发起人 {detail.createdByName ?? detail.createdBy ?? '—'} · {formatTime(detail.createdAt)}</p></div></div>
          <div className="run-actions">{user.role === 'platform_admin' && detail.status === 'failed' && <button type="button" className="button button--danger-soft" onClick={() => setCleanupIds([detail.id])}><Trash2 size={14} /> 删除失败记录</button>}{user.role==='platform_admin' && detail.status==='succeeded' && !['archived','queued','running'].includes(detail.archive?.status ?? '') ? <button className="button button--quiet" disabled={busy==='archive'} onClick={async()=>{setBusy('archive');try{await api.archiveRuns([detail.id]);notify('success','归档任务已受理');signalRefresh('runs')}catch(e){notify('error','归档失败',displayError(e))}finally{setBusy(undefined)}}}>归档</button>:null}<StatusPill status={detail.status} />{ACTIVE.has(detail.status) && detail.status !== 'awaiting_approval' && <button disabled={busy === 'cancel'} className="button button--danger-soft" onClick={() => void action('cancel')}><Square size={14} /> 取消</button>}</div>
          <div className="progress-track"><span style={{ width: `${progress}%` }} /><small>{progressLabel}</small></div>
        </article>
        <RunDiagnosticsPanel key={detail.id} run={detail} retryBusy={busy === 'retry'} onRetryRun={() => void retrySafeRange()} />
        <RunLogDownload key={`logs-${detail.id}`} run={detail} />
        <RunDeliveryPanel run={detail} />
        <RefreshNotice loading={activity.loading} error={activity.error} onRetry={activity.reload} /><RunWaitingPanel status={activity.data?.status ?? detail.status} observations={activity.data?.waitingObservations ?? []} />
        <StatusExplanationPanel item={runWorkItem} currentResourceHref={`/runs?selected=${selectedId}`} />
        <StatusExplanationPanel explanation={actionExplanation} title="运行操作被阻断" />
        {detail.status === 'awaiting_approval' && <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><ShieldAlert size={18} /></span><div><h2>风险审批与交付决策</h2><p>{detail.approval?.riskReason ?? '该运行需要环境 Owner 审批。'}</p></div></div></header>{detail.deliveryRequirements?.length ? <div className="backup-list">{detail.deliveryRequirements.map((item) => <section key={item.id}><strong>{item.kind === 'artifact' ? '介质' : '镜像'} · {item.componentName} / {item.name}</strong><p>{item.identity}</p><small>来源：{item.source}</small><small>目标：{item.target || '环境未配置本地目标'}</small>{user.role === 'environment_owner' ? <div className="row-actions"><label><input type="radio" name={item.id} checked={deliveryModes[item.id] === 'direct'} onChange={() => setDeliveryModes((current) => ({ ...current, [item.id]: 'direct' }))} /> 直接使用来源</label><label><input type="radio" name={item.id} disabled={!item.transferAvailable} checked={deliveryModes[item.id] === 'transfer'} onChange={() => setDeliveryModes((current) => ({ ...current, [item.id]: 'transfer' }))} /> 平移到环境目标</label></div> : null}</section>)}</div> : null}{user.role === 'environment_owner' ? <div className="modal-body"><label><span>审批理由</span><textarea rows={2} value={approvalReason} onChange={(event) => setApprovalReason(event.target.value)} /></label><div className="row-actions"><button disabled={Boolean(busy)} className="button button--quiet" onClick={() => void action('reject')}><X size={15} /> 拒绝</button><button disabled={Boolean(busy) || (detail.deliveryRequirements ?? []).some((item) => !deliveryModes[item.id])} className="button button--primary" onClick={() => void action('approve')}><Check size={15} /> 批准执行</button></div></div> : <span>仅环境 Owner 可审批</span>}</article>}
        {detail.deliveryRequirements?.length ? <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><Archive size={18} /></span><div><h2>锁定交付记录</h2><p>来源、目标、审批选择和实际结果均来自本次 Run 快照。</p></div></div></header><div className="backup-list">{detail.deliveryRequirements.map((item) => { const decision = detail.deliveryDecisions?.find((entry) => entry.requirementId === item.id); const result = detail.deliveryResults?.find((entry) => entry.requirementId === item.id); return <section key={item.id}><strong>{item.kind} · {item.name}</strong><p>{decision ? `${decision.mode === 'direct' ? '直接使用来源' : '平移到目标'} · ${decision.decidedBy ?? '—'}` : '等待逐项决定'}</p><small>{result?.actualLocation ?? item.source}</small><small>{result?.status ?? 'pending'}{result?.message ? ` · ${result.message}` : ''}</small></section>; })}</div></article> : null}
        {(detail.artifactTransfers?.length || detail.imageTransfers?.length) ? <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><ShieldAlert size={18} /></span><div><h2>跨仓库平移</h2><p>以下内容属于本次审批范围；目标已存在相同指纹时不会重复传输。</p></div></div></header><div className="backup-list">{detail.imageTransfers?.map((item) => <section key={item.targetDigest}><strong>镜像：{item.sourceRegistry} → {item.targetRegistry}</strong><p>{item.targetDigest}</p></section>)}{detail.artifactTransfers?.map((item) => <section key={`${item.targetStation}-${item.relativePath}`}><strong>介质 {item.alias}：{item.sourceUrl} → {item.targetStation}</strong><p>{item.relativePath}</p><small>sha256:{item.sha256}</small></section>)}</div></article> : null}
        <article className="panel"><header className="panel__header"><div><span className="panel__icon"><Clock3 size={18} /></span><div><h2>执行步骤</h2><p>{detail.executionMode ? '组件验证及变更完成后，按顺序执行场景业务验收；失败立即停止并保留现场。' : '组件按顺序执行：前置检查 → 执行动作及 handler → 后置检查'}</p>{detail.jobDigest&&<a className="button button--quiet" href={api.runJobDownloadURL(detail.id)}>下载本次作业包</a>}{detail.exitCode!==undefined&&<small>作业退出码：{detail.exitCode}</small>}</div></div></header>{detail.steps?.length ? <div className="step-timeline">{detail.steps.map((step, index) => <Fragment key={step.id}>{(index === 0 || `${detail.steps![index - 1].sourceNodeId}/${detail.steps![index - 1].parentActionId ?? detail.steps![index - 1].id}` !== `${step.sourceNodeId}/${step.parentActionId ?? step.id}`) && <div className="step-group-heading"><strong>{step.componentName || step.name || scenarioStepLabel(step)}{step.parentAction ? ` · ${{ install: '安装', rollback: '回滚', configure: '配置', upgrade: '升级', uninstall: '卸载' }[step.parentAction] ?? step.parentAction}` : ''}</strong>{step.sourceNodeId && <small>节点 {step.sourceNodeId}</small>}</div>}<div id={`run-step-${step.id}`} key={step.id} className={`step step--${step.status}`}><span className="step__index">{step.status === 'succeeded' ? <Check size={14} /> : step.status === 'failed' ? <X size={14} /> : index + 1}</span><span className="step__line" /><div><div><strong>{scenarioStepLabel(step)}</strong><StatusPill status={step.status} /></div><p>{step.hostGroup&&<><HostGroupName value={step.hostGroup}/> · </>}{step.componentName ? `${step.componentName} · ` : ''}{step.action ?? ''} · {scenarioStepLabel(step)}{step.summary ? ` · ${step.summary}` : ''}</p><small>{formatTime(step.startedAt)}{step.finishedAt ? ` → ${formatTime(step.finishedAt)}` : ''}</small></div></div></Fragment>)}</div> : <EmptyState title="步骤尚未生成" description={detail.status === 'awaiting_approval' ? '审批通过后进入环境队列。' : 'Planner 正在生成执行步骤。'} />}</article>
        {detail.backups?.length ? <article className="panel"><header className="panel__header"><div><span className="panel__icon"><ShieldAlert size={18} /></span><div><h2>备份基线</h2><p>每个引用绑定创建它的变更 Run；恢复时核对完整引用链和版本基线。</p></div></div></header><div className="backup-list">{detail.backups.map((backup) => <section key={`${backup.nodeId ?? backup.componentId}-${backup.backupRef}`}><div><strong>{backup.componentName ?? backup.componentId} · {backup.action}</strong><StatusPill status={backup.action === 'rollback' ? 'rollback' : 'captured'}>{backup.action === 'rollback' ? '用于回滚' : '已锁定'}</StatusPill></div><p>{backup.backupRef}</p><small>来自 Run {backup.installRunId} · 捕获于 {formatTime(backup.capturedAt)} · Playbook {backup.playbookSha256.slice(0, 16)}…</small></section>)}</div></article> : null}
        {detail.resolvedParametersByNode && Object.keys(detail.resolvedParametersByNode).length > 0 && <article className="panel"><header className="panel__header"><div><span className="panel__icon"><ListFilter size={18} /></span><div><h2>解析参数</h2><p>下游参数值及其上游来源，不包含 CredentialRef 实际值</p></div></div></header><div className="resolved-parameters">{Object.entries(detail.resolvedParametersByNode).map(([nodeId, parameters]) => <section key={nodeId}><strong>{nodeId}</strong>{Object.entries(parameters).map(([name, item]) => <div key={name} className="parameter-preview__item"><span>{name}</span><small>{item.value === undefined ? '（空）' : String(item.value)}</small>{item.upstreamParameter ? <small className="parameter-lineage">{item.targetParameter ?? name} 来自节点 {item.sourceNodeId ?? '上游'} 的公开参数 {item.upstreamParameter}</small> : <small>{item.source ?? 'local'}{item.sourceNodeId ? ` · ${item.sourceNodeId}` : ''}</small>}</div>)}</section>)}</div></article>}
        {detail.archive?.status === 'archived' ? <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><Archive size={18} /></span><div><h2>历史日志已归档</h2><p>完整脱敏日志保存在归档包内。{formatFileSize(detail.archive.sizeBytes)}</p></div></div><a className="button button--quiet" href={api.archiveDownloadURL(detail.id)}><Download size={14} /> 下载归档包</a></header></article> : <article className="panel log-panel"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><ScrollText size={18} /></span><div><h2>{ACTIVE.has(detail.status) ? '实时日志' : '历史日志'}</h2><p>最近最多 2000 条 · 凭据自动脱敏 · 完整日志可下载</p></div></div><span className={`live-badge${ACTIVE.has(detail.status) ? '' : ' live-badge--history'}`}><span /> {ACTIVE.has(detail.status) ? 'LIVE' : 'ARCHIVED'}</span></header><div className="log-toolbar"><label><Search size={14} /><input aria-label="搜索运行日志" value={logQuery} onChange={(event) => setLogQuery(event.target.value)} placeholder="搜索主机、任务或错误" /></label><select aria-label="日志流筛选" value={logStream} onChange={(event) => setLogStream(event.target.value as typeof logStream)}><option value="all">全部流</option><option value="stdout">stdout</option><option value="stderr">stderr</option><option value="system">system</option></select><span>{visibleLogLines.length}/{allLogLines.length} 行</span><button className="icon-text" onClick={() => void navigator.clipboard?.writeText(visibleLogLines.join('\n'))}><Copy size={14} /> 复制结果</button><button className="icon-text" onClick={downloadLogs}><Download size={14} /> 下载已加载日志</button></div><pre ref={logRef}>{allLogLines.length ? visibleLogLines.join('\n') || '没有匹配的日志行。' : `[platform] Run ${detail.id}\n[platform] status=${detail.status}\n[platform] 等待 Ansible 输出…`}</pre></article>}
      </section> : <section className="panel"><EmptyState title="选择一个运行" description="查看步骤、审批和日志详情。" /></section>}
    </div>}
    {batchOpen && <Modal title={`批量批准 ${batchSelection.length} 个危险运行`} description={`共 ${batchCandidates.length} 个候选；每批最多 100 条。仅包含无需逐项交付选择的 Run，服务端原子校验本次勾选的审批。`} onClose={() => setBatchOpen(false)}><div className="modal-body"><div className="batch-approval-list">{batchCandidates.map((run) => { const checked = batchSelection.some((item) => item.id === run.id); return <label key={run.id}><input type="checkbox" aria-label={`审批候选 ${run.id}`} checked={checked} disabled={busy === 'batch-approve' || (!checked && batchSelection.length >= 100)} onChange={() => setBatchSelection((current) => checked ? current.filter((item) => item.id !== run.id) : [...current, run])} /><strong>{run.name}</strong><span>{run.environmentName} · Run {run.id} · 待风险审批</span></label>; })}</div><label><span>审批理由</span><textarea rows={3} value={batchReason} onChange={(event) => setBatchReason(event.target.value)} /></label></div><footer className="modal-actions"><button className="button button--quiet" onClick={() => setBatchOpen(false)}>取消</button><button className="button button--primary" disabled={busy === 'batch-approve' || !batchReason.trim() || !batchSelection.length} onClick={() => void approveBatch()}><Check size={15} /> {busy === 'batch-approve' ? '批准中…' : '确认批量批准'}</button></footer></Modal>}
  </div>;
}
