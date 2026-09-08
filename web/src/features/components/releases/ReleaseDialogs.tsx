import { useDialogs } from '../../../components/UIProvider';
import { Field } from '../../../components/Field';
import { Input, Button, Select, Checkbox } from 'antd';
import { PencilLine } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api } from '../../../api/client';
import { BranchScope } from '../../../components/BranchScope';
import { EnvironmentConstraintEditor } from '../../../components/EnvironmentConstraintEditor';
import { HostGroupName } from '../../../components/HostGroupName';
import { DependencyContractList, ParameterContractList } from '../../../components/ParameterEditors';
import { LoadingBlock, Modal, StatusPill } from '../../../components/Primitives';
import { displayError, useApp } from '../../../context/AppContext';
import { COMPONENT_LAYERS } from '../../../types/componentClassification';
import type { ActionDefinition, Component, ComponentRelease, PlaybookFile } from '../../../types/domain';
import { environmentConstraintDimensions, parseConstraintSelection, serializeConstraintSelection, unselectedConstraintDimensions } from '../../../types/environmentConstraints';
import { EnvironmentConstraints, ReleaseReviewDetails } from '../catalog/ReleasePresentation';
import { PlaybookActionEditor } from '../contract/PlaybookActionEditor';
import { ACTION_OPTIONS, componentInput, type ContractEditIntent } from '../model';
export function EditComponentModal({ component, onClose, onDone }: {
    component: Component;
    onClose: () => void;
    onDone: () => void;
}) {
    const { notify } = useApp();
    const [busy, setBusy] = useState(false);
    async function submit(values: Record<string, string>) {
        setBusy(true);
        const form = values;
        try {
            await api.updateComponent(component.id, componentInput(form));
            notify('success', '组件信息已更新');
            onDone();
        }
        catch (reason) {
            notify('error', '更新组件失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    return <Modal busy={Boolean(busy)} title={`编辑 ${component.name}`} description="名称和分类在这里改。参数公开/内部、以及依赖哪个上游参数，属于 Release 合同。" onClose={onClose} formProps={{ onFinish: (values) => submit(values), layout: "vertical", preserve: false }} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={busy} htmlType={"submit"} type="primary">{busy ? '保存中…' : '保存组件'}</Button></footer>}><div className="form-grid"><Field label={"组件名称"} name={"name"} initialValue={component.name} required rules={[{ required: true, message: "请填写此项" }]}><Input required/></Field><Field label={"标识"} name={"slug"} initialValue={component.slug} required rules={[{ required: true, message: "请填写此项" }, { pattern: new RegExp("^(?:" + "[a-z0-9\-]+" + ")$"), message: "格式不正确" }]}><Input required pattern="[a-z0-9\-]+"/></Field><ClassificationFields component={component}/><Field className="span-2" label={"说明"} name={"description"} initialValue={component.description}><Input.TextArea rows={3}/></Field></div></Modal>;
}
export function CreateComponentModal({ onClose, onDone }: {
    onClose: () => void;
    onDone: (component: Component) => void;
}) {
    const { notify } = useApp();
    const [busy, setBusy] = useState(false);
    async function submit(values: Record<string, string>) {
        setBusy(true);
        const form = values;
        try {
            const component = await api.createComponent(componentInput(form));
            notify('success', '组件已创建', '继续创建首个 Draft，并上传或在线编写 Playbook。');
            onDone(component);
        }
        catch (reason) {
            notify('error', '创建失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    return <Modal busy={Boolean(busy)} title="新建组件" description="选择稳定的架构分层；用途和检索维度使用自由标签。" onClose={onClose} formProps={{ onFinish: (values) => submit(values), layout: "vertical", preserve: false }} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={busy} htmlType={"submit"} type="primary">{busy ? '创建中…' : '创建组件'}</Button></footer>}><div className="form-grid"><Field label={"组件名称"} name={"name"} required rules={[{ required: true, message: "请填写此项" }]}><Input required placeholder="例如 containerd"/></Field><Field label={"标识"} name={"slug"} required rules={[{ required: true, message: "请填写此项" }, { pattern: new RegExp("^(?:" + "[a-z0-9\-]+" + ")$"), message: "格式不正确" }]}><Input required pattern="[a-z0-9\-]+" placeholder="containerd"/></Field><ClassificationFields /><Field className="span-2" label={"说明"} name={"description"}><Input.TextArea rows={3}/></Field></div></Modal>;
}
function ClassificationFields({ component }: {
    component?: Component;
}) {
    return <>
    <Field label={"组件层级"} name={"layer"} initialValue={component?.layer ?? COMPONENT_LAYERS[0].value}><Select popupMatchSelectWidth={true}>{COMPONENT_LAYERS.map((item) => <Select.Option key={item.value} value={item.value}>{item.code} · {item.label}</Select.Option>)}</Select></Field>
    <Field label={"标签"} name={"tags"} initialValue={component?.tags.join(', ') ?? ''}><Input placeholder="runtime, core（最多 8 个）"/></Field>
  </>;
}
export function NewVersionModal({ component, baseRelease, blank = false, onClose, onDone }: {
    component: Component;
    baseRelease?: ComponentRelease;
    blank?: boolean;
    contractIntent?: ContractEditIntent;
    onClose: () => void;
    onDone: (release?: ComponentRelease) => void;
}) {
    const { confirm } = useDialogs();
    const { notify, platformOptionCategories, platformOptionsLoading, platformOptionsError } = useApp();
    const dimensions = environmentConstraintDimensions(platformOptionCategories);
    const [busy, setBusy] = useState(false);
    const lines = component.releaseLines ?? [];
    const releasedParents = lines.flatMap((line) => {
        if (!line.evolutionEligible || !line.evolutionParentId || (!blank && baseRelease && line.id !== baseRelease.lineId))
            return [];
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
    const unselected = unselectedConstraintDimensions(constraints, dimensions);
    const scopeReady = !platformOptionsLoading && !platformOptionsError && unselected.length === 0;
    useEffect(() => {
        if (constraintsCatalogReady || !dimensions.length)
            return;
        setConstraints(parseConstraintSelection(initialMode === 'evolution' ? suggestedParent?.environmentConstraints : undefined, dimensions));
        setConstraintsCatalogReady(true);
    }, [constraintsCatalogReady, dimensions, initialMode, suggestedParent?.environmentConstraints]);
    const blockedEvolutionReasons = lines.filter((line) => !line.evolutionEligible && line.evolutionBlockedReason);
    async function changeConstraintSource(nextSource: ComponentRelease | undefined, commit: () => void) {
        const nextConstraints = parseConstraintSelection(nextSource ? nextSource.environmentConstraints ?? {} : undefined, dimensions);
        const currentSerialized = JSON.stringify(constraints);
        const nextSerialized = JSON.stringify(nextConstraints);
        if (constraintsDirty && currentSerialized !== nextSerialized && !await confirm('切换创建来源会用新来源的环境约束覆盖当前手工编辑，是否继续？'))
            return;
        commit();
        setConstraints(nextConstraints);
        setConstraintsDirty(false);
        setScopeConfirmed(false);
    }
    function changeTemplate(nextID: string) {
        const nextSource = component.releases?.find((item) => item.id === nextID);
        changeConstraintSource(nextSource, () => setTemplateSourceReleaseId(nextID));
    }
    function changeParent(nextID: string) {
        const nextSource = component.releases?.find((item) => item.id === nextID);
        changeConstraintSource(nextSource, () => setParentReleaseId(nextID));
    }
    async function submit(values: Record<string, string>) {
        if (busy || (mode === 'new_line' && (!scopeConfirmed || !scopeReady)))
            return;
        setBusy(true);
        const form = values;
        const environmentConstraints = serializeConstraintSelection(constraints, dimensions);
        const input = {
            mode,
            version: String(form['version']),
            releaseNotes: String(form['notes']),
            riskLevel: String(form['riskLevel']) as NonNullable<ComponentRelease['riskLevel']>,
            ...(mode === 'new_line' ? { environmentConstraints } : {}),
            ...(mode === 'new_line' ? {
                lineName: String(form['lineName']),
                templateSourceReleaseId: templateSourceReleaseId || undefined,
                compatibility: 'not_applicable' as const,
            } : {
                parentReleaseId,
                compatibility: String(form['compatibility']) as 'compatible' | 'breaking',
            }),
        };
        try {
            const plan = await api.previewReleaseDraft(component.id, input);
            const relation = plan.mode === 'evolution'
                ? `${plan.lineName}：${plan.parentVersion} → ${plan.targetVersion}`
                : `全新发布线：${plan.lineName} · ${plan.targetVersion}`;
            const removed = plan.removedActions.length ? `\n已移除转换动作：${plan.removedActions.join('、')}` : '';
            if (!await confirm(`版本预览\n${relation}\n${plan.actions.length} 个 Action · ${plan.playbooks.length} 个 Playbook · ${plan.artifactCount} 个介质 · ${plan.imageCount} 个镜像${removed}\n\n确认创建？`))
                return;
            const release = await api.createReleaseDraft(component.id, { ...input, expectedPlanDigest: plan.planDigest });
            if (!release)
                return;
            notify('success', '版本草稿已创建', '接下来为每个参数选择内部或公开，并映射上游公开参数。');
            onDone(release);
        }
        catch (reason) {
            notify('error', '创建版本失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    const title = `${mode === 'new_line' ? '新增分支' : '新增版本'} · ${component.name}`;
    const description = mode === 'new_line' ? '创建时确定适配范围，创建后该分支范围固定。内容模板不建立跨分支升级关系。' : '在当前分支继续演进，适配标签由分支继承。';
    return <Modal busy={Boolean(busy)} size="wide" title={title} description={description} onClose={onClose} formProps={{ onFinish: (values) => submit(values), layout: "vertical", preserve: false }} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={busy || (mode === 'new_line' ? !scopeConfirmed || !scopeReady : !parentReleaseId)} htmlType={"submit"} type="primary">{busy ? '创建中…' : mode === 'new_line' ? '创建分支' : '创建版本'}</Button></footer>}>
      <div className="form-grid">
        {blockedEvolutionReasons.length ? <div className="span-2"><small>{blockedEvolutionReasons.map((line) => `${line.name}：${line.evolutionBlockedReason}`).join('；')}</small></div> : null}
        {mode === 'new_line' ? <>
          <Field label={"分支名称"} name={"lineName"} required rules={[{ required: true, message: "请填写此项" }]}><Input required placeholder="例如 Kubernetes 1.34"/></Field>
          <Field label={"内容模板（可选）"}><Select value={templateSourceReleaseId} onChange={(selectedValue) => changeTemplate(selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">空白创建</Select.Option>{(component.releases ?? []).filter((item) => item.state === 'released' || item.state === 'deprecated').map((item) => <Select.Option key={item.id} value={item.id}>{item.lineName} · {item.version}</Select.Option>)}</Select></Field>
        </> : <>
          <Field label={"演进来源"} required><Select value={parentReleaseId} onChange={(selectedValue) => changeParent(selectedValue)} popupMatchSelectWidth={true}>{releasedParents.map((item) => <Select.Option key={item.id} value={item.id}>{item.lineName} · {item.version}</Select.Option>)}</Select></Field>
          <Field label={"升级兼容性"} name={"compatibility"} initialValue={"compatible"}><Select popupMatchSelectWidth={true}><Select.Option value="compatible">兼容升级</Select.Option><Select.Option value="breaking">破坏性升级</Select.Option></Select></Field>
        </>}
        <Field label={"新版本"} name={"version"} required rules={[{ required: true, message: "请填写此项" }]}><Input required placeholder="v1.1.0"/></Field>
        <Field label={"风险级别"} name={"riskLevel"} initialValue={source?.riskLevel ?? 'low'}><Select popupMatchSelectWidth={true}><Select.Option value="low">低</Select.Option><Select.Option value="medium">中</Select.Option><Select.Option value="high">高</Select.Option><Select.Option value="destructive">破坏性（需审批）</Select.Option></Select></Field>
        <Field className="span-2" label={"发布说明"} name={"notes"} required rules={[{ required: true, message: "请填写此项" }]}><Input.TextArea required rows={4} placeholder="说明变化和下游注意事项"/></Field>
        <div className="span-2">{mode === 'new_line' ? <><EnvironmentConstraintEditor value={constraints} onChange={next => { setConstraints(next); setConstraintsDirty(true); setScopeConfirmed(false); }}/><div className="scope-confirmation"><BranchScope scope={serializeConstraintSelection(constraints, dimensions)} selection={constraints} full/>{unselected.length > 0 && <small>请先选择：{unselected.map(dimension => dimension.label).join('、')}。</small>}<Checkbox className="checkbox-field" checked={scopeConfirmed} disabled={!scopeReady} onChange={event => setScopeConfirmed(event.target.checked)} required><span>确认以上适配范围，创建后固定。</span></Checkbox></div></> : <BranchScope scope={source?.environmentConstraints} full/>}</div>
      </div>

    </Modal>;
}
export function EditReleaseModal({ release, releases, onClose, onDone }: {
    release: ComponentRelease;
    releases: ComponentRelease[];
    onClose: () => void;
    onDone: () => void;
}) {
    const { notify, signalRefresh } = useApp();
    const [busy, setBusy] = useState(false);
    const [generation, setGeneration] = useState(release.definitionGeneration);
    const [actions, setActions] = useState<ActionDefinition[]>(release.actions ?? []);
    const [savedActions, setSavedActions] = useState(() => JSON.stringify(release.actions ?? []));
    const [playbookDirty, setPlaybookDirty] = useState(false);
    const actionsDirty = JSON.stringify(actions) !== savedActions;
    async function submit(values: Record<string, string>) {
        if (playbookDirty || actionsDirty) {
            notify('error', '请先保存当前 Action', 'Action 配置或在线编辑器内容仍有未保存变更。');
            return;
        }
        setBusy(true);
        const form = values;
        try {
            const submittedActions = actions.map((item) => ({ ...item, requiredCredentials: item.requiredCredentials ?? [] }));
            const clearedCredentials = actions.reduce((count, action, index) => {
                const before = release.actions?.[index]?.requiredCredentials ?? [];
                const after = new Set(action.requiredCredentials ?? []);
                return count + before.filter((name) => !after.has(name)).length;
            }, 0);
            const saved = await api.updateRelease(release.id, {
                definitionGeneration: generation,
                version: String(form['version']),
                releaseNotes: String(form['notes']),
                compatibility: release.parentReleaseId ? String(form['compatibility']) as 'compatible' | 'breaking' : 'not_applicable',
                riskLevel: String(form['riskLevel']) as NonNullable<ComponentRelease['riskLevel']>,
                environmentConstraints: release.environmentConstraints,
                actions: submittedActions,
            });
            setActions(saved.actions ?? []);
            setSavedActions(JSON.stringify(saved.actions ?? []));
            notify('success', 'Draft 配置已保存', clearedCredentials ? `已清除 ${clearedCredentials} 个 CredentialRef；其余 Draft 配置也已更新。` : '版本信息已更新。');
            onDone();
        }
        catch (reason) {
            notify('error', '保存 Draft 失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    const close = () => {
        if (!busy)
            onClose();
    };
    return <Modal busy={Boolean(busy)} size="workspace" title={`配置 Draft ${release.version}`} description="维护版本信息与 Playbook。Action 保存/删除会立即原子持久化，关闭编辑器不会撤销。" onClose={close} formProps={{ onFinish: (values) => submit(values), layout: "vertical", preserve: false }} footer={<footer className="modal-actions">{playbookDirty || actionsDirty ? <span className="modal-actions__hint">请先保存当前 Action</span> : null}<Button className="button button--quiet" disabled={busy} onClick={close} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={busy || playbookDirty || actionsDirty} htmlType={"submit"} type="primary"><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</Button></footer>}><div className="form-grid"><Field label={"版本"} name={"version"} initialValue={release.version} required rules={[{ required: true, message: "请填写此项" }]}><Input required/></Field><Field label={"风险级别"} name={"riskLevel"} initialValue={release.riskLevel ?? 'low'}><Select popupMatchSelectWidth={true}><Select.Option value="low">低</Select.Option><Select.Option value="medium">中</Select.Option><Select.Option value="high">高</Select.Option><Select.Option value="destructive">破坏性（需审批）</Select.Option></Select></Field>{release.parentReleaseId ? <Field label={"升级兼容性"} name={"compatibility"} initialValue={release.compatibility}><Select popupMatchSelectWidth={true}><Select.Option value="compatible">兼容升级</Select.Option><Select.Option value="breaking">破坏性升级</Select.Option></Select></Field> : <div className="field-summary"><span>版本关系</span><strong>全新基线 · 不适用升级兼容性</strong></div>}<Field className="span-2" label={"发布说明"} name={"notes"} initialValue={release.releaseNotes} required rules={[{ required: true, message: "请填写此项" }]}><Input.TextArea rows={3} required/></Field><div className="span-2"><PlaybookActionEditor releaseId={release.id} releases={releases} actions={actions} onChange={setActions} onPersisted={(persistedActions) => { setActions(persistedActions); setSavedActions(JSON.stringify(persistedActions)); signalRefresh('components'); void api.component(release.componentId).then(component => setGeneration(component.releases?.find(item => item.id === release.id)?.definitionGeneration)).catch(reason => notify('error', '版本状态刷新失败，请重新打开编辑器', displayError(reason))); }} onDirtyChange={setPlaybookDirty}/></div></div></Modal>;
}
export function InspectReleaseModal({ release, components, onClose }: {
    release: ComponentRelease;
    components: Component[];
    onClose: () => void;
    onEdit?: () => void;
}) {
    const [selectedAction, setSelectedAction] = useState(0);
    const [playbook, setPlaybook] = useState<PlaybookFile>();
    const [playbookLoading, setPlaybookLoading] = useState(false);
    const [playbookError, setPlaybookError] = useState('');
    const action = release.actions?.[selectedAction];
    useEffect(() => {
        setPlaybook(undefined);
        setPlaybookError('');
        setPlaybookLoading(false);
        if (!action)
            return;
        const controller = new AbortController();
        setPlaybookLoading(true);
        void api.playbook(release.id, action.id ?? '', controller.signal)
            .then((value) => setPlaybook(value))
            .catch((reason) => {
            if (!controller.signal.aborted)
                setPlaybookError(displayError(reason));
        })
            .finally(() => {
            if (!controller.signal.aborted)
                setPlaybookLoading(false);
        });
        return () => controller.abort();
    }, [action?.playbook, release.id]);
    const actionLabel = ACTION_OPTIONS.find((item) => item.value === action?.type)?.label ?? action?.type ?? '—';
    return <Modal size="wide" title={`${release.version} Release 详情`} description="只读查看版本合同、生命周期动作与已锁定的 Playbook 内容。" onClose={onClose} footer={<footer className="modal-actions">
      <Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">关闭</Button>
    </footer>}>
    <div className="modal-body inspect-contract">
      <section className="contract-section release-detail-summary">
        <h3>版本信息</h3>
        <div className="release-detail-grid">
          <div><span>状态</span><StatusPill status={release.state}/></div>
          <div><span>Readiness</span><StatusPill status={release.readiness.status}/></div>
          <div><span>风险等级</span><strong>{release.riskLevel ?? 'low'}</strong></div>
          <div><span>发布线</span><strong>{release.lineName}</strong></div>
          <div><span>版本关系</span><strong>{release.compatibility === 'not_applicable' ? '全新基线' : release.compatibility === 'compatible' ? '兼容升级' : '破坏性升级'}</strong></div>
        </div>
        <p>{release.releaseNotes || '未填写发布说明'}</p>
      </section>
      <section className="contract-section">
        <h3>适配标签</h3>
        <EnvironmentConstraints constraints={release.environmentConstraints}/>
      </section>
      <ReleaseReviewDetails release={release}/>
      <ParameterContractList release={release} components={components}/>
      <section className="contract-section">
        <h3>依赖映射</h3>
        <DependencyContractList dependencies={release.dependencies ?? []} components={components}/>
      </section>
      <section className="contract-section release-action-inspector">
        <h3>生命周期动作与 Playbook</h3>
        {release.actions?.length ? <>
          <Field label={"生命周期动作"}><Select aria-label="查看生命周期动作" value={selectedAction} onChange={(selectedValue) => setSelectedAction(Number(selectedValue))} popupMatchSelectWidth={true}>{release.actions.map((item, index) => <Select.Option key={item.id ?? `${item.type}-${index}`} value={index}>{ACTION_OPTIONS.find((option) => option.value === item.type)?.label ?? item.type} · {item.name || item.type}</Select.Option>)}</Select></Field>

          {action && <div className="release-action-facts">
            <div><span>动作类型</span><strong>{actionLabel}</strong></div>
            <div><span>Playbook 路径</span><code>{action.playbook || '—'}</code></div>
            <div><span>主机组</span><strong>{<HostGroupName value={action.hostGroup}/>}</strong></div>
            <div><span>超时 / 风险</span><strong>{action.timeoutSeconds ?? 1800}s / {action.riskLevel ?? 'low'}</strong></div>
            <div><span>参数来源</span><strong>由 Release 参数契约决定</strong></div>
            <div><span>CredentialRef</span><strong>{action.requiredCredentials?.join('、') || '无'}</strong></div>
            <div><span>能力</span><strong>{[action.idempotent ? '幂等' : '', action.destructive ? '破坏性' : ''].filter(Boolean).join('、') || '标准'}</strong></div>
            <div><span>版本转换</span><strong>{action.fromReleaseId || '—'} → {action.toReleaseId || '—'}</strong></div>
          </div>}
          {playbookLoading ? <LoadingBlock label="正在读取 Playbook…"/> : playbookError ? <div className="inline-warning" role="alert"><span>Playbook 读取失败：{playbookError}</span></div> : playbook ? <div className="release-playbook-source"><div><span>{playbook.filename}</span><code>sha256:{playbook.sha256}</code></div><pre className="code-editor release-playbook-preview">{playbook.content}</pre></div> : <div className="mapping-empty">当前动作未配置可读取的 Playbook。</div>}
        </> : <div className="mapping-empty">当前 Release 尚未配置生命周期动作。</div>}
      </section>
    </div>

  </Modal>;
}
function SaveIcon() { return <PencilLine size={15}/>; }
