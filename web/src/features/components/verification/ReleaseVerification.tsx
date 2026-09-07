import { ChevronDown, ExternalLink } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { actionableExplanation, api } from '../../../api/client';
import { ExecutionPreparationPanel } from '../../../components/ExecutionPreparationPanel';
import { JobPlanPreview } from '../../../components/JobPlanPreview';
import { EmptyState, ErrorBlock, formatTime, InfoNote, LoadingBlock, Modal, RefreshNotice, StatusPill } from '../../../components/Primitives';
import { StatusExplanationPanel } from '../../../components/StatusExplanationPanel';
import { displayError, useApp } from '../../../context/AppContext';
import { activeRun } from '../../../hooks/activeWork';
import { useApiData } from '../../../hooks/useApiData';
import type { ComponentRelease, ComponentTestPlan, ComponentTestRequest, EvidenceSummary, WorkExplanation } from '../../../types/domain';
import { componentEvidenceLabel, componentRunLabel, scenarioRunLabel, type ScenarioRunEvidenceGroup } from '../model';

export function EvidenceLink({ run }: { run?: EvidenceSummary }) {
  if (!run) return <small className="verification-evidence verification-evidence--missing">暂无 Run 证据</small>;
  const stale = !run.matchesContract;
  return <Link className={`verification-evidence${run.status === 'succeeded' && !stale ? ' verification-evidence--passed' : ' verification-evidence--failed'}`} to={`/runs?selected=${run.id}`} onClick={(event) => event.stopPropagation()}>
    {run.environmentName ?? '未知环境'} · {formatTime(run.finishedAt ?? run.createdAt)}
    {run.status === 'failed' || run.status === 'interrupted' ? ' · 验证失败' : run.status === 'cancelled' ? ' · 已取消' : ''}
    {stale ? ' · 合同已变更' : ''}
    <ExternalLink size={12} aria-hidden="true" />
  </Link>;
}

export function ReleaseRunEvidenceModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user } = useApp();
  const { data: runs, loading, error, reload } = useApiData((signal) => api.releaseRunEvidence(release.id, signal), [release.id, user.id], 'runs', runs => runs.some(activeRun));
  const componentRuns = useMemo(() => (runs ?? []).filter((run) => run.kind === 'component_test'), [runs]);
  const scenarioGroups = useMemo(() => {
    const groups = new Map<string, ScenarioRunEvidenceGroup>();
    for (const run of runs ?? []) {
      if (run.kind !== 'scenario_test' && run.kind !== 'scenario_run') continue;
      const id = run.scenarioId ?? run.id;
      const group = groups.get(id) ?? { id, name: run.scenarioName ?? '未知场景', runs: [], testCount: 0, runCount: 0 };
      group.runs.push(run);
      if (run.kind === 'scenario_test') group.testCount++;
      else group.runCount++;
      groups.set(id, group);
    }
    return [...groups.values()].sort((left, right) => Date.parse(right.runs[0]?.createdAt ?? '') - Date.parse(left.runs[0]?.createdAt ?? ''));
  }, [runs]);
  const scenarioRunCount = scenarioGroups.reduce((total, group) => total + group.runs.length, 0);

  return <Modal size="wide" title={`Run 证据 ${release.version}`} description="组件验证与场景执行均绑定到该不可变 Release；点击记录可进入运行中心查看完整步骤和日志。" onClose={onClose}>
    <div className="modal-body run-evidence-modal">
      {runs ? <RefreshNotice loading={loading} error={error} onRetry={() => void reload()} /> : null}
      {loading && !runs ? <LoadingBlock label="正在读取 Run 证据…" /> : error && !runs ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <>
        <div className="run-evidence-summary"><div><span>组件验证</span><strong>{componentRuns.length}</strong></div><div><span>使用场景</span><strong>{scenarioGroups.length}</strong></div><div><span>场景 Run</span><strong>{scenarioRunCount}</strong></div></div>
        <section className="run-evidence-section" aria-label="组件验证 Run">
          <header><div><h3>组件验证</h3><p>包含当前发布证据及同一 Release 的历史验证记录</p></div><strong>{componentRuns.length} 条</strong></header>
          {componentRuns.length ? <div className="run-evidence-list">{componentRuns.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`} onClick={onClose}>
            <div><strong>{componentRunLabel(run)}</strong><small>{run.environmentName ?? '未知环境'} · {formatTime(run.finishedAt ?? run.createdAt)}</small></div>
            <span className={componentEvidenceLabel(release, run) === '历史证据' ? 'run-evidence-kind' : 'run-evidence-kind run-evidence-kind--current'}>{componentEvidenceLabel(release, run)}</span>
            <StatusPill status={run.status} />
            <ExternalLink size={14} aria-hidden="true" />
          </Link>)}</div> : <EmptyState title="暂无组件验证 Run" description="该 Release 尚未执行环境验证。" />}
        </section>
        <section className="run-evidence-section" aria-label="场景使用 Run">
          <header><div><h3>场景使用</h3><p>按场景聚合完整测试与正式运行，展开可查看每次不可变 Run</p></div><strong>{scenarioGroups.length} 个场景</strong></header>
          {scenarioGroups.length ? <div className="scenario-evidence-groups">{scenarioGroups.map((group) => {
            const latest = group.runs[0];
            return <details key={group.id} className="scenario-evidence-group">
              <summary>
                <div><strong>{group.name}</strong><small>{group.runCount} 次正式运行 · {group.testCount} 次完整测试</small></div>
                <div><small>{latest.environmentName ?? '未知环境'} · {formatTime(latest.finishedAt ?? latest.createdAt)}</small><StatusPill status={latest.status} /></div>
                <ChevronDown size={16} aria-hidden="true" />
              </summary>
              <div className="run-evidence-list">{group.runs.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`} onClick={onClose}>
                <div><strong>{scenarioRunLabel(run)}</strong><small>{run.environmentName ?? '未知环境'} · {formatTime(run.finishedAt ?? run.createdAt)}</small></div>
                <span className="run-evidence-id">Run {run.id.slice(0, 12)}</span>
                <StatusPill status={run.status} />
                <ExternalLink size={14} aria-hidden="true" />
              </Link>)}</div>
            </details>;
          })}</div> : <EmptyState title="尚未被场景运行使用" description="没有场景测试或正式运行锁定该 Release。" />}
        </section>
      </>}
    </div>
    <footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>关闭</button></footer>
  </Modal>;
}

export function TestReleaseModal({ release, onClose, onDone }: { release: ComponentRelease; onClose: () => void; onDone: () => void }) {
  const { notify, user } = useApp();
  const { data: environments } = useApiData(signal => api.environments(signal), [user.id], 'environments');
  const [selection, setSelection] = useState(release.parentReleaseId ? 'evolution' : release.actions?.find(action => action.type === 'install')?.id ?? '');
  const [environmentId, setEnvironmentId] = useState('');
  const [plan, setPlan] = useState<ComponentTestPlan>();
  const [busy, setBusy] = useState<'preview' | 'submit'>();
  const [error, setError] = useState<string>();
  const [explanation, setExplanation] = useState<WorkExplanation>();
  const invalidate = () => { setPlan(undefined); setError(undefined); setExplanation(undefined); };
  const input = (digest?: string): ComponentTestRequest => ({ environmentId, mode: selection === 'evolution' ? 'evolution_round_trip' : 'install_verify', actionId: selection === 'evolution' ? undefined : selection, expectedPlanDigest: digest });

  async function run() { if (!plan) return; setBusy('submit'); try { await api.testRelease(release.id, input(plan.planDigest)); notify('success', '验证已提交', '可在运行中心查看各阶段结果。'); onDone(); } catch (reason) { invalidate(); setError(displayError(reason)); setExplanation(actionableExplanation(reason)); } finally { setBusy(undefined); } }
  return <Modal title={`环境验证 ${release.version}`} description="先预览锁定计划，确认提交后创建单个 Ansible 作业。" onClose={onClose}><div className="modal-body">
    <label><span>验证动作</span><select aria-label="验证动作" value={selection} onChange={event => { setSelection(event.target.value); invalidate(); }}><option value="">请选择动作</option>{release.parentReleaseId && <option value="evolution">升级闭环：父版本安装 → 升级 → 回滚</option>}<optgroup label="执行动作">{release.actions?.filter(action => action.type !== 'check').map(action => <option key={action.id} value={action.id}>{action.name || action.type}（含已安排的检查）</option>)}</optgroup><optgroup label="独立检查">{release.actions?.filter(action => action.type === 'check').map(action => <option key={action.id} value={action.id}>{action.name}</option>)}</optgroup></select></label>
    <label><span>目标环境</span><select aria-label="目标环境" value={environmentId} onChange={event => { setEnvironmentId(event.target.value); invalidate(); }}><option value="">请选择环境</option>{environments?.map(env => <option key={env.id} value={env.id}>{env.name}</option>)}</select></label>
    <InfoNote>使用 Release 固定值、测试值及所选环境版本。回滚按其绑定的检查确认恢复基线。</InfoNote>
    <ExecutionPreparationPanel request={{ kind: "component_test", subjectId: release.id, environmentId, component: input() }} disabled={!selection || !!busy} onPlan={value => setPlan(value as ComponentTestPlan | undefined)} />
    {error && <p role="alert" className="form-validation">{error}</p>}<StatusExplanationPanel explanation={explanation} title="验证被阻断" />{plan && <JobPlanPreview plan={plan} />}
  </div><footer className="modal-actions"><button className="button button--quiet" disabled={!!busy} onClick={onClose}>取消</button><button className="button button--primary" disabled={!!busy || !plan} onClick={() => void run()}>确认提交验证</button></footer></Modal>;
}
