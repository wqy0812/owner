import { newScenarioClientID as newClientID } from '../pages/scenarioLifecycle';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import type { ComponentTestPlan, ComponentTestRequest, ScenarioExecutionPreview } from '../types/domain';
import { StatusExplanationPanel } from './StatusExplanationPanel';
import { CheckCircle2, ClipboardCheck, CircleDashed, LoaderCircle, XCircle } from 'lucide-react';
import { StatusPill } from './Primitives';

export interface PreparationRequest { kind: 'component_test' | 'scenario_execution'; subjectId: string; environmentId: string; component?: ComponentTestRequest; scenario?: { environmentId: string; executionMode: string; testOnly: boolean } }
export interface PreparationSession { id: string; status: string; input: PreparationRequest; output: { checks: Array<{ id: string; category: string; label: string; host?: string; status: string; message?: string; source?: string; elapsedMs: number; startedAt?: string }>; plan?: unknown; error?: string; explanation?: import('../types/domain').WorkExplanation } }
const CATEGORIES: Record<string, string> = { runtime: '执行器', connectivity: '连通性', prerequisite: 'Ansible 运行基础', residual: '历史残留探测', component_check: '组件 YAML 检查', resource_contract: '资源合同', media: '介质核验' };
const STATES: Record<string, string> = { queued: '等待准备', running: '正在检查', pending: '待检查', scheduled: '执行时检查', passed: '通过', failed: '失败', skipped: '不适用', not_applicable: '不适用', provided: '由前置步骤提供', succeeded: '计划已生成', cancelled: '已取消', interrupted: '服务重启，需重新准备', timed_out: '准备超时' };
export function ExecutionPreparationPanel({ request, disabled, onPlan }: { request: PreparationRequest; disabled?: boolean; onPlan: (plan: ComponentTestPlan | ScenarioExecutionPreview | undefined) => void }) {
 const { user } = useApp();
 const [session, setSession] = useState<PreparationSession>();
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const signature = JSON.stringify(request);
 const scope = `${user.id}:${request.kind}:${request.subjectId}:${request.environmentId}`;
 const onPlanRef = useRef(onPlan); onPlanRef.current = onPlan;
 const delivered = useRef('');
 const epoch = useRef(0);
 const apply = useCallback((value: PreparationSession) => {
  setSession(value);
  if (value.status === 'succeeded' && value.output.plan && delivered.current !== value.id) {
   delivered.current = value.id;
   onPlanRef.current(api.preparationPlan(value.input.kind, value.output.plan));
  }
 }, []);
 useEffect(() => {
  const generation = ++epoch.current;
  setSession(undefined); setError(''); delivered.current = ''; onPlanRef.current(undefined);
  const stored = sessionStorage.getItem(`preparation:${scope}`);
  if (stored) {
   try { const saved = JSON.parse(stored) as { id: string; signature: string };
    if (saved.signature === signature) void api.preparation(saved.id).then(value => { if (epoch.current === generation) apply(value); }).catch(reason => { if (epoch.current === generation) setError(displayError(reason)); });
   } catch { sessionStorage.removeItem(`preparation:${scope}`); }
  }
  return () => { epoch.current++; };
 }, [signature, scope, apply]);
 useEffect(() => {
  if (!session || !['queued', 'running'].includes(session.status)) return;
  let cancelled = false;
  const timer = window.setInterval(() => { void api.preparation(session.id).then(value => { if (!cancelled) { apply(value); setError(''); } }).catch(reason => { if (!cancelled) setError(displayError(reason)); }); }, 1500);
  return () => { cancelled = true; window.clearInterval(timer); };
 }, [session?.id, session?.status, apply]);
 const start = async () => {
  const generation = epoch.current;
  setBusy(true); setError(''); onPlanRef.current(undefined); delivered.current = '';
  try { const value = await api.createPreparation({ ...request, idempotencyKey: newClientID() });
   if (generation !== epoch.current) return;
   sessionStorage.setItem(`preparation:${scope}`, JSON.stringify({ id: value.id, signature })); apply(value);
  } catch (reason) { if (generation === epoch.current) setError(displayError(reason)); } finally { setBusy(false); }
 };
 const active = Boolean(session && ['queued', 'running'].includes(session.status));
 return <section className="execution-preparation" aria-label="执行准备"><header className="execution-preparation__header"><div className="editor-section-heading"><span className="panel__icon"><ClipboardCheck size={18} aria-hidden="true" /></span><div><h4>执行准备</h4><p>逐项检查并生成锁定计划；页面刷新后可以继续查看进度。</p></div></div><div className="scenario-inline-actions"><button type="button" className="button button--secondary" disabled={disabled || busy || active || !request.environmentId} onClick={() => void start()}>{busy ? '正在创建准备任务…' : session ? '重新准备执行计划' : '预览执行计划'}</button>{active && <button type="button" className="button button--quiet" onClick={() => void api.cancelPreparation(session!.id).catch(reason => setError(displayError(reason)))}>取消准备</button>}</div></header>
 {session ? <div className="preparation-progress" role="status"><PreparationStatus status={session.status} /><span>已返回 {session.output.checks.length} 项检查</span></div> : <div className="preparation-idle"><CircleDashed size={16} aria-hidden="true" /><span>{request.environmentId ? '目标环境已选择，可开始检查并预览执行计划。' : '选择目标环境后，可检查执行器、连通性和介质，并预览组件 YAML 检查安排。'}</span></div>}
 {Object.entries(CATEGORIES).map(([category, label]) => { const checks = session?.output.checks.filter(item => item.category === category) ?? []; return checks.length ? <section className="preparation-category" key={category}><header><h5>{label}</h5><span>{checks.length} 项</span></header>{checks.map(item => <article key={item.id} className={`preparation-check preparation-check--${item.status}`}><header><strong>{item.host ? `${item.host} · ` : ''}{item.label}</strong><PreparationStatus status={item.status} /></header>{item.message && <p>{item.message}</p>}<div className="preparation-check__meta">{item.elapsedMs > 0 && <small>耗时 {(item.elapsedMs / 1000).toFixed(1)} 秒</small>}{item.startedAt && !item.startedAt.startsWith('0001-') && Number.isFinite(Date.parse(item.startedAt)) && <small>检查时间：{new Date(item.startedAt).toLocaleString()}</small>}{item.source && <small>来源：{item.source}</small>}</div></article>)}</section> : null; })}
 {(error || session?.output.error) && <p role="alert" className="inline-warning">{error || session?.output.error}</p>}
 <StatusExplanationPanel explanation={session?.output.explanation} title="执行准备被阻断" />
 </section>;
}

function PreparationStatus({ status }: { status: string }) {
 const passed = ['passed', 'succeeded'].includes(status);
 const failed = ['failed', 'timed_out', 'interrupted'].includes(status);
 const running = status === 'running';
 const Icon = passed ? CheckCircle2 : failed ? XCircle : running ? LoaderCircle : CircleDashed;
 return <StatusPill status={passed ? 'succeeded' : failed ? 'failed' : status}><Icon size={13} className={running ? 'spin' : undefined} aria-hidden="true" />{STATES[status] || status}</StatusPill>;
}
