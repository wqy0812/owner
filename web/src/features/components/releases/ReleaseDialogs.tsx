import { PencilLine } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../../../api/client';
import { BranchScope } from '../../../components/BranchScope';
import { EnvironmentConstraintEditor } from '../../../components/EnvironmentConstraintEditor';
import { HostGroupName } from '../../../components/HostGroupName';
import { LegacyYamlNotice } from '../../../components/LegacyYamlNotice';
import { DependencyContractList, ParameterContractList } from '../../../components/ParameterEditors';
import { LoadingBlock, Modal, StatusPill } from '../../../components/Primitives';
import { displayError, useApp } from '../../../context/AppContext';
import {
  COMPONENT_LAYERS
} from '../../../types/componentClassification';
import type { ActionDefinition, Component, ComponentLayer, ComponentRelease, PlaybookFile } from '../../../types/domain';
import { environmentConstraintDimensions, parseConstraintSelection, serializeConstraintSelection } from '../../../types/environmentConstraints';
import { EnvironmentConstraints, ReleaseReviewDetails } from '../catalog/ReleasePresentation';
import { PlaybookActionEditor } from '../contract/PlaybookActionEditor';
import { ACTION_OPTIONS, componentInput, type ContractEditIntent } from '../model';

export function EditComponentModal({ component, onClose, onDone }: { component: Component; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.updateComponent(component.id, componentInput(form));
      notify('success', '组件信息已更新'); onDone();
    } catch (reason) { notify('error', '更新组件失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title={`编辑 ${component.name}`} description="名称和分类在这里改。参数公开/内部、以及依赖哪个上游参数，属于 Release 合同。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" defaultValue={component.name} required /></label><label><span>标识</span><input name="slug" defaultValue={component.slug} required pattern="[a-z0-9\-]+" /></label><ClassificationFields component={component} /><label className="span-2"><span>说明</span><textarea name="description" defaultValue={component.description} rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存组件'}</button></footer></form></Modal>;
}

export function CreateComponentModal({ onClose, onDone }: { onClose: () => void; onDone: (component: Component) => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true);
    const form = new FormData(event.currentTarget);
    try { const component = await api.createComponent(componentInput(form)); notify('success', '组件已创建', '继续创建首个 Draft，并上传或在线编写 Playbook。'); onDone(component); }
    catch (reason) { notify('error', '创建失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建组件" description="选择稳定的架构分层；用途和检索维度使用自由标签。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" required placeholder="例如 containerd" /></label><label><span>标识</span><input name="slug" required pattern="[a-z0-9\-]+" placeholder="containerd" /></label><ClassificationFields /><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建组件'}</button></footer></form></Modal>;
}

function ClassificationFields({ component }: { component?: Component }) {
  const [layer, setLayer] = useState<ComponentLayer>(component?.layer ?? COMPONENT_LAYERS[0].value);
  return <>
    <label><span>组件层级</span><select name="layer" value={layer} onChange={(event) => setLayer(event.target.value as ComponentLayer)}>{COMPONENT_LAYERS.map((item) => <option key={item.value} value={item.value}>{item.code} · {item.label}</option>)}</select></label>
    <label><span>标签</span><input name="tags" defaultValue={component?.tags.join(', ') ?? ''} placeholder="runtime, core（最多 8 个）" /></label>
  </>;
}

export function NewVersionModal({ component, baseRelease, blank = false, onClose, onDone }: { component: Component; baseRelease?: ComponentRelease; blank?: boolean; contractIntent?: ContractEditIntent; onClose: () => void; onDone: (release?: ComponentRelease) => void }) {
  const { notify, platformOptionCategories } = useApp();
  const dimensions = environmentConstraintDimensions(platformOptionCategories);
  const [busy, setBusy] = useState(false);
  const lines = component.releaseLines ?? [];
  const releasedParents = lines.flatMap((line) => {
    if (!line.evolutionEligible || !line.evolutionParentId || (!blank && baseRelease && line.id !== baseRelease.lineId)) return [];
    const release = line.releases.find((item) => item.id === line.evolutionParentId);
    return release ? [release] : [];
  });
  const eligibleBase = baseRelease?.state === 'released' && releasedParents.some((item) => item.id === baseRelease.id) ? baseRelease : undefined;
  const suggestedParent = eligibleBase ?? releasedParents[0];
  const initialMode: 'new_line' | 'evolution' = blank || !component.releases?.length ? 'new_line' : 'evolution';
  const mode = initialMode;
  const [scopeConfirmed, setScopeConfirmed] = useState(false);
  const [parentReleaseId, setParentReleaseId] = useState(suggestedParent?.id ?? '');
  const [templateSourceReleaseId, setTemplateSourceReleaseId] = useState('');
  const source = component.releases?.find((item) => item.id === (mode === 'evolution' ? parentReleaseId : templateSourceReleaseId));
  const [constraints, setConstraints] = useState(() => parseConstraintSelection(initialMode === 'evolution' ? suggestedParent?.environmentConstraints : undefined, dimensions));
  const [constraintsCatalogReady, setConstraintsCatalogReady] = useState(dimensions.length > 0);
  const [constraintsDirty, setConstraintsDirty] = useState(false);
  useEffect(() => {
    if (constraintsCatalogReady || !dimensions.length) return;
    setConstraints(parseConstraintSelection(initialMode === 'evolution' ? suggestedParent?.environmentConstraints : undefined, dimensions));
    setConstraintsCatalogReady(true);
  }, [constraintsCatalogReady, dimensions, initialMode, suggestedParent?.environmentConstraints]);
  const blockedEvolutionReasons = lines.filter((line) => !line.evolutionEligible && line.evolutionBlockedReason);
  function changeConstraintSource(nextSource: ComponentRelease | undefined, commit: () => void) {
    const nextConstraints = parseConstraintSelection(nextSource?.environmentConstraints, dimensions);
    const currentSerialized = JSON.stringify(serializeConstraintSelection(constraints, dimensions));
    const nextSerialized = JSON.stringify(serializeConstraintSelection(nextConstraints, dimensions));
    if (constraintsDirty && currentSerialized !== nextSerialized && !window.confirm('切换创建来源会用新来源的环境约束覆盖当前手工编辑，是否继续？')) return;
    commit();
    setConstraints(nextConstraints);
    setConstraintsDirty(false); setScopeConfirmed(false);
  }
  function changeTemplate(nextID: string) {
    const nextSource = component.releases?.find((item) => item.id === nextID);
    changeConstraintSource(nextSource, () => setTemplateSourceReleaseId(nextID));
  }
  function changeParent(nextID: string) {
    const nextSource = component.releases?.find((item) => item.id === nextID);
    changeConstraintSource(nextSource, () => setParentReleaseId(nextID));
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    const form = new FormData(event.currentTarget);
    const environmentConstraints = serializeConstraintSelection(constraints, dimensions);
    const input = {
      mode,
      version: String(form.get('version')),
      releaseNotes: String(form.get('notes')),
      riskLevel: String(form.get('riskLevel')) as NonNullable<ComponentRelease['riskLevel']>,
      ...(mode === 'new_line' ? { environmentConstraints } : {}),
      ...(mode === 'new_line' ? {
        lineName: String(form.get('lineName')),
        templateSourceReleaseId: templateSourceReleaseId || undefined,
        compatibility: 'not_applicable' as const,
      } : {
        parentReleaseId,
        compatibility: String(form.get('compatibility')) as 'compatible' | 'breaking',
      }),
    };
    try {
      const plan = await api.previewReleaseDraft(component.id, input);
      const relation = plan.mode === 'evolution'
        ? `${plan.lineName}：${plan.parentVersion} → ${plan.targetVersion}`
        : `全新发布线：${plan.lineName} · ${plan.targetVersion}`;
      const removed = plan.removedActions.length ? `\n已移除转换动作：${plan.removedActions.join('、')}` : '';
      if (!window.confirm(`版本预览\n${relation}\n${plan.actions.length} 个 Action · ${plan.playbooks.length} 个 Playbook · ${plan.artifactCount} 个介质 · ${plan.imageCount} 个镜像${removed}\n\n确认创建？`)) return;
      const release = await api.createReleaseDraft(component.id, { ...input, expectedPlanDigest: plan.planDigest });
      if (!release) return;
      notify('success', '版本草稿已创建', '接下来为每个参数选择内部或公开，并映射上游公开参数。');
      onDone(release);
    } catch (reason) {
      notify('error', '创建版本失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }
  const title = `${mode === 'new_line' ? '新增分支' : '新增版本'} · ${component.name}`;
  const description = mode === 'new_line' ? '创建时确定适配范围，创建后该分支范围固定。内容模板不建立跨分支升级关系。' : '在当前分支继续演进，适配标签由分支继承。';
  return <Modal size="wide" title={title} description={description} onClose={onClose}>
    <form onSubmit={(event) => void submit(event)}>
      <div className="form-grid">
        {blockedEvolutionReasons.length ? <div className="span-2"><small>{blockedEvolutionReasons.map((line) => `${line.name}：${line.evolutionBlockedReason}`).join('；')}</small></div> : null}
        {mode === 'new_line' ? <>
          <label><span>分支名称</span><input name="lineName" required placeholder="例如 Kubernetes 1.34" /></label>
          <label><span>内容模板（可选）</span><select value={templateSourceReleaseId} onChange={(event) => changeTemplate(event.target.value)}><option value="">空白创建</option>{(component.releases ?? []).filter((item) => item.state === 'released' || item.state === 'deprecated').map((item) => <option key={item.id} value={item.id}>{item.lineName} · {item.version}</option>)}</select></label>
        </> : <>
          <label><span>演进来源</span><select value={parentReleaseId} required onChange={(event) => changeParent(event.target.value)}>{releasedParents.map((item) => <option key={item.id} value={item.id}>{item.lineName} · {item.version}</option>)}</select></label>
          <label><span>升级兼容性</span><select name="compatibility" defaultValue="compatible"><option value="compatible">兼容升级</option><option value="breaking">破坏性升级</option></select></label>
        </>}
        <label><span>新版本</span><input name="version" required placeholder="v1.1.0" /></label>
        <label><span>风险级别</span><select name="riskLevel" defaultValue={source?.riskLevel ?? 'low'}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <label className="span-2"><span>发布说明</span><textarea name="notes" required rows={4} placeholder="说明变化和下游注意事项" /></label>
        <div className="span-2">{mode === 'new_line' ? <><EnvironmentConstraintEditor value={constraints} onChange={next => { setConstraints(next); setConstraintsDirty(true); setScopeConfirmed(false); }} /><div className="scope-confirmation"><BranchScope scope={serializeConstraintSelection(constraints, dimensions)} full /><label className="checkbox-field"><input type="checkbox" checked={scopeConfirmed} onChange={event => setScopeConfirmed(event.target.checked)} required /><span>确认以上适配范围，创建后固定；未选择的维度为不限制。</span></label></div></> : <BranchScope scope={source?.environmentConstraints} full />}</div>
      </div>
      <footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy || (mode === 'new_line' ? !scopeConfirmed : !parentReleaseId)}>{busy ? '创建中…' : mode === 'new_line' ? '创建分支' : '创建版本'}</button></footer>
    </form>
  </Modal>;
}

export function EditReleaseModal({ release, releases, onClose, onDone }: { release: ComponentRelease; releases: ComponentRelease[]; onClose: () => void; onDone: () => void }) {
  const { notify, signalRefresh } = useApp();
  const [busy, setBusy] = useState(false);
  const [generation, setGeneration] = useState(release.definitionGeneration);
  const [actions, setActions] = useState<ActionDefinition[]>(release.actions ?? []);
  const [savedActions, setSavedActions] = useState(() => JSON.stringify(release.actions ?? []));
  const [playbookDirty, setPlaybookDirty] = useState(false);
  const actionsDirty = JSON.stringify(actions) !== savedActions;
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (playbookDirty || actionsDirty) { notify('error', '请先保存当前 Action', 'Action 配置或在线编辑器内容仍有未保存变更。'); return; }
    setBusy(true); const form = new FormData(event.currentTarget);
    try {
      const submittedActions = actions.map((item) => ({ ...item, requiredCredentials: item.requiredCredentials ?? [] }));
      const clearedCredentials = actions.reduce((count, action, index) => {
        const before = release.actions?.[index]?.requiredCredentials ?? [];
        const after = new Set(action.requiredCredentials ?? []);
        return count + before.filter((name) => !after.has(name)).length;
      }, 0);
      const saved = await api.updateRelease(release.id, {
        definitionGeneration: generation,
        version: String(form.get('version')),
        releaseNotes: String(form.get('notes')),
        compatibility: release.parentReleaseId ? String(form.get('compatibility')) as 'compatible' | 'breaking' : 'not_applicable',
        riskLevel: String(form.get('riskLevel')) as NonNullable<ComponentRelease['riskLevel']>,
        environmentConstraints: release.environmentConstraints,
        actions: submittedActions,
      });
      setActions(saved.actions ?? []);
      setSavedActions(JSON.stringify(saved.actions ?? []));
      notify('success', 'Draft 配置已保存', clearedCredentials ? `已清除 ${clearedCredentials} 个 CredentialRef；其余 Draft 配置也已更新。` : '版本信息已更新。');
      onDone();
    } catch (reason) { notify('error', '保存 Draft 失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  const close = () => { if (!busy) onClose(); };
  return <Modal size="wide" title={`配置 Draft ${release.version}`} description="维护版本信息与 Playbook。Action 保存/删除会立即原子持久化，关闭编辑器不会撤销。" onClose={close}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>版本</span><input name="version" defaultValue={release.version} required /></label><label><span>风险级别</span><select name="riskLevel" defaultValue={release.riskLevel ?? 'low'}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>{release.parentReleaseId ? <label><span>升级兼容性</span><select name="compatibility" defaultValue={release.compatibility}><option value="compatible">兼容升级</option><option value="breaking">破坏性升级</option></select></label> : <div className="field-summary"><span>版本关系</span><strong>全新基线 · 不适用升级兼容性</strong></div>}<label className="span-2"><span>发布说明</span><textarea name="notes" defaultValue={release.releaseNotes} rows={3} required /></label><div className="span-2"><PlaybookActionEditor releaseId={release.id} releases={releases} actions={actions} onChange={setActions} onPersisted={(persistedActions) => { setActions(persistedActions); setSavedActions(JSON.stringify(persistedActions)); signalRefresh('components'); void api.component(release.componentId).then(component => setGeneration(component.releases?.find(item => item.id === release.id)?.definitionGeneration)).catch(reason => notify('error', '版本状态刷新失败，请重新打开编辑器', displayError(reason))); }} onDirtyChange={setPlaybookDirty} /></div></div><footer className="modal-actions">{playbookDirty || actionsDirty ? <span className="modal-actions__hint">请先保存当前 Action</span> : null}<button type="button" className="button button--quiet" disabled={busy} onClick={close}>取消</button><button className="button button--primary" disabled={busy || playbookDirty || actionsDirty}><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</button></footer></form></Modal>;
}

export function InspectReleaseModal({ release, components, onClose }: { release: ComponentRelease; components: Component[]; onClose: () => void; onEdit?: () => void }) {
  const [selectedAction, setSelectedAction] = useState(0);
  const [playbook, setPlaybook] = useState<PlaybookFile>();
  const [playbookLoading, setPlaybookLoading] = useState(false);
  const [playbookError, setPlaybookError] = useState('');
  const action = release.actions?.[selectedAction];

  useEffect(() => {
    setPlaybook(undefined);
    setPlaybookError('');
    setPlaybookLoading(false);
    if (!action) return;
    const controller = new AbortController();
    setPlaybookLoading(true);
    void api.playbook(release.id, action.id ?? '', controller.signal)
      .then((value) => setPlaybook(value))
      .catch((reason) => {
        if (!controller.signal.aborted) setPlaybookError(displayError(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setPlaybookLoading(false);
      });
    return () => controller.abort();
  }, [action?.playbook, release.id]);

  const actionLabel = ACTION_OPTIONS.find((item) => item.value === action?.type)?.label ?? action?.type ?? '—';
  return <Modal size="wide" title={`${release.version} Release 详情`} description="只读查看版本合同、生命周期动作与已锁定的 Playbook 内容。" onClose={onClose}>
    <div className="modal-body inspect-contract">
      <section className="contract-section release-detail-summary">
        <h3>版本信息</h3>
        <div className="release-detail-grid">
          <div><span>状态</span><StatusPill status={release.state} /></div>
          <div><span>Readiness</span><StatusPill status={release.readiness.status} /></div>
          <div><span>风险等级</span><strong>{release.riskLevel ?? 'low'}</strong></div>
          <div><span>发布线</span><strong>{release.lineName}</strong></div>
          <div><span>版本关系</span><strong>{release.compatibility === 'not_applicable' ? '全新基线' : release.compatibility === 'compatible' ? '兼容升级' : '破坏性升级'}</strong></div>
        </div>
        <p>{release.releaseNotes || '未填写发布说明'}</p>
      </section>
      <section className="contract-section">
        <h3>适配标签</h3>
        <EnvironmentConstraints constraints={release.environmentConstraints} />
      </section>
      <ReleaseReviewDetails release={release} />
      <ParameterContractList release={release} components={components} />
      <section className="contract-section">
        <h3>依赖映射</h3>
        <DependencyContractList dependencies={release.dependencies ?? []} components={components} />
      </section>
      <section className="contract-section release-action-inspector">
        <h3>生命周期动作与 Playbook</h3>
        {release.actions?.length ? <>
          <label><span>生命周期动作</span><select aria-label="查看生命周期动作" value={selectedAction} onChange={(event) => setSelectedAction(Number(event.target.value))}>{release.actions.map((item, index) => <option key={item.id ?? `${item.type}-${index}`} value={index}>{ACTION_OPTIONS.find((option) => option.value === item.type)?.label ?? item.type} · {item.name || item.type}</option>)}</select></label>
          {action && <LegacyYamlNotice value={action.legacyYamlSettings} checksInPrecheck={action.type !== 'check'} />}
          {action && <div className="release-action-facts">
            <div><span>动作类型</span><strong>{actionLabel}</strong></div>
            <div><span>Playbook 路径</span><code>{action.playbook || '—'}</code></div>
            <div><span>主机组</span><strong>{<HostGroupName value={action.hostGroup} />}</strong></div>
            <div><span>超时 / 风险</span><strong>{action.timeoutSeconds ?? 1800}s / {action.riskLevel ?? 'low'}</strong></div>
            <div><span>参数来源</span><strong>由 Release 参数契约决定</strong></div>
            <div><span>CredentialRef</span><strong>{action.requiredCredentials?.join('、') || '无'}</strong></div>
            <div><span>能力</span><strong>{[action.idempotent ? '幂等' : '', action.destructive ? '破坏性' : ''].filter(Boolean).join('、') || '标准'}</strong></div>
            <div><span>版本转换</span><strong>{action.fromReleaseId || '—'} → {action.toReleaseId || '—'}</strong></div>
          </div>}
          {playbookLoading ? <LoadingBlock label="正在读取 Playbook…" /> : playbookError ? <div className="inline-warning" role="alert"><span>Playbook 读取失败：{playbookError}</span></div> : playbook ? <div className="release-playbook-source"><div><span>{playbook.filename}</span><code>sha256:{playbook.sha256}</code></div><pre className="code-editor release-playbook-preview">{playbook.content}</pre></div> : <div className="mapping-empty">当前动作未配置可读取的 Playbook。</div>}
        </> : <div className="mapping-empty">当前 Release 尚未配置生命周期动作。</div>}
      </section>
    </div>
    <footer className="modal-actions">
      <button type="button" className="button button--quiet" onClick={onClose}>关闭</button>
    </footer>
  </Modal>;
}

function SaveIcon() { return <PencilLine size={15} />; }
