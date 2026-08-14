import { useMemo, useState, type FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, ArrowUpRight, Beaker, Boxes, GitBranch, PencilLine, Plus, Rocket, Shield, UserRound } from 'lucide-react';
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
import type { Component, ComponentKind, ComponentLayer, ComponentRelease, ComponentRequiredness, Environment, ImpactPreview } from '../types/domain';

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
  const [busy, setBusy] = useState(false);

  const selected = useMemo(
    () => components?.find((item) => item.id === selectedId) ?? components?.[0],
    [components, selectedId],
  );
  const releases = selected?.releases?.length ? selected.releases : selected?.latestRelease ? [selected.latestRelease] : [];
  const mine = selected?.ownerId === user.id && user.role === 'component_owner';
  const canTest = mine || user.role === 'environment_owner';
  const layeredComponents = COMPONENT_LAYERS.map((layer) => ({
    layer,
    components: components?.filter((component) => component.layer === layer.value) ?? [],
  }));

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
            <div className="catalog-list__header"><strong>组件目录</strong><span>{components?.length ?? 0}</span></div>
            {components?.length ? layeredComponents.map(({ layer, components: layerComponents }) => <section className="catalog-layer" key={layer.value}>
              <header><span>{layer.code}</span><div><strong>{layer.label}</strong><small>{layerComponents.length} 个组件</small></div></header>
              {layerComponents.length ? layerComponents.map((component) => (
                <button key={component.id} className={`catalog-item${selected?.id === component.id ? ' active' : ''}`} onClick={() => setSearchParams({ selected: component.id })}>
                  <span className="catalog-item__icon"><Boxes size={18} /></span>
                  <span><strong>{component.name}</strong><small>{COMPONENT_CATEGORY_LABELS[component.category]} · {COMPONENT_KIND_LABELS[component.kind]}</small></span>
                  {component.ownerId === user.id && <span className="mine-dot" title="我负责的组件" />}
                </button>
              )) : <div className="catalog-layer__empty">本层暂无组件</div>}
            </section>) : <EmptyState title="暂无组件" description="组件 Owner 可以创建第一个组件。" />}
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
              {mine && <div className="row-actions"><button className="button button--quiet" onClick={() => setEditComponent(selected)}><PencilLine size={16} /> 编辑组件</button><button className="button button--secondary" onClick={() => setVersionBase(selected)}><Plus size={16} /> 创建新版本</button></div>}
            </article>

            <article className="panel">
              <header className="panel__header"><div><span className="panel__icon"><Rocket size={18} /></span><div><h2>发布历史</h2><p>Released 版本不可修改，更新将产生新 Draft</p></div></div></header>
              {releases.length ? <div className="release-table">
                <div className="release-table__head"><span>版本</span><span>验证</span><span>依赖 / 动作</span><span>发布时间</span><span /></div>
                {releases.map((release) => <div key={release.id} className="release-row">
                  <div><strong>{release.version}</strong>{release.breaking && <span className="breaking-badge">BREAKING</span>}<small>{release.releaseNotes ?? '未填写发布说明'}</small></div>
                  <div><StatusPill status={release.verification ?? (release.verified ? 'passed' : 'unverified')} /></div>
                  <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖</span><span>{release.actions?.length ?? 0} 个动作</span></div>
                  <div>{formatTime(release.releasedAt ?? release.createdAt)}</div>
                  <div className="row-actions">
                    {canTest && <button className="icon-text" onClick={() => setTestRelease(release)}><Beaker size={15} /> 测试</button>}
                    {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setEditRelease(release)}><PencilLine size={15} /> 配置</button>}
                    {mine && release.state === 'draft' && <button className="icon-text icon-text--primary" onClick={() => void previewPublish(release)}><Rocket size={15} /> 发布</button>}
                    {mine && release.state === 'released' && <button className="icon-text" onClick={() => void deprecate(release)}>废弃</button>}
                  </div>
                </div>)}
              </div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。" />}
            </article>

            <div className="two-column">
              <article className="panel">
                <header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>锁定精确 Release ID</p></div></div></header>
                {(selected.latestRelease?.dependencies ?? []).length ? <div className="dependency-list">{selected.latestRelease!.dependencies!.map((dep) => <div key={`${dep.componentId}-${dep.releaseId}`}><span className="dependency-dot" /><div><strong>{dep.componentName ?? dep.componentId}</strong><small>{dep.version ?? dep.releaseId} · {dep.purpose ?? 'runtime dependency'}</small></div><ArrowUpRight size={15} /></div>)}</div> : <EmptyState title="没有直接依赖" />}
              </article>
              <article className="panel">
                <header className="panel__header"><div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>环境约束</h2><p>执行前由 Planner 校验</p></div></div></header>
                <pre className="json-preview">{JSON.stringify(selected.latestRelease?.environmentConstraints ?? { arch: ['amd64'], network: ['ipv4'] }, null, 2)}</pre>
              </article>
            </div>
          </section> : <section className="panel"><EmptyState title="请选择组件" /></section>}
        </div>
        </>
      )}

      {createOpen && <CreateComponentModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('components'); }} />}
      {editComponent && <EditComponentModal component={editComponent} onClose={() => setEditComponent(undefined)} onDone={() => { setEditComponent(undefined); signalRefresh('components'); }} />}
      {versionBase && <NewVersionModal component={versionBase} onClose={() => setVersionBase(undefined)} onDone={() => { setVersionBase(undefined); signalRefresh('components'); }} />}
      {editRelease && <EditReleaseModal release={editRelease} onClose={() => setEditRelease(undefined)} onDone={() => { setEditRelease(undefined); signalRefresh('components'); }} />}
      {testRelease && <TestReleaseModal release={testRelease} onClose={() => setTestRelease(undefined)} onDone={() => { setTestRelease(undefined); signalRefresh(['components', 'runs']); }} />}
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
  return <Modal title={`编辑 ${component.name}`} description="分类用于展示和编排提示，不改变 Release 依赖或 DAG 顺序。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" defaultValue={component.name} required /></label><label><span>标识</span><input name="slug" defaultValue={component.slug} required pattern="[a-z0-9-]+" /></label><ClassificationFields component={component} /><label className="span-2"><span>说明</span><textarea name="description" defaultValue={component.description} rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存组件'}</button></footer></form></Modal>;
}

function CreateComponentModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true);
    const form = new FormData(event.currentTarget);
    try { await api.createComponent(componentInput(form)); notify('success', '组件已创建', '现在可以创建首个 Draft 版本。'); onDone(); }
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

function NewVersionModal({ component, onClose, onDone }: { component: Component; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp(); const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget); const input = { version: String(form.get('version')), releaseNotes: String(form.get('notes')), breaking: form.get('breaking') === 'on' }; try { if (component.latestRelease) await api.cloneRelease(component.latestRelease.id, input); else await api.createRelease(component.id, { ...input, type: String(form.get('releaseType')) as ComponentRelease['type'], state: 'draft' }); notify('success', 'Draft 已创建', component.latestRelease ? '依赖、类型和动作已从上一版本复制。' : '请继续配置依赖和 Ansible 动作。'); onDone(); } catch (reason) { notify('error', '创建版本失败', displayError(reason)); } finally { setBusy(false); } }
  return <Modal title={`更新 ${component.name}`} description={component.latestRelease ? '从当前版本克隆为新 Draft，Released 版本保持不变。' : '创建组件的首个 Draft Release。'} onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>新版本</span><input name="version" required placeholder="v1.1.0" /></label><label><span>Release 类型</span><select name="releaseType" defaultValue={component.latestRelease?.type ?? (component.kind === 'software_bundle' ? 'bundle' : 'atomic')} disabled={Boolean(component.latestRelease)}><option value="atomic">atomic · 原子组件</option><option value="bundle">bundle · 组合组件</option></select></label><label className="checkbox-field"><input name="breaking" type="checkbox" /><span>包含不兼容变更</span></label><label className="span-2"><span>发布说明</span><textarea name="notes" required rows={4} placeholder="说明变化和下游注意事项" /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建 Draft'}</button></footer></form></Modal>;
}

function TestReleaseModal({ release, onClose, onDone }: { release: ComponentRelease; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp(); const { data: environments } = useApiData((signal) => api.environments(signal), [], 'environments'); const [environmentId, setEnvironmentId] = useState(''); const [busy, setBusy] = useState(false);
  const [runInputValues, setRunInputValues] = useState<Record<string, string>>({});
  const primary = release.actions?.find((action) => action.type === 'upgrade') ?? release.actions?.find((action) => action.type === 'install');
  const declaredRunInputs = uniqueRunInputs(primary?.allowedParameters ?? []);
  async function run() { if (!environmentId) return; setBusy(true); try { await api.testRelease(release.id, environmentId, parseRunInput(declaredRunInputs, runInputValues)); notify('success', '组件测试已提交', '可以在运行中心查看 Ansible 日志。'); onDone(); } catch (reason) { notify('error', '提交测试失败', displayError(reason)); } finally { setBusy(false); } }
  return <Modal title={`测试 ${release.version}`} description="选择共享环境执行组件生命周期验证。" onClose={onClose}><div className="modal-body"><label><span>测试环境</span><select value={environmentId} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((env: Environment) => <option key={env.id} value={env.id}>{env.name} · {env.status ?? 'ready'}</option>)}</select></label><RunInputFields names={declaredRunInputs} values={runInputValues} onChange={(name, value) => setRunInputValues((current) => ({ ...current, [name]: value }))} /></div><footer className="modal-actions"><button className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={!environmentId || busy} onClick={() => void run()}><Beaker size={16} /> {busy ? '提交中…' : '开始测试'}</button></footer></Modal>;
}

function EditReleaseModal({ release, onClose, onDone }: { release: ComponentRelease; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  const [constraints, setConstraints] = useState(JSON.stringify(release.environmentConstraints ?? {}, null, 2));
  const [schema, setSchema] = useState(JSON.stringify(release.parameterSchema ?? {}, null, 2));
  const [dependencies, setDependencies] = useState(JSON.stringify(release.dependencies ?? [], null, 2));
  const [actions, setActions] = useState(JSON.stringify(release.actions ?? [], null, 2));
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.updateRelease(release.id, {
        ...release,
        version: String(form.get('version')),
        type: String(form.get('releaseType')) as ComponentRelease['type'],
        releaseNotes: String(form.get('notes')),
        breaking: form.get('breaking') === 'on',
        environmentConstraints: JSON.parse(constraints),
        parameterSchema: JSON.parse(schema),
        dependencies: JSON.parse(dependencies),
        actions: JSON.parse(actions),
      });
      notify('success', 'Draft 配置已保存', '依赖、Schema 和 Ansible 动作已更新。'); onDone();
    } catch (reason) { notify('error', '保存 Draft 失败', reason instanceof SyntaxError ? '约束、Schema 和动作必须是有效 JSON。' : displayError(reason)); }
    finally { setBusy(false); }
  }
  return <Modal title={`配置 Draft ${release.version}`} description="配置类型、依赖、参数 Schema、环境约束及 Ansible 生命周期动作。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>版本</span><input name="version" defaultValue={release.version} required /></label><label><span>Release 类型</span><select name="releaseType" defaultValue={release.type ?? 'atomic'}><option value="atomic">atomic · 原子组件</option><option value="bundle">bundle · 组合组件</option></select></label><label className="checkbox-field"><input type="checkbox" name="breaking" defaultChecked={release.breaking} /><span>包含不兼容变更</span></label><label className="span-2"><span>发布说明</span><textarea name="notes" defaultValue={release.releaseNotes} rows={3} required /></label><label><span>环境约束 JSON</span><textarea className="code-editor code-editor--small" value={constraints} onChange={(event) => setConstraints(event.target.value)} /></label><label><span>参数 Schema JSON</span><textarea className="code-editor code-editor--small" value={schema} onChange={(event) => setSchema(event.target.value)} /></label><label className="span-2"><span>精确依赖 JSON（componentId、releaseId、purpose）</span><textarea className="code-editor code-editor--small" value={dependencies} onChange={(event) => setDependencies(event.target.value)} /></label><label className="span-2"><span>Ansible 动作 JSON（kind/type、playbook、hostGroup、timeoutSeconds）</span><textarea className="code-editor code-editor--small" value={actions} onChange={(event) => setActions(event.target.value)} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</button></footer></form></Modal>;
}

function SaveIcon() { return <PencilLine size={15} />; }
