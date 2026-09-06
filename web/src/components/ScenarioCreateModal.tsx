import { useState, type FormEvent } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { Modal } from './Primitives';
import { BranchScope } from './BranchScope';
import { EnvironmentConstraintEditor } from './EnvironmentConstraintEditor';
import { environmentConstraintDimensions, parseConstraintSelection, serializeConstraintSelection } from '../types/environmentConstraints';
import type { Scenario, ScenarioForkPlan } from '../types/domain';

export function ScenarioCreateModal({ scenarios, initialSourceRevisionId = '', onClose, onDone }: {
  scenarios: Scenario[]; initialSourceRevisionId?: string; onClose: () => void; onDone: (scenario: Scenario) => void;
}) {
  const { notify, platformOptionCategories, platformOptionsLoading } = useApp();
  const mode = initialSourceRevisionId ? 'fork' : 'blank';
  const sources = scenarios.flatMap(scenario => scenario.revisions?.filter(revision => revision.state === 'released').map(revision => ({ scenario, revision })) ?? []);
  const [sourceRevisionId, setSourceRevisionId] = useState(initialSourceRevisionId);
  const [scope, setScope] = useState<Record<string, unknown>>(() => sources.find(item => item.revision.id === initialSourceRevisionId)?.revision.environmentConstraints ?? {});
  const [scopeDirty, setScopeDirty] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [name, setName] = useState(''); const [slug, setSlug] = useState(''); const [description, setDescription] = useState('');
  const [busy, setBusy] = useState(false); const [plan, setPlan] = useState<ScenarioForkPlan>();
  const dimensions = environmentConstraintDimensions(platformOptionCategories);
  const constraints = parseConstraintSelection(scope, dimensions);
  const environmentConstraints = serializeConstraintSelection(constraints, dimensions);
  function invalidate() { setPlan(undefined); }
  function changeSource(id: string) {
    if (scopeDirty && !window.confirm('切换来源会用来源标签替换当前选择，确认继续？')) return;
    setSourceRevisionId(id); setScope(sources.find(item => item.revision.id === id)?.revision.environmentConstraints ?? {}); setScopeDirty(false); setConfirmed(false); invalidate();
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!confirmed || platformOptionsLoading) return; setBusy(true);
    try {
      const input = {sourceRevisionId,name,slug,description,environmentConstraints};
      if (mode === 'fork' && !plan) { setPlan(await api.previewScenarioFork(input)); return; }
      const created = mode === 'fork' ? await api.forkScenario({...input,expectedPlanDigest:plan!.planDigest}) : await api.createScenario({name,slug,description,environmentConstraints});
      notify('success', mode === 'fork' ? '分支已创建' : '场景已创建', '从独立 r1 草稿开始；适配范围已固定。'); onDone(created);
    } catch (reason) { notify('error','创建失败',displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal size="wide" title={mode === 'fork' ? '新增分支' : '新建场景'} description="创建时确定适配标签；创建后如需改变范围，请新增分支。" onClose={onClose}><form onSubmit={event => void submit(event)}><div className="form-grid">
    {mode === 'fork' && <label className="span-2"><span>来源场景与已发布版本</span><select aria-label="分支来源版本" required value={sourceRevisionId} onChange={event => changeSource(event.target.value)}><option value="">请选择已发布版本</option>{sources.map(({scenario,revision}) => <option key={revision.id} value={revision.id}>{scenario.name} · r{revision.revision} · {scenario.ownerName ?? '场景 Owner'}</option>)}</select></label>}
    <label><span>场景名称</span><input name="name" required value={name} onChange={event => {setName(event.target.value);invalidate();}} placeholder="例如 Kylin Kubernetes 集群"/></label>
    <label><span>标识</span><input name="slug" required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" value={slug} onChange={event => {setSlug(event.target.value);invalidate();}} placeholder="kylin-k8s-cluster"/></label>
    <label className="span-2"><span>说明</span><textarea name="description" rows={3} value={description} onChange={event => {setDescription(event.target.value);invalidate();}}/></label>
    <div className="span-2"><EnvironmentConstraintEditor value={constraints} title="分支支持范围" onChange={next => {setScope(next);setScopeDirty(true);setConfirmed(false);invalidate();}}/><div className="scope-confirmation"><BranchScope scope={environmentConstraints} full/><label className="checkbox-field"><input type="checkbox" required checked={confirmed} onChange={event => setConfirmed(event.target.checked)}/><span>确认以上适配范围，创建后固定；未选择的维度为不限制。</span></label></div></div>
    {plan && <div className="span-2 scenario-fork-preview"><strong>分支预览 · 来源 r{plan.sourceRevision}</strong><p>{plan.nodeCount} 个组件节点 · {plan.acceptanceJobCount} 项业务验收；工作区独立复制。</p><span>来源范围</span><BranchScope scope={plan.sourceEnvironmentConstraints}/><span>新分支范围</span><BranchScope scope={plan.environmentConstraints}/><p>从 r1 草稿开始，不继承测试证据、发布状态或环境安装基线。适配冲突须在草稿中调整组件。</p></div>}
  </div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy || !confirmed || platformOptionsLoading}>{busy ? '处理中…' : mode === 'fork' ? plan ? '确认创建分支' : '预览分支' : '创建场景'}</button></footer></form></Modal>;
}
