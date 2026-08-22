import { useEffect, useMemo, useRef, useState, type Dispatch, type FormEvent, type SetStateAction } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, Beaker, Boxes, ChevronDown, ChevronRight, Container, FileCode2, GitBranch, PencilLine, Plus, Rocket, Shield, Trash2, Upload, UserRound } from 'lucide-react';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { parseRunInput, RunInputFields, uniqueRunInputs } from '../components/RunInputFields';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import {
  COMPONENT_CATEGORY_LABELS,
  COMPONENT_KIND_LABELS,
  COMPONENT_LAYERS,
  COMPONENT_REQUIREDNESS_LABELS,
  componentLayer,
} from '../types/componentClassification';
import { ComponentMappingOverview, defaultContractRelease, defaultFixtureValues, DependencyContractList, DependencyEditor, mappedParameterNames, mappingCount, ParameterContractList, ParameterTable, parameterContractErrors } from '../components/ParameterEditors';
import { EnvironmentConstraintEditor } from '../components/EnvironmentConstraintEditor';
import { environmentConstraintGroups, parseConstraintSelection, serializeConstraintSelection } from '../types/environmentConstraints';
import type { ActionDefinition, Component, ComponentDependency, ComponentImageBuild, ComponentKind, ComponentLayer, ComponentRelease, ComponentRequiredness, Environment, ImpactPreview, ParameterDefinition } from '../types/domain';

type ContractSection = 'dependencies' | 'parameters';

const REQUIRED_LIFECYCLE_ACTIONS: Array<{ type: ActionDefinition['type']; label: string }> = [
  { type: 'install', label: '安装' },
  { type: 'verify', label: '验证' },
  { type: 'rollback', label: '回滚' },
];

function lifecycleSummary(actions: ActionDefinition[] = []) {
  const configured = new Set(actions.map((action) => action.type));
  const completed = REQUIRED_LIFECYCLE_ACTIONS.filter((action) => configured.has(action.type));
  return {
    completed: completed.length,
    total: REQUIRED_LIFECYCLE_ACTIONS.length,
    labels: completed.map((action) => action.label).join('、') || '尚未配置',
  };
}

export function ComponentsPage() {
  const { user, notify, signalRefresh } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: components, loading, error, isRefreshing, reload } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const selectedId = searchParams.get('selected') ?? undefined;
  const [createOpen, setCreateOpen] = useState(false);
  const [versionBase, setVersionBase] = useState<Component>();
  const [editComponent, setEditComponent] = useState<Component>();
  const [publishRelease, setPublishRelease] = useState<ComponentRelease>();
  const [editRelease, setEditRelease] = useState<ComponentRelease>();
  const [impact, setImpact] = useState<ImpactPreview>();
  const [testRelease, setTestRelease] = useState<ComponentRelease>();
  const [inspectRelease, setInspectRelease] = useState<ComponentRelease>();
  const [imageRelease, setImageRelease] = useState<ComponentRelease>();
  const [contractReleaseId, setContractReleaseId] = useState<string>();
  const [editingContract, setEditingContract] = useState(false);
  const [contractFocus, setContractFocus] = useState<ContractSection>();
  const [layerOpen, setLayerOpen] = useState<Partial<Record<ComponentLayer, boolean>>>({});
  const [busy, setBusy] = useState(false);

  const selected = useMemo(
    () => selectedId ? components?.find((item) => item.id === selectedId) : components?.[0],
    [components, selectedId],
  );
  const releases = selected?.releases?.length ? selected.releases : selected?.latestRelease ? [selected.latestRelease] : [];
  const contractRelease = releases.find((release) => release.id === contractReleaseId) ?? defaultContractRelease(releases, selected?.latestRelease);
  const editableDraft = releases.find((release) => release.state === 'draft');
  const visibleEditRelease = editRelease?.componentId === selected?.id ? editRelease : undefined;
  const showContractEditor = Boolean(editingContract && contractRelease?.state === 'draft');
  const mine = selected?.ownerId === user.id && user.role === 'component_owner';
  const canTest = mine || user.role === 'environment_owner';
  useEffect(() => {
    setContractReleaseId(undefined);
    setEditingContract(false);
    setContractFocus(undefined);
    setEditComponent(undefined);
    setPublishRelease(undefined);
    setImpact(undefined);
    setTestRelease(undefined);
    setInspectRelease(undefined);
    setEditRelease(undefined);
    setImageRelease(undefined);
  }, [selected?.id]);
  function selectContractRelease(id: string) {
    const release = releases.find((item) => item.id === id);
    setContractReleaseId(id);
    if (release?.state !== 'draft') {
      setEditingContract(false);
      setContractFocus(undefined);
    }
  }
  function startEditingContract(section?: ContractSection) {
    if (!selected || !mine) return;
    if (contractRelease?.state === 'draft') {
      setEditingContract(true);
      setContractFocus(section);
      return;
    }
    if (editableDraft) {
      setContractReleaseId(editableDraft.id);
      setEditingContract(true);
      setContractFocus(section);
      notify('info', '已切换到可编辑 Draft', `${contractRelease?.version ?? '当前版本'} 已不可修改，正在编辑 ${editableDraft.version}。`);
      return;
    }
    setVersionBase(selected);
  }
  const layeredComponents = COMPONENT_LAYERS.map((layer) => ({
    layer,
    components: components?.filter((component) => component.layer === layer.value) ?? [],
  }));
  function isLayerOpen(layer: ComponentLayer) {
    if (layerOpen[layer] !== undefined) return layerOpen[layer];
    return selected?.layer === layer;
  }
  function toggleLayer(layer: ComponentLayer) {
    setLayerOpen((current) => ({ ...current, [layer]: !isLayerOpen(layer) }));
  }
  function setAllLayers(open: boolean) {
    setLayerOpen(Object.fromEntries(COMPONENT_LAYERS.map((layer) => [layer.value, open])));
  }
  function selectComponent(id: string) {
    if (id === selected?.id) return;
    if (editRelease) {
      notify('info', '请先完成当前 Draft 编辑', '保存或关闭 Playbook 弹窗后才能切换组件。');
      return;
    }
    const component = components?.find((item) => item.id === id);
    if (component) setLayerOpen((current) => ({ ...current, [component.layer]: true }));
    setSearchParams({ selected: id });
  }

  async function previewPublish(release: ComponentRelease) {
    setPublishRelease(release);
    setImpact(undefined);
    try {
      setImpact(await api.releaseImpact(release.id));
    } catch (reason) {
      notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function confirmPublish() {
    if (!publishRelease) return;
    setBusy(true);
    try {
      await api.publishRelease(publishRelease.id);
      notify('success', '组件版本已发布', '下游 Owner 的站内影响通知已生成。');
      setPublishRelease(undefined);
      signalRefresh(['components', 'notifications']);
    } catch (reason) {
      notify('error', '发布失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function deprecate(release: ComponentRelease) {
    if (!window.confirm(`确认废弃 ${release.version}？已锁定该 Release ID 的历史 Run 不受影响。`)) return;
    try { await api.deprecateRelease(release.id); notify('success', '组件版本已废弃'); signalRefresh('components'); }
    catch (reason) { notify('error', '废弃失败', displayError(reason)); }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Component registry"
        title="组件中心"
        description="组件 Owner 在这里维护不可变发布、依赖关系和 Ansible 生命周期动作。"
        actions={user.role === 'component_owner' ? <button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建组件</button> : undefined}
      />
      {loading && !components ? <LoadingBlock label="正在读取组件目录…" /> : error && !components ? <ErrorBlock message={error} onRetry={() => void reload()} /> : (
        <>
        <RefreshNotice loading={isRefreshing} error={components ? error : undefined} onRetry={() => void reload()} />
        <div className="catalog-layout">
          <aside className="catalog-list panel">
            <div className="catalog-list__header">
              <strong>组件目录</strong>
              <div className="catalog-list__tools">
                <span>{components?.length ?? 0}</span>
                <button type="button" className="icon-text" onClick={() => setAllLayers(true)}>全部展开</button>
                <button type="button" className="icon-text" onClick={() => setAllLayers(false)}>全部折叠</button>
              </div>
            </div>
            {components?.length ? layeredComponents.map(({ layer, components: layerComponents }) => {
              const open = isLayerOpen(layer.value);
              return <section className={`catalog-layer${open ? '' : ' catalog-layer--collapsed'}`} key={layer.value}>
                <button type="button" className="catalog-layer__toggle" aria-expanded={open} aria-controls={`catalog-layer-${layer.value}`} onClick={() => toggleLayer(layer.value)}>
                  <span>{layer.code}</span>
                  <div><strong>{layer.label}</strong><small>{layerComponents.length} 个组件</small></div>
                  {open ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight size={14} aria-hidden="true" />}
                </button>
                {open ? <div id={`catalog-layer-${layer.value}`}>{layerComponents.length ? layerComponents.map((component) => (
                  <button key={component.id} type="button" disabled={Boolean(editRelease) && selected?.id !== component.id} className={`catalog-item${selected?.id === component.id ? ' active' : ''}`} onClick={() => selectComponent(component.id)}>
                    <span className="catalog-item__icon"><Boxes size={18} /></span>
                    <span><strong>{component.name}</strong><small>{COMPONENT_CATEGORY_LABELS[component.category]} · {COMPONENT_KIND_LABELS[component.kind]}</small></span>
                    {component.ownerId === user.id && <span className="mine-dot" title="我负责的组件" />}
                  </button>
                )) : <div className="catalog-layer__empty">本层暂无组件</div>}</div> : null}
              </section>;
            }) : <EmptyState title="暂无组件" description="组件 Owner 可以创建第一个组件。" />}
          </aside>

          {selected ? <section className="detail-stack">
            <article className="panel component-hero">
              <div className="component-hero__title">
                <span className="component-logo"><Boxes size={26} /></span>
                <div><div className="eyebrow">{componentLayer(selected.layer).code} · {selected.slug ?? 'component'}</div><h2>{selected.name}</h2><p>{selected.description ?? '暂无组件说明'}</p><div className="classification-badges"><span>{componentLayer(selected.layer).label}</span><span>{COMPONENT_CATEGORY_LABELS[selected.category]}</span><span>{COMPONENT_KIND_LABELS[selected.kind]}</span><span>{COMPONENT_REQUIREDNESS_LABELS[selected.requiredness]}</span></div></div>
              </div>
              <div className="component-hero__meta">
                <span><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}</span>
                <span><GitBranch size={15} /> {selected.releaseCount ?? releases.length} 个版本</span>
                {selected.latestRelease && <StatusPill status={selected.latestRelease.state} />}
              </div>
              {mine && <div className="row-actions"><button className="button button--quiet" onClick={() => setEditComponent(selected)}><PencilLine size={16} /> 编辑组件</button><button className="button button--secondary" onClick={() => startEditingContract()}><PencilLine size={16} /> {editableDraft ? '编辑依赖和参数' : '创建 Draft 编辑合同'}</button>{editableDraft ? <button className="button button--quiet" onClick={() => setEditRelease(editableDraft)}><FileCode2 size={16} /> 编辑版本与 Playbook</button> : null}</div>}
            </article>

            <article className="panel">
              <header className="panel__header"><div><span className="panel__icon"><Rocket size={18} /></span><div><h2>发布历史</h2><p>点击版本可切换下方依赖和参数合同；Released 版本不可修改</p></div></div></header>
              {releases.length ? <div className="release-table">
                <div className="release-table__head"><span>版本</span><span>适配环境</span><span>验证</span><span>依赖 / 动作</span><span>发布时间</span><span /></div>
                {releases.map((release) => {
                  const active = contractRelease?.id === release.id;
                  const lifecycle = lifecycleSummary(release.actions);
                  return <div key={release.id} className={`release-row${active ? ' release-row--active' : ''}`} onClick={() => selectContractRelease(release.id)}>
                    <button type="button" className="release-row__version" aria-pressed={active} aria-label={`查看 ${release.version} 的依赖和参数合同`} onClick={() => selectContractRelease(release.id)}>
                      <strong>{release.version}</strong>{release.breaking && <span className="breaking-badge">BREAKING</span>}
                      <small>{release.releaseNotes ?? '未填写发布说明'}</small>
                    </button>
                    <EnvironmentConstraints constraints={release.environmentConstraints} />
                    <div><StatusPill status={release.verification ?? (release.verified ? 'passed' : 'unverified')} /></div>
                    <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖</span><span>{mappingCount(release)} 个参数映射</span><span>{publicCount(release)} 个公开参数</span><span className={`lifecycle-completeness${lifecycle.completed === lifecycle.total ? ' lifecycle-completeness--complete' : ''}`}>动作 {lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</span></div>
                    <div>{formatTime(release.releasedAt ?? release.createdAt)}</div>
                    <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                      {canTest && <button className="icon-text" onClick={() => { setContractReleaseId(release.id); setTestRelease(release); }}><Beaker size={15} /> 测试</button>}
                      <button className="icon-text" onClick={() => { setContractReleaseId(release.id); setInspectRelease(release); }}>查看合同</button>
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => { selectContractRelease(release.id); setEditingContract(true); }}><PencilLine size={15} /> 配置合同</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setEditRelease(release)}><FileCode2 size={15} /> Playbook</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setImageRelease(release)}><Container size={15} /> 构建镜像</button>}
                      {mine && release.state === 'draft' && <button className="icon-text icon-text--primary" onClick={() => void previewPublish(release)}><Rocket size={15} /> 发布</button>}
                      {mine && release.state === 'released' && <button className="icon-text" onClick={() => void deprecate(release)}>废弃</button>}
                    </div>
                  </div>;
                })}
              </div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。" />}
            </article>

            <ComponentMappingOverview releases={releases} components={components ?? []} />
            {!contractReleaseId && selected.latestRelease && contractRelease && selected.latestRelease.id !== contractRelease.id ? <p className="mapping-empty contract-hint">当前展示 {contractRelease.version}，因为它有参数映射。最新版本 {selected.latestRelease.version} 没有映射。</p> : null}
            {showContractEditor && contractRelease ? (
              <ReleaseContractEditor key={contractRelease.id} release={contractRelease} components={components ?? []} focusSection={contractFocus} onCancel={() => { setEditingContract(false); setContractFocus(undefined); }} onSaved={() => { setEditingContract(false); setContractFocus(undefined); signalRefresh('components'); }} />
            ) : (
              <div className="two-column">
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{contractRelease ? `${contractRelease.version} 锁定的上游，以及引用了哪个公开参数` : '锁定上游版本，并标明引用了哪个公开参数'}</p></div></div>
                    {mine ? <button type="button" className="button button--quiet" aria-label="编辑直接依赖" onClick={() => startEditingContract('dependencies')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <DependencyContractList dependencies={contractRelease?.dependencies ?? []} components={components ?? []} />
                </article>
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>{contractRelease ? `${contractRelease.version} 的公开参数可被下游引用；内部参数只给本组件使用` : '公开参数可被下游引用；内部参数只给本组件使用'}</p></div></div>
                    {mine ? <button type="button" className="button button--quiet" aria-label="编辑参数合同" onClick={() => startEditingContract('parameters')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <ParameterContractList release={contractRelease} components={components ?? []} />
                </article>
              </div>
            )}
          </section> : <section className="panel"><EmptyState title="请选择组件" /></section>}
        </div>
        </>
      )}

      {createOpen && <CreateComponentModal onClose={() => setCreateOpen(false)} onDone={(component) => { setCreateOpen(false); setSearchParams({ selected: component.id }); setVersionBase(component); signalRefresh('components'); }} />}
      {editComponent && <EditComponentModal component={editComponent} onClose={() => setEditComponent(undefined)} onDone={() => { setEditComponent(undefined); signalRefresh('components'); }} />}
      {versionBase && <NewVersionModal component={versionBase} baseRelease={contractRelease ?? versionBase.latestRelease} onClose={() => setVersionBase(undefined)} onDone={(release) => { setVersionBase(undefined); signalRefresh('components'); if (release) { setContractReleaseId(release.id); setEditRelease(release); } }} />}
      {inspectRelease && <InspectReleaseModal release={inspectRelease} components={components ?? []} onClose={() => setInspectRelease(undefined)} onEdit={mine && inspectRelease.state === 'draft' ? () => { setInspectRelease(undefined); setContractReleaseId(inspectRelease.id); setEditingContract(true); } : undefined} />}
      {visibleEditRelease && <EditReleaseModal key={visibleEditRelease.id} release={visibleEditRelease} releases={releases} onClose={() => setEditRelease(undefined)} onDone={() => { setEditRelease(undefined); signalRefresh('components'); }} />}
      {testRelease && <TestReleaseModal release={testRelease} onClose={() => setTestRelease(undefined)} onDone={() => { setTestRelease(undefined); signalRefresh(['components', 'runs']); }} />}
      {imageRelease && <ImageBuildModal release={imageRelease} onClose={() => setImageRelease(undefined)} />}
      {publishRelease && <Modal title={`发布 ${publishRelease.version}`} description="发布后版本不可修改；影响通知将发送给下游组件和场景 Owner。" onClose={() => setPublishRelease(undefined)}>
        <div className="modal-body">
          {!publishRelease.verified && publishRelease.verification !== 'passed' && <div className="warning-callout"><AlertTriangle size={19} /><div><strong>此版本尚未验证</strong><p>组件允许以 Unverified 状态发布，下游会在通知中看到该风险。</p></div></div>}
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关场景 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <footer className="modal-actions"><button className="button button--quiet" onClick={() => setPublishRelease(undefined)}>取消</button><button disabled={busy} className="button button--primary" onClick={() => void confirmPublish()}>{busy ? '发布中…' : '确认发布并通知'}</button></footer>
      </Modal>}
    </div>
  );
}

const ACTIVE_IMAGE_BUILD_STATUSES = new Set(['queued', 'running']);

function ImageBuildModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { notify } = useApp();
  const [tag, setTag] = useState(release.version.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'latest');
  const [dockerfile, setDockerfile] = useState<File>();
  const [builds, setBuilds] = useState<ComponentImageBuild[]>([]);
  const [selectedBuild, setSelectedBuild] = useState<ComponentImageBuild>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    void api.imageBuilds(release.id).then((items) => {
      if (!active) return;
      setBuilds(items);
      setSelectedBuild(items[0]);
    }).catch((reason) => notify('error', '读取镜像构建记录失败', displayError(reason))).finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [notify, release.id]);

  useEffect(() => {
    if (!selectedBuild || !ACTIVE_IMAGE_BUILD_STATUSES.has(selectedBuild.status)) return;
    const controller = new AbortController();
    const timer = window.setInterval(() => {
      void api.imageBuild(selectedBuild.id, controller.signal).then((next) => {
        setSelectedBuild(next);
        setBuilds((current) => [next, ...current.filter((item) => item.id !== next.id)]);
        if (next.status === 'succeeded') notify('success', '镜像已发布', next.imageDigest ?? next.imageRef);
        if (next.status === 'failed' || next.status === 'interrupted') notify('error', '镜像构建失败', next.error ?? '请查看构建日志。');
      }).catch((reason) => {
        if (!(reason instanceof DOMException && reason.name === 'AbortError')) notify('error', '刷新镜像构建状态失败', displayError(reason));
      });
    }, 1000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [notify, selectedBuild?.id, selectedBuild?.status]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!dockerfile) {
      notify('error', '请选择 Dockerfile');
      return;
    }
    setBusy(true);
    try {
      const build = await api.startImageBuild(release.id, dockerfile, tag);
      setBuilds((current) => [build, ...current]);
      setSelectedBuild(build);
      notify('success', '镜像构建已提交', build.imageRef);
    } catch (reason) {
      notify('error', '提交镜像构建失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const logs = selectedBuild?.logs?.map((item) => `[${item.stream}] ${item.message}`).join('\n') ?? '';
  return <Modal title={`构建并发布镜像 · ${release.version}`} description="上传单个 Dockerfile；平台会自动构建并推送到 deploy 节点的指定仓库。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      <form onSubmit={(event) => void submit(event)}>
        <div className="warning-callout"><AlertTriangle size={19} /><div><strong>Dockerfile 会在平台构建机上执行</strong><p>构建上下文仅包含该 Dockerfile，不接受本地目录或主机路径；请只上传可信内容。</p></div></div>
        <div className="form-grid image-build-form">
          <label className="span-2"><span>Dockerfile</span><input type="file" required onChange={(event) => setDockerfile(event.target.files?.[0])} /><small>UTF-8，最大 1 MiB，必须包含 FROM 指令</small></label>
          <label className="span-2"><span>镜像标签</span><input value={tag} required pattern="[a-z0-9][a-z0-9._-]{0,127}" onChange={(event) => setTag(event.target.value.toLowerCase())} /><small>镜像路径由平台固定为 deploy 仓库 / components / 组件 slug : 标签</small></label>
        </div>
        <button disabled={busy || !dockerfile} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '提交中…' : '上传并构建'}</button>
      </form>
      <section className="image-build-results">
        <div className="image-build-history"><strong>构建记录</strong>{loading ? <span>读取中…</span> : builds.length ? builds.map((build) => <button key={build.id} type="button" className={selectedBuild?.id === build.id ? 'active' : ''} onClick={() => setSelectedBuild(build)}><span>{build.imageTag}</span><StatusPill status={build.status} /></button>) : <span>暂无构建记录</span>}</div>
        {selectedBuild ? <div className="image-build-detail">
          <div className="image-build-ref"><span>目标镜像</span><code>{selectedBuild.imageRef}</code>{selectedBuild.imageDigest ? <code>{selectedBuild.imageDigest}</code> : null}{selectedBuild.error ? <p>{selectedBuild.error}</p> : null}</div>
          <pre aria-label="镜像构建日志">{logs || (ACTIVE_IMAGE_BUILD_STATUSES.has(selectedBuild.status) ? '等待构建日志…' : '无日志')}</pre>
        </div> : <EmptyState title="尚未选择构建" description="上传 Dockerfile 后可在这里查看实时日志与镜像摘要。" />}
      </section>
    </div>
    <footer className="modal-actions"><button className="button button--quiet" type="button" onClick={onClose}>关闭</button></footer>
  </Modal>;
}

function ReleaseContractEditor({ release, components, focusSection, onCancel, onSaved }: { release: ComponentRelease; components: Component[]; focusSection?: ContractSection; onCancel: () => void; onSaved: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  const [parameters, setParameters] = useState<ParameterDefinition[]>(release.parameters ?? []);
  const [dependencies, setDependencies] = useState<ComponentDependency[]>(release.dependencies ?? []);
  const dependenciesRef = useRef<HTMLElement>(null);
  const parametersRef = useRef<HTMLElement>(null);
  const contractErrors = parameterContractErrors(parameters, dependencies, components.flatMap((item) => item.releases ?? []));
  useEffect(() => {
    const node = focusSection === 'parameters' ? parametersRef.current : focusSection === 'dependencies' ? dependenciesRef.current : null;
    if (!node) return;
    node.focus({ preventScroll: true });
    node.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
  }, [focusSection, release.id]);
  async function save() {
    if (contractErrors.length) {
      notify('error', '参数合同无效', contractErrors.join('；'));
      return;
    }
    setBusy(true);
    try {
      await api.updateReleaseContract(release.id, { parameters, dependencies });
      notify('success', '依赖和参数已保存', 'Draft 的直接依赖与参数合同已更新。');
      onSaved();
    } catch (reason) {
      notify('error', '保存失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="contract-panels contract-panels--editing">
      <article className="panel" id="contract-dependencies" ref={dependenciesRef} tabIndex={-1}>
        <header className="panel__header">
          <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{release.version} 可编辑的上游锁定和公开参数映射</p></div></div>
        </header>
        <div className="contract-editor">
          <DependencyEditor dependencies={dependencies} components={components} currentParameters={parameters} currentComponentId={release.componentId} onChange={setDependencies} />
        </div>
      </article>
      <article className="panel" id="contract-parameters" ref={parametersRef} tabIndex={-1}>
        <header className="panel__header">
          <div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>为每个参数选择内部或公开；公开参数可被下游引用</p></div></div>
        </header>
        <div className="contract-editor">
          <ParameterTable parameters={parameters} onChange={setParameters} />
        </div>
      </article>
      {contractErrors.length ? <div className="form-validation">{contractErrors.map((item) => <span key={item}>{item}</span>)}</div> : null}
      <div className="contract-editor-actions">
        <button type="button" className="button button--quiet" onClick={onCancel}>取消</button>
        <button type="button" className="button button--primary" disabled={busy} onClick={() => void save()}>{busy ? '保存中…' : '保存依赖和参数'}</button>
      </div>
    </div>
  );
}

const ACTION_OPTIONS: Array<{ value: ActionDefinition['type']; label: string }> = [
  { value: 'inspect', label: '检查' }, { value: 'preflight', label: '预检' },
  { value: 'install', label: '安装' }, { value: 'configure', label: '配置' },
  { value: 'upgrade', label: '升级' }, { value: 'verify', label: '验证' },
  { value: 'rollback', label: '回滚' }, { value: 'uninstall', label: '卸载' },
];

function PlaybookActionEditor({ releaseId, releases, actions, onChange, onDirtyChange }: { releaseId: string; releases: ComponentRelease[]; actions: ActionDefinition[]; onChange: Dispatch<SetStateAction<ActionDefinition[]>>; onDirtyChange: (dirty: boolean) => void }) {
  const { notify } = useApp();
  const [selected, setSelected] = useState(0);
  const [content, setContent] = useState('');
  const initialFilename = actions[0]?.playbook?.split('/').pop() || `${actions[0]?.type ?? 'install'}.yml`;
  const [filename, setFilename] = useState(initialFilename);
  const [savedEditor, setSavedEditor] = useState({ content: '', filename: initialFilename });
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const pendingSelection = useRef<number>();
  const loadRequest = useRef(0);
  const action = actions[selected];
  const releaseIds = new Set(releases.map((release) => release.id));
  const dirty = content !== savedEditor.content || filename !== savedEditor.filename;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  useEffect(() => {
    const pending = pendingSelection.current;
    if (pending !== undefined) {
      // The actions array is owned by the parent. Wait until the newly added
      // item is present before selecting it; clamping against the old array
      // can otherwise leave the previous tab visible while editing the new
      // action's Playbook content.
      if (pending < actions.length) {
        pendingSelection.current = undefined;
        setSelected(pending);
      }
      return;
    }
    if (selected < actions.length) return;
    setSelected(Math.max(0, actions.length - 1));
    setContent('');
    const fallback = actions.at(-1)?.playbook?.split('/').pop() || `${actions.at(-1)?.type ?? 'install'}.yml`;
    setFilename(fallback);
    setSavedEditor({ content: '', filename: fallback });
  }, [actions.length, selected]);

  function selectAction(index: number) {
    if (index === selected) return;
    if (dirty && !window.confirm('当前 Playbook 有未保存内容，确认放弃并切换动作？')) return;
    loadRequest.current += 1;
    setLoading(false);
    setSelected(index);
    setContent('');
    const path = actions[index]?.playbook;
    const nextFilename = path?.split('/').pop() || `${actions[index]?.type ?? 'install'}.yml`;
    setFilename(nextFilename);
    setSavedEditor({ content: '', filename: nextFilename });
  }

  function updateAction(patch: Partial<ActionDefinition>, index = selected) {
    // Playbook saves are asynchronous. Always merge into the latest parent
    // state so a completed save cannot replace actions added while it ran.
    onChange((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
  }

  function addAction() {
    if (dirty && !window.confirm('当前 Playbook 有未保存内容，确认放弃并新增动作？')) return;
    const next: ActionDefinition = { name: 'install', type: 'install', playbook: '', timeoutSeconds: 1800, risk: 'normal', riskLevel: 'low' };
    loadRequest.current += 1;
    setLoading(false);
    onChange((current) => {
      pendingSelection.current = current.length;
      return [...current, next];
    });
    const template = '---\n- name: Install component\n  hosts: all\n  become: true\n  tasks: []\n';
    setContent(template);
    setFilename('install.yml');
    setSavedEditor({ content: '', filename: 'install.yml' });
  }

  function removeAction() {
    if (!action || !window.confirm(`确认移除 ${action.type} 动作？托管文件不会立即删除。`)) return;
    onChange((current) => current.filter((_, index) => index !== selected));
    loadRequest.current += 1;
    setLoading(false);
    setSelected(Math.max(0, selected - 1));
    setContent('');
    const previous = actions[Math.max(0, selected - 1)];
    const previousFilename = previous?.playbook?.split('/').pop() || `${previous?.type ?? 'install'}.yml`;
    setFilename(previousFilename);
    setSavedEditor({ content: '', filename: previousFilename });
  }

  async function loadPlaybook() {
    if (!action?.playbook) return;
    const request = ++loadRequest.current;
    setLoading(true);
    try {
      const playbook = await api.playbook(releaseId, action.playbook);
      if (request !== loadRequest.current) return;
      setContent(playbook.content);
      setFilename(playbook.filename);
      setSavedEditor({ content: playbook.content, filename: playbook.filename });
    } catch (reason) {
      if (request !== loadRequest.current) return;
      notify('error', '读取 Playbook 失败', displayError(reason));
    } finally {
      if (request === loadRequest.current) setLoading(false);
    }
  }

  async function uploadPlaybook(file?: File) {
    if (!file) return;
    const actionIndex = selected;
    setSaving(true);
    try {
      const playbook = await api.uploadPlaybook(releaseId, file);
      setContent(playbook.content);
      setFilename(playbook.filename);
      updateAction({ playbook: playbook.path }, actionIndex);
      setSavedEditor({ content: playbook.content, filename: playbook.filename });
      notify('success', 'Playbook 已上传', '托管路径已自动绑定到当前动作；保存 Draft 后生效。');
    } catch (reason) {
      notify('error', '上传 Playbook 失败', displayError(reason));
    } finally {
      setSaving(false);
    }
  }

  async function savePlaybook() {
    if (!action || !content.trim()) {
      notify('error', 'Playbook 内容不能为空');
      return;
    }
    const actionIndex = selected;
    setSaving(true);
    try {
      const playbook = await api.savePlaybook(releaseId, filename, content);
      setFilename(playbook.filename);
      updateAction({ playbook: playbook.path }, actionIndex);
      setSavedEditor({ content, filename: playbook.filename });
      notify('success', 'Playbook 已保存', '托管路径已自动绑定到当前动作；保存 Draft 后生效。');
    } catch (reason) {
      notify('error', '保存 Playbook 失败', displayError(reason));
    } finally {
      setSaving(false);
    }
  }

  return <section className="playbook-editor">
    <header className="playbook-editor__header">
      <div><h3>Playbook 与生命周期动作</h3><p>上传 YAML 或直接在线编辑；每个 Draft 使用独立托管文件。</p></div>
      <button type="button" className="button button--quiet" disabled={saving} onClick={addAction}><Plus size={15} /> 新增动作</button>
    </header>
    {action ? <>
      <div className="playbook-action-tabs" role="tablist" aria-label="Ansible 动作">
        {actions.map((item, index) => <button key={`${item.type}-${index}`} type="button" disabled={saving} className={selected === index ? 'active' : ''} onClick={() => selectAction(index)}>{ACTION_OPTIONS.find((option) => option.value === item.type)?.label ?? item.type}</button>)}
      </div>
      <div className="form-grid playbook-action-fields">
        <label><span>动作类型</span><select value={action.type} onChange={(event) => {
          const type = event.target.value as ActionDefinition['type'];
          const previousType = action.type;
          updateAction({ type, name: !action.name || action.name === previousType ? type : action.name });
          if (filename === `${previousType}.yml` || filename === `${previousType}.yaml`) setFilename(`${type}.yml`);
        }}>{ACTION_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.value} · {option.label}</option>)}</select></label>
        <label><span>目标主机组</span><input value={action.hostGroup ?? ''} onChange={(event) => updateAction({ hostGroup: event.target.value })} placeholder="例如 k8snode" /></label>
        <label><span>超时（秒）</span><input type="number" min={1} value={action.timeoutSeconds ?? 1800} onChange={(event) => updateAction({ timeoutSeconds: Number(event.target.value) })} /></label>
        <label><span>风险级别</span><select value={action.riskLevel ?? (action.risk === 'destructive' ? 'destructive' : 'low')} onChange={(event) => { const riskLevel = event.target.value as NonNullable<ActionDefinition['riskLevel']>; updateAction({ riskLevel, risk: riskLevel === 'destructive' ? 'destructive' : 'normal', destructive: riskLevel === 'destructive' }); }}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <label className="span-2"><span>Playbook 路径</span><div className="inline-field"><input value={action.playbook} onChange={(event) => updateAction({ playbook: event.target.value })} placeholder="可填写服务器已有的相对路径，或在下方上传" /><button type="button" className="button button--quiet" disabled={!action.playbook || loading} onClick={() => void loadPlaybook()}>{loading ? '读取中…' : '载入编辑器'}</button></div></label>
        <label><span>Tags（逗号分隔）</span><input value={(action.tags ?? []).join(', ')} onChange={(event) => updateAction({ tags: splitCSV(event.target.value) })} /></label>
        <label><span>Limit 匹配</span><input value={action.limit ?? ''} onChange={(event) => updateAction({ limit: event.target.value })} placeholder="可选，例如 workers" /></label>
        <label><span>可用参数（逗号分隔）</span><input value={(action.allowedParameters ?? []).join(', ')} onChange={(event) => updateAction({ allowedParameters: splitCSV(event.target.value) })} /></label>
        <label className="span-2"><span>所需凭据名（逗号分隔）</span><input value={(action.requiredCredentials ?? []).join(', ')} onChange={(event) => updateAction({ requiredCredentials: splitCSV(event.target.value) })} /></label>
        {(action.type === 'upgrade' || action.type === 'rollback') ? <>
          <label><span>来源版本</span><select aria-label="来源 Release" value={action.fromReleaseId ?? ''} onChange={(event) => updateAction({ fromReleaseId: event.target.value || undefined })}><option value="">请选择来源版本</option>{action.fromReleaseId && !releaseIds.has(action.fromReleaseId) ? <option value={action.fromReleaseId}>{action.fromReleaseId} · 现有值</option> : null}{releases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.id}</option>)}</select></label>
          <label><span>目标版本</span><select aria-label="目标 Release" value={action.toReleaseId ?? ''} onChange={(event) => updateAction({ toReleaseId: event.target.value || undefined })}><option value="">请选择目标版本</option>{action.toReleaseId && !releaseIds.has(action.toReleaseId) ? <option value={action.toReleaseId}>{action.toReleaseId} · 现有值</option> : null}{releases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.id}</option>)}</select></label>
          {action.type === 'rollback' ? <small className="span-2">起止版本都留空时表示回退当前版本的安装；填写时表示从当前版本回到指定旧版本。</small> : null}
        </> : null}
      </div>
      <div className="playbook-source">
        <div className="playbook-source__toolbar">
          <label><Upload size={15} /><span>上传 .yml / .yaml</span><input type="file" accept=".yml,.yaml,text/yaml,application/x-yaml" onChange={(event) => { void uploadPlaybook(event.target.files?.[0]); event.currentTarget.value = ''; }} /></label>
          <input aria-label="Playbook 文件名" value={filename} onChange={(event) => setFilename(event.target.value)} pattern="[A-Za-z0-9][A-Za-z0-9._\-]*\.(yml|yaml)" />
          <span>{dirty ? '有未保存内容' : content ? '内容已保存' : '可上传或载入现有文件'}</span>
        </div>
        <textarea className="code-editor playbook-source__editor" aria-label="Playbook 在线编辑器" spellCheck={false} value={content} placeholder={'---\n- name: Install component\n  hosts: all\n  tasks: []'} onChange={(event) => setContent(event.target.value)} />
        <div className="playbook-source__actions"><button type="button" className="icon-text icon-text--danger" disabled={saving} onClick={removeAction}><Trash2 size={14} /> 移除动作</button><button type="button" className="button button--secondary" disabled={saving || !content.trim()} onClick={() => void savePlaybook()}><FileCode2 size={15} /> {saving ? '保存中…' : '保存 Playbook'}</button></div>
      </div>
    </> : <div className="playbook-editor__empty"><FileCode2 size={24} /><p>尚未配置生命周期动作。新增动作后即可上传或在线编写 Playbook。</p><button type="button" className="button button--secondary" onClick={addAction}><Plus size={15} /> 新增第一个动作</button></div>}
  </section>;
}

function splitCSV(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

function EditComponentModal({ component, onClose, onDone }: { component: Component; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.updateComponent(component.id, componentInput(form));
      notify('success', '组件信息已更新'); onDone();
    } catch (reason) { notify('error', '更新组件失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title={`编辑 ${component.name}`} description="名称和分类在这里改。参数公开/内部、以及依赖哪个上游参数，属于 Release 合同。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" defaultValue={component.name} required /></label><label><span>标识</span><input name="slug" defaultValue={component.slug} required pattern="[a-z0-9-]+" /></label><ClassificationFields component={component} /><label className="span-2"><span>说明</span><textarea name="description" defaultValue={component.description} rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存组件'}</button></footer></form></Modal>;
}

function CreateComponentModal({ onClose, onDone }: { onClose: () => void; onDone: (component: Component) => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true);
    const form = new FormData(event.currentTarget);
    try { const component = await api.createComponent(componentInput(form)); notify('success', '组件已创建', '继续创建首个 Draft，并上传或在线编写 Playbook。'); onDone(component); }
    catch (reason) { notify('error', '创建失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建组件" description="组件分类与 Release 的 atomic / bundle 交付类型相互独立。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" required placeholder="例如 containerd" /></label><label><span>标识</span><input name="slug" required pattern="[a-z0-9-]+" placeholder="containerd" /></label><ClassificationFields /><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建组件'}</button></footer></form></Modal>;
}

function componentInput(form: FormData): Partial<Component> {
  return {
    name: String(form.get('name')),
    slug: String(form.get('slug')),
    description: String(form.get('description')),
    layer: String(form.get('layer')) as ComponentLayer,
    category: String(form.get('category')) as Component['category'],
    kind: String(form.get('kind')) as ComponentKind,
    requiredness: String(form.get('requiredness')) as ComponentRequiredness,
  };
}

function ClassificationFields({ component }: { component?: Component }) {
  const [layer, setLayer] = useState<ComponentLayer>(component?.layer ?? COMPONENT_LAYERS[0].value);
  const definition = componentLayer(layer);
  const category = definition.categories.includes(component?.category ?? definition.categories[0]) ? component?.category : definition.categories[0];
  return <>
    <label><span>组件层级</span><select name="layer" value={layer} onChange={(event) => setLayer(event.target.value as ComponentLayer)}>{COMPONENT_LAYERS.map((item) => <option key={item.value} value={item.value}>{item.code} · {item.label}</option>)}</select></label>
    <label><span>能力类别</span><select key={layer} name="category" defaultValue={category}>{definition.categories.map((item) => <option key={item} value={item}>{COMPONENT_CATEGORY_LABELS[item]}</option>)}</select></label>
    <label><span>组件形态</span><select name="kind" defaultValue={component?.kind ?? 'software'}><option value="software">独立软件</option><option value="software_bundle">软件组合</option><option value="delivery_stage">交付阶段</option><option value="configuration">配置能力</option><option value="artifact_set">制品集合</option></select></label>
    <label><span>必选性</span><select name="requiredness" defaultValue={component?.requiredness ?? 'profile_required'}><option value="core_required">核心必选</option><option value="profile_required">方案必选</option><option value="optional">可选</option></select></label>
  </>;
}

function NewVersionModal({ component, baseRelease, onClose, onDone }: { component: Component; baseRelease?: ComponentRelease; onClose: () => void; onDone: (release?: ComponentRelease) => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  const source = baseRelease ?? component.latestRelease;
  const [constraints, setConstraints] = useState(() => parseConstraintSelection(source?.environmentConstraints));
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    const form = new FormData(event.currentTarget);
    const environmentConstraints = serializeConstraintSelection(constraints);
    const input = { version: String(form.get('version')), releaseNotes: String(form.get('notes')), breaking: form.get('breaking') === 'on', environmentConstraints };
    try {
      const release = source
        ? await api.cloneRelease(source.id, input)
        : await api.createRelease(component.id, { ...input, type: String(form.get('releaseType')) as ComponentRelease['type'], state: 'draft' });
      notify('success', 'Draft 已创建', '接下来为每个参数选择内部或公开，并映射上游公开参数。');
      onDone(release);
    } catch (reason) {
      notify('error', '创建版本失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }
  return <Modal size="wide" title={`更新 ${component.name}`} description={source ? `从 ${source.version} 克隆为新 Draft，可继续设置参数可见性和上游映射。` : '创建组件的首个 Draft Release。'} onClose={onClose}>
    <form onSubmit={(event) => void submit(event)}>
      <div className="form-grid">
        <label><span>新版本</span><input name="version" required placeholder="v1.1.0" /></label>
        <label><span>Release 类型</span><select name="releaseType" defaultValue={source?.type ?? (component.kind === 'software_bundle' ? 'bundle' : 'atomic')} disabled={Boolean(source)}><option value="atomic">atomic · 原子组件</option><option value="bundle">bundle · 组合组件</option></select></label>
        <label className="checkbox-field"><input name="breaking" type="checkbox" /><span>包含不兼容变更</span></label>
        <label className="span-2"><span>发布说明</span><textarea name="notes" required rows={4} placeholder="说明变化和下游注意事项" /></label>
        <div className="span-2"><EnvironmentConstraintEditor value={constraints} onChange={setConstraints} /></div>
      </div>
      <footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建 Draft'}</button></footer>
    </form>
  </Modal>;
}

function TestReleaseModal({ release, onClose, onDone }: { release: ComponentRelease; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp(); const { data: environments } = useApiData((signal) => api.environments(signal), [], 'environments');
  const { data: components } = useApiData((signal) => api.components(signal), [], 'components');
  const [environmentId, setEnvironmentId] = useState(''); const [busy, setBusy] = useState(false);
  const [runInputValues, setRunInputValues] = useState<Record<string, string>>({});
  const mapped = [...mappedParameterNames(release)];
  const [fixtureValues, setFixtureValues] = useState<Record<string, string>>({});
  useEffect(() => { setFixtureValues(defaultFixtureValues(release, components)); }, [components, release]);
  const primary = release.actions?.find((action) => action.type === 'upgrade') ?? release.actions?.find((action) => action.type === 'install');
  const declaredRunInputs = uniqueRunInputs(primary?.allowedParameters ?? []);
  async function run() {
    if (!environmentId) return; setBusy(true);
    try {
      const fixtures = Object.fromEntries(mapped.map((name) => {
        const raw = fixtureValues[name] ?? '';
        try { return [name, JSON.parse(raw)]; } catch { return [name, raw]; }
      }));
      await api.testRelease(release.id, environmentId, parseRunInput(declaredRunInputs, runInputValues), fixtures);
      notify('success', '组件测试已提交', mapped.length ? 'Fixture 只证明组件能消费参数，不能替代场景完整测试。' : '可以在运行中心查看 Ansible 日志。'); onDone();
    } catch (reason) { notify('error', '提交测试失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title={`测试 ${release.version}`} description="选择共享环境执行组件生命周期验证。" onClose={onClose}><div className="modal-body"><label><span>测试环境</span><select value={environmentId} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((env: Environment) => <option key={env.id} value={env.id}>{env.name} · {env.status ?? 'ready'}</option>)}</select></label>{mapped.length ? <div className="fixture-fields">{mapped.map((name) => <label key={name}><span>依赖 Fixture · {name}</span><input value={fixtureValues[name] ?? ''} onChange={(event) => setFixtureValues((current) => ({ ...current, [name]: event.target.value }))} /></label>)}</div> : null}<RunInputFields names={declaredRunInputs} values={runInputValues} onChange={(name, value) => setRunInputValues((current) => ({ ...current, [name]: value }))} /></div><footer className="modal-actions"><button className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={!environmentId || busy} onClick={() => void run()}><Beaker size={16} /> {busy ? '提交中…' : '开始测试'}</button></footer></Modal>;
}

function EditReleaseModal({ release, releases, onClose, onDone }: { release: ComponentRelease; releases: ComponentRelease[]; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const { data: components } = useApiData((signal) => api.components(signal), [], 'components');
  const [busy, setBusy] = useState(false);
  const [constraints, setConstraints] = useState(() => parseConstraintSelection(release.environmentConstraints));
  const [parameters, setParameters] = useState<ParameterDefinition[]>(release.parameters ?? []);
  const [dependencies, setDependencies] = useState<ComponentDependency[]>(release.dependencies ?? []);
  const [actions, setActions] = useState<ActionDefinition[]>(release.actions ?? []);
  const [playbookDirty, setPlaybookDirty] = useState(false);
  const contractErrors = parameterContractErrors(parameters, dependencies, components?.flatMap((item) => item.releases ?? []) ?? []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (playbookDirty) { notify('error', '请先保存 Playbook', '在线编辑器中仍有未保存内容。'); return; }
    setBusy(true); const form = new FormData(event.currentTarget);
    if (contractErrors.length) { notify('error', '参数合同无效', contractErrors.join('；')); setBusy(false); return; }
    try {
      await api.updateRelease(release.id, {
        ...release,
        version: String(form.get('version')),
        type: String(form.get('releaseType')) as ComponentRelease['type'],
        releaseNotes: String(form.get('notes')),
        breaking: form.get('breaking') === 'on',
        environmentConstraints: serializeConstraintSelection(constraints),
        parameters,
        dependencies,
        actions,
      });
      notify('success', 'Draft 配置已保存', '参数合同、依赖映射和 Ansible 动作已更新。'); onDone();
    } catch (reason) { notify('error', '保存 Draft 失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  const close = () => { if (!busy) onClose(); };
  return <Modal size="wide" title={`配置 Draft ${release.version}`} description="维护版本信息、参数合同、依赖映射，以及可上传和在线编辑的 Playbook。" onClose={close}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>版本</span><input name="version" defaultValue={release.version} required /></label><label><span>Release 类型</span><select name="releaseType" defaultValue={release.type ?? 'atomic'}><option value="atomic">atomic · 原子组件</option><option value="bundle">bundle · 组合组件</option></select></label><label className="checkbox-field"><input type="checkbox" name="breaking" defaultChecked={release.breaking} /><span>包含不兼容变更</span></label><label className="span-2"><span>发布说明</span><textarea name="notes" defaultValue={release.releaseNotes} rows={3} required /></label><div className="span-2"><EnvironmentConstraintEditor value={constraints} onChange={setConstraints} /></div><div className="span-2"><PlaybookActionEditor releaseId={release.id} releases={releases} actions={actions} onChange={setActions} onDirtyChange={setPlaybookDirty} /></div><div className="span-2 contract-section"><h3>参数合同</h3><p>公开参数会出现在下游的「上游公开参数」列表中；内部参数不会。</p><ParameterTable parameters={parameters} onChange={setParameters} /></div><div className="span-2 contract-section"><h3>精确依赖与公开参数映射</h3><p>先选上游组件版本，再把它的公开参数映射到本组件参数。</p><DependencyEditor dependencies={dependencies} components={components ?? []} currentParameters={parameters} currentComponentId={release.componentId} onChange={setDependencies} /></div>{contractErrors.length ? <div className="span-2 form-validation">{contractErrors.map((item) => <span key={item}>{item}</span>)}</div> : null}</div><footer className="modal-actions">{playbookDirty ? <span className="modal-actions__hint">请先保存 Playbook 内容</span> : null}<button type="button" className="button button--quiet" disabled={busy} onClick={close}>取消</button><button className="button button--primary" disabled={busy || playbookDirty}><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</button></footer></form></Modal>;
}

function InspectReleaseModal({ release, components, onClose, onEdit }: { release: ComponentRelease; components: Component[]; onClose: () => void; onEdit?: () => void }) {
  return <Modal size="wide" title={`${release.version} 参数合同`} description="查看每个参数的可见性，以及本组件引用了哪个上游组件的哪个公开参数。" onClose={onClose}>
    <div className="modal-body inspect-contract">
      <section className="contract-section">
        <h3>适配环境</h3>
        <EnvironmentConstraints constraints={release.environmentConstraints} />
      </section>
      <ParameterContractList release={release} components={components} />
      <section className="contract-section">
        <h3>依赖映射</h3>
        <DependencyContractList dependencies={release.dependencies ?? []} components={components} />
      </section>
    </div>
    <footer className="modal-actions">
      <button type="button" className="button button--quiet" onClick={onClose}>关闭</button>
      {onEdit && <button type="button" className="button button--primary" onClick={onEdit}><PencilLine size={15} /> 编辑可见性与映射</button>}
    </footer>
  </Modal>;
}

function publicCount(release: ComponentRelease) {
  return (release.parameters ?? []).filter((item) => item.visibility === 'public').length;
}

function EnvironmentConstraints({ constraints }: { constraints?: Record<string, unknown> }) {
  const groups = environmentConstraintGroups(constraints);
  if (!groups.length) return <div className="env-constraints env-constraints--empty">未声明适配环境</div>;
  return <div className="env-constraints">{groups.map((group) => (
    <span className="env-constraint" key={group.key}>
      <em>{group.label}</em>
      {group.values.map((value) => <b key={value}>{value}</b>)}
    </span>
  ))}</div>;
}

function SaveIcon() { return <PencilLine size={15} />; }
