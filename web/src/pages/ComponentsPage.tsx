import { useEffect, useMemo, useRef, useState, type Dispatch, type FormEvent, type SetStateAction } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { AlertTriangle, Archive, Beaker, Boxes, CheckCircle2, ChevronDown, ChevronRight, CircleDashed, Container, ExternalLink, FileCode2, Filter, GitBranch, PencilLine, Plus, Rocket, Search, Shield, Trash2, Upload, UserRound } from 'lucide-react';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { parseRunInput, RunInputFields, uniqueRunInputs } from '../components/RunInputFields';
import { RunInputPresetPicker } from '../components/RunInputPresetPicker';
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
import { parseComponentImportTemplate } from './componentTemplateImport';
import type { ActionDefinition, Component, ComponentArtifact, ComponentDependency, ComponentImageBuild, ComponentKind, ComponentLayer, ComponentRelease, ComponentRequiredness, ComponentTestPlan, ComponentTestRequest, Environment, ImpactPreview, ParameterDefinition, PlaybookFile, Run, WorkExplanation } from '../types/domain';

type ContractSection = 'dependencies' | 'parameters';
type ContractEditIntent = ContractSection | 'all';
type CatalogFilter = 'all' | 'mine' | 'draft' | 'attention';
const REQUIRED_LIFECYCLE_ACTIONS: Array<{ type: ActionDefinition['type']; label: string }> = [
  { type: 'install', label: '安装' },
  { type: 'verify', label: '验证' },
  { type: 'rollback', label: '回退' },
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

const PRIMARY_ACTIONS = new Set<ActionDefinition['type']>(['upgrade', 'install', 'configure', 'preflight', 'inspect']);

function releaseIsVerified(release: ComponentRelease) {
  return release.verified === true;
}

function runHasOrderedVerification(run: Run, primary: (action: ActionDefinition['type']) => boolean) {
  let primarySeen = false;
  for (const step of run.steps ?? []) {
    const action = step.action as ActionDefinition['type'];
    if (primary(action)) primarySeen = true;
    else if (primarySeen && action === 'verify') return true;
  }
  return false;
}

function newestRun(runs: Run[], predicate: (run: Run) => boolean) {
  return runs.filter(predicate).sort((left, right) => Date.parse(right.finishedAt ?? right.createdAt ?? '') - Date.parse(left.finishedAt ?? left.createdAt ?? ''))[0];
}

function componentNeedsAttention(component: Component) {
  const releases = component.releases?.length ? component.releases : component.latestRelease ? [component.latestRelease] : [];
  const draft = releases.find((release) => release.state === 'draft');
  const release = draft ?? component.latestRelease ?? releases[0];
  if (!release) return true;
  return Boolean(draft) || !releaseIsVerified(release) || lifecycleSummary(release.actions).completed < REQUIRED_LIFECYCLE_ACTIONS.length;
}

function releaseEvidence(release: ComponentRelease, runs: Run[], mode: 'install' | 'rollback') {
  return newestRun(runs, (run) => run.kind === 'component_test'
    && run.componentReleaseId === release.id
    && (mode === 'rollback'
      ? runHasOrderedVerification(run, (action) => action === 'rollback')
      : runHasOrderedVerification(run, (action) => PRIMARY_ACTIONS.has(action))));
}

function releaseReadyForPublish(release: ComponentRelease, runs: Run[]) {
  const lifecycle = lifecycleSummary(release.actions);
  const install = releaseEvidence(release, runs, 'install');
  const rollback = releaseEvidence(release, runs, 'rollback');
  return lifecycle.completed === lifecycle.total
    && releaseIsVerified(release)
    && install?.status === 'succeeded'
    && rollback?.status === 'succeeded';
}

function EvidenceLink({ run, stale = false }: { run?: Run; stale?: boolean }) {
  if (!run) return <small className="verification-evidence verification-evidence--missing">暂无 Run 证据</small>;
  return <Link className={`verification-evidence${run.status === 'succeeded' && !stale ? ' verification-evidence--passed' : ' verification-evidence--failed'}`} to={`/runs?selected=${run.id}`} onClick={(event) => event.stopPropagation()}>
    {run.environmentName ?? '未知环境'} · {formatTime(run.finishedAt ?? run.createdAt)}
    {stale ? ' · 合同已变更' : ''}
    <ExternalLink size={12} aria-hidden="true" />
  </Link>;
}

function DraftReadiness({ release, runs, onContract, onLifecycle, onImage, onArtifact, onValidate, onPublish }: {
  release: ComponentRelease;
  runs: Run[];
  onContract: () => void;
  onLifecycle: () => void;
  onImage: () => void;
  onArtifact: () => void;
  onValidate: () => void;
  onPublish: () => void;
}) {
  const lifecycle = lifecycleSummary(release.actions);
  const install = releaseEvidence(release, runs, 'install');
  const rollback = releaseEvidence(release, runs, 'rollback');
  const checks = [
    true,
    lifecycle.completed === lifecycle.total,
    releaseIsVerified(release) && install?.status === 'succeeded',
    releaseIsVerified(release) && rollback?.status === 'succeeded',
  ];
  const complete = checks.filter(Boolean).length;
  const ready = complete === checks.length;
  return <article className="panel readiness-panel" aria-label={`Draft ${release.version} 发布就绪度`}>
    <header className="readiness-header">
      <div><span className="panel__icon"><CheckCircle2 size={18} /></span><div><h2>Draft 发布就绪度</h2><p>{release.version} · 按合同、生命周期、安装验证和回退验证逐项闭环</p></div></div>
      <div className={`readiness-score${ready ? ' readiness-score--ready' : ''}`}><strong>{complete}/{checks.length}</strong><span>{ready ? '可以发布' : '仍有阻断项'}</span></div>
    </header>
    <div className="readiness-progress" aria-label={`已完成 ${complete} 项，共 ${checks.length} 项`}><span style={{ width: `${complete / checks.length * 100}%` }} /></div>
    <div className="readiness-steps">
      <section className="readiness-step readiness-step--done"><CheckCircle2 size={18} /><div><strong>Release 合同</strong><p>{release.dependencies?.length ?? 0} 项依赖 · {release.parameters?.length ?? 0} 个参数</p></div><button className="icon-text" onClick={onContract}>编辑合同</button></section>
      <section className={lifecycle.completed === lifecycle.total ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{lifecycle.completed === lifecycle.total ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>生命周期动作</strong><p>{lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</p></div><button className="icon-text" onClick={onLifecycle}>配置动作</button></section>
      <section className={releaseIsVerified(release) && install?.status === 'succeeded' ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{releaseIsVerified(release) && install?.status === 'succeeded' ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>安装与验证</strong><EvidenceLink run={install} stale={!releaseIsVerified(release)} /></div><button className="icon-text" onClick={onValidate}>环境验证</button></section>
      <section className={releaseIsVerified(release) && rollback?.status === 'succeeded' ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{releaseIsVerified(release) && rollback?.status === 'succeeded' ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>回退与回退后验证</strong><EvidenceLink run={rollback} stale={!releaseIsVerified(release)} /></div><button className="icon-text" onClick={onValidate}>环境验证</button></section>
    </div>
    <footer className="readiness-actions">
      <div><span>可选交付物：</span><button className="icon-text" onClick={onImage}><Container size={14} /> 镜像构建</button><button className="icon-text" onClick={onArtifact}><Archive size={14} /> 管理介质</button></div>
      <div><span>{ready ? '发布前将展示完整下游影响。' : '完成所有阻断项后才能发布。'}</span><button className="button button--primary" disabled={!ready} title={ready ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={onPublish}><Rocket size={15} /> 预览影响并发布</button></div>
    </footer>
  </article>;
}

export function ComponentsPage() {
  const { user, notify, signalRefresh } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: components, loading, error, isRefreshing, reload } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const { data: runs } = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench');
  const selectedId = searchParams.get('selected') ?? undefined;
  const selectedReleaseId = searchParams.get('release') ?? undefined;
  const deepLinkAction = searchParams.get('action') ?? undefined;
  const handledDeepLink = useRef<string>();
  const [catalogSearch, setCatalogSearch] = useState('');
  const [catalogFilter, setCatalogFilter] = useState<CatalogFilter>('all');
  const [createOpen, setCreateOpen] = useState(false);
  const [componentImportOpen, setComponentImportOpen] = useState(false);
  const [versionBase, setVersionBase] = useState<Component>();
  const [editComponent, setEditComponent] = useState<Component>();
  const [publishRelease, setPublishRelease] = useState<ComponentRelease>();
  const [deprecateRelease, setDeprecateRelease] = useState<ComponentRelease>();
  const [editRelease, setEditRelease] = useState<ComponentRelease>();
  const [impact, setImpact] = useState<ImpactPreview>();
  const [operationExplanation, setOperationExplanation] = useState<WorkExplanation>();
  const [testRelease, setTestRelease] = useState<ComponentRelease>();
  const [inspectRelease, setInspectRelease] = useState<ComponentRelease>();
  const [imageRelease, setImageRelease] = useState<ComponentRelease>();
  const [artifactRelease, setArtifactRelease] = useState<ComponentRelease>();
  const [contractReleaseId, setContractReleaseId] = useState<string>();
  const [editingContract, setEditingContract] = useState(false);
  const [contractFocus, setContractFocus] = useState<ContractSection>();
  const [contractDraftIntent, setContractDraftIntent] = useState<ContractEditIntent>();
  const [pendingContractRelease, setPendingContractRelease] = useState<ComponentRelease>();
  const [layerOpen, setLayerOpen] = useState<Partial<Record<ComponentLayer, boolean>>>({});
  const [busy, setBusy] = useState(false);

  const selected = useMemo(
    () => selectedId ? components?.find((item) => item.id === selectedId) : components?.[0],
    [components, selectedId],
  );
  const releases = selected?.releases?.length ? selected.releases : selected?.latestRelease ? [selected.latestRelease] : [];
  const contractRelease = releases.find((release) => release.id === (contractReleaseId ?? selectedReleaseId))
    ?? (pendingContractRelease?.id === contractReleaseId ? pendingContractRelease : undefined)
    ?? defaultContractRelease(releases, selected?.latestRelease);
  const editableDraft = releases.find((release) => release.state === 'draft');
  const releaseWorkItem = workbench?.items.find((item) => item.subject.type === 'component_release' && item.subject.id === contractRelease?.id);
  const visibleEditRelease = editRelease?.componentId === selected?.id ? editRelease : undefined;
  const showContractEditor = Boolean(editingContract && contractRelease?.state === 'draft');
  const deprecationScenarioRunCount = deprecateRelease ? impact?.scenarioRunCount ?? 0 : 0;
  const deprecationBlocked = deprecationScenarioRunCount > 0;
  const mine = selected?.ownerId === user.id && user.role === 'component_owner';
  const canTest = mine || user.role === 'environment_owner';
  useEffect(() => {
    setContractReleaseId(selectedReleaseId);
  }, [selectedReleaseId]);
  useEffect(() => {
    setContractReleaseId(undefined);
    setEditingContract(false);
    setContractFocus(undefined);
    setContractDraftIntent(undefined);
    setPendingContractRelease(undefined);
    setEditComponent(undefined);
    setPublishRelease(undefined);
    setDeprecateRelease(undefined);
    setImpact(undefined);
    setTestRelease(undefined);
    setInspectRelease(undefined);
    setEditRelease(undefined);
    setImageRelease(undefined);
    setArtifactRelease(undefined);
  }, [selected?.id]);
  useEffect(() => {
    if (!deepLinkAction) {
      handledDeepLink.current = undefined;
      return;
    }
    if (!selected || !contractRelease) return;
    const key = `${selected.id}:${contractRelease.id}:${deepLinkAction}`;
    if (handledDeepLink.current === key) return;
    handledDeepLink.current = key;
    if (deepLinkAction === 'validate' && canTest) setTestRelease(contractRelease);
    if (deepLinkAction === 'publish' && mine) void previewPublish(contractRelease);
    if (deepLinkAction === 'contract' && mine) startEditingContract();
    if (deepLinkAction === 'lifecycle' && mine && contractRelease.state === 'draft') setEditRelease(contractRelease);
    const next = new URLSearchParams(searchParams);
    next.delete('action');
    setSearchParams(next, { replace: true });
  }, [canTest, contractRelease, deepLinkAction, mine, searchParams, selected, setSearchParams]);
  function selectContractRelease(id: string) {
    const release = releases.find((item) => item.id === id);
    setContractReleaseId(id);
    if (selected) setSearchParams({ selected: selected.id, release: id });
    if (pendingContractRelease?.id !== id) setPendingContractRelease(undefined);
    if (release?.state !== 'draft') {
      setEditingContract(false);
      setContractFocus(undefined);
    }
  }
  function startEditingContract(section?: ContractSection) {
    if (!selected || !mine) return;
    if (contractRelease?.state === 'draft') {
      setPendingContractRelease(undefined);
      setEditingContract(true);
      setContractFocus(section);
      return;
    }
    if (editableDraft) {
      setPendingContractRelease(undefined);
      setContractReleaseId(editableDraft.id);
      setEditingContract(true);
      setContractFocus(section);
      notify('info', '已切换到可编辑 Draft', `${contractRelease?.version ?? '当前版本'} 已不可修改，正在编辑 ${editableDraft.version}。`);
      return;
    }
    setContractFocus(section);
    setContractDraftIntent(section ?? 'all');
    setVersionBase(selected);
  }
  const filteredComponents = useMemo(() => {
    const query = catalogSearch.trim().toLocaleLowerCase();
    return (components ?? []).filter((component) => {
      const matchesQuery = !query || `${component.name} ${component.slug ?? ''}`.toLocaleLowerCase().includes(query);
      if (!matchesQuery) return false;
      if (catalogFilter === 'mine') return component.ownerId === user.id;
      if (catalogFilter === 'draft') return component.ownerId === user.id && component.releases?.some((release) => release.state === 'draft');
      if (catalogFilter === 'attention') return component.ownerId === user.id && componentNeedsAttention(component);
      return true;
    });
  }, [catalogFilter, catalogSearch, components, user.id]);
  const layeredComponents = COMPONENT_LAYERS.map((layer) => ({
    layer,
    components: filteredComponents.filter((component) => component.layer === layer.value),
  }));
  const selectedInCatalog = Boolean(selected && filteredComponents.some((component) => component.id === selected.id));
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
    setDeprecateRelease(undefined);
    setImpact(undefined);
    setOperationExplanation(undefined);
    try {
      setImpact(await api.releaseImpact(release.id));
    } catch (reason) {
      notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function previewDeprecate(release: ComponentRelease) {
    setDeprecateRelease(release);
    setPublishRelease(undefined);
    setImpact(undefined);
    setOperationExplanation(undefined);
    try {
      setImpact(await api.releaseImpact(release.id));
    } catch (reason) {
      notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function confirmPublish() {
    if (!publishRelease) return;
    if (!releaseReadyForPublish(publishRelease, runs ?? [])) {
      notify('error', '暂不能发布', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    setBusy(true);
    setOperationExplanation(undefined);
    try {
      await api.publishRelease(publishRelease.id);
      notify('success', '组件版本已发布', '下游 Owner 的站内影响通知已生成。');
      setPublishRelease(undefined);
      signalRefresh(['components', 'notifications', 'workbench']);
    } catch (reason) {
      setOperationExplanation(actionableExplanation(reason));
      notify('error', '发布失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function toggleCandidate(release: ComponentRelease) {
    if (!release.candidate && !releaseReadyForPublish(release, runs ?? [])) {
      notify('error', '暂不能加入候选集', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    setBusy(true);
    try {
      await api.setReleaseCandidate(release.id, !release.candidate);
      notify('success', release.candidate ? '已撤回候选版本' : '已加入候选发布集', release.candidate ? '场景 Owner 将不再能新引用此 Draft。' : '场景 Owner 现在可以编排和测试；发布场景时将原子发布全部候选版本。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '候选状态更新失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function confirmDeprecate() {
    if (!deprecateRelease) return;
    if (deprecationBlocked) {
      notify('error', '无法废弃', `该版本已经被 ${deprecationScenarioRunCount} 个场景 Run 锁定。`);
      return;
    }
    setBusy(true);
    setOperationExplanation(undefined);
    try {
      await api.deprecateRelease(deprecateRelease.id);
      notify('success', deprecateRelease.state === 'draft' ? '组件草稿已废弃' : '组件版本已废弃', '历史 Run、Playbook、介质和已经锁定此 Release ID 的场景保持不变。');
      setDeprecateRelease(undefined);
      signalRefresh(['components', 'notifications']);
    } catch (reason) {
      setOperationExplanation(actionableExplanation(reason));
      notify('error', '废弃失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Component registry"
        title="组件中心"
        description="组件 Owner 在这里维护不可变发布、依赖关系和 Ansible 生命周期动作。"
        actions={user.role === 'component_owner' ? <><button className="button button--quiet" onClick={() => setComponentImportOpen(true)}><Upload size={16} /> 批量导入</button><button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建组件</button></> : undefined}
      />
      {loading && !components ? <LoadingBlock label="正在读取组件目录…" /> : error && !components ? <ErrorBlock message={error} onRetry={() => void reload()} /> : (
        <>
        <RefreshNotice loading={isRefreshing} error={components ? error : undefined} onRetry={() => void reload()} />
        <div className="catalog-layout">
          <aside className="catalog-list panel">
            <div className="catalog-list__header">
              <strong>组件目录</strong>
              <div className="catalog-list__tools">
                <span>{filteredComponents.length}/{components?.length ?? 0}</span>
                <button type="button" className="icon-text" onClick={() => setAllLayers(true)}>全部展开</button>
                <button type="button" className="icon-text" onClick={() => setAllLayers(false)}>全部折叠</button>
              </div>
            </div>
            <div className="catalog-search">
              <Search size={15} aria-hidden="true" />
              <input aria-label="搜索组件" value={catalogSearch} onChange={(event) => setCatalogSearch(event.target.value)} placeholder="搜索名称或标识" />
            </div>
            <div className="catalog-filters" aria-label="组件目录筛选">
              <Filter size={14} aria-hidden="true" />
              {([
                ['all', '全部'],
                ['mine', '我负责的'],
                ['draft', '有 Draft'],
                ['attention', '待处理'],
              ] as Array<[CatalogFilter, string]>).map(([value, label]) => <button key={value} type="button" className={catalogFilter === value ? 'active' : ''} aria-pressed={catalogFilter === value} onClick={() => setCatalogFilter(value)}>{label}</button>)}
            </div>
            {components?.length && filteredComponents.length ? layeredComponents.map(({ layer, components: layerComponents }) => {
              if ((catalogSearch || catalogFilter !== 'all') && !layerComponents.length) return null;
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
            }) : components?.length ? <EmptyState title="没有匹配的组件" description="请调整搜索词或筛选条件。" /> : <EmptyState title="暂无组件" description="组件 Owner 可以创建第一个组件。" />}
          </aside>

          {selected ? <section className="detail-stack">
            {!selectedInCatalog ? <div className="catalog-selection-notice"><span>当前详情不在目录筛选结果中：{selected.name}</span><button type="button" className="icon-text" onClick={() => { setCatalogSearch(''); setCatalogFilter('all'); }}>清除筛选</button></div> : null}
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

            <StatusExplanationPanel item={releaseWorkItem} />

            {mine && editableDraft ? <DraftReadiness
              release={editableDraft}
              runs={runs ?? []}
              onContract={() => { selectContractRelease(editableDraft.id); setEditingContract(true); }}
              onLifecycle={() => setEditRelease(editableDraft)}
              onImage={() => setImageRelease(editableDraft)}
              onArtifact={() => setArtifactRelease(editableDraft)}
              onValidate={() => { selectContractRelease(editableDraft.id); setTestRelease(editableDraft); }}
              onPublish={() => void previewPublish(editableDraft)}
            /> : null}

            <article className="panel">
              <header className="panel__header"><div><span className="panel__icon"><Rocket size={18} /></span><div><h2>发布历史</h2><p>点击版本可切换下方依赖和参数合同；已发布版本不可修改</p></div></div></header>
              {releases.length ? <div className="release-table">
                <div className="release-table__head"><span>版本</span><span>适配环境</span><span>验证</span><span>依赖 / 动作</span><span>发布时间</span><span /></div>
                {releases.map((release) => {
                  const active = contractRelease?.id === release.id;
                  const lifecycle = lifecycleSummary(release.actions);
                  const evidence = releaseEvidence(release, runs ?? [], 'install');
                  const publishReady = releaseReadyForPublish(release, runs ?? []);
                  return <div key={release.id} className={`release-row${active ? ' release-row--active' : ''}`} onClick={() => selectContractRelease(release.id)}>
                    <button type="button" className="release-row__version" aria-pressed={active} aria-label={`查看 ${release.version} 的依赖和参数合同`} onClick={() => selectContractRelease(release.id)}>
                      <span className="release-state-line"><strong>{release.version}</strong><StatusPill status={release.state} />{release.candidate && <StatusPill status="candidate">候选集</StatusPill>}{release.breaking && <span className="breaking-badge">不兼容变更</span>}</span>
                      <small>{release.releaseNotes ?? '未填写发布说明'}</small>
                    </button>
                    <EnvironmentConstraints constraints={release.environmentConstraints} />
                    <div className="release-verification"><StatusPill status={release.verified ? 'passed' : 'unverified'} /><EvidenceLink run={evidence} stale={!releaseIsVerified(release)} /></div>
                    <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖</span><span>{mappingCount(release)} 个参数映射</span><span>{publicCount(release)} 个公开参数</span><span className={`lifecycle-completeness${lifecycle.completed === lifecycle.total ? ' lifecycle-completeness--complete' : ''}`}>动作 {lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</span></div>
                    <div>{formatTime(release.releasedAt ?? release.createdAt)}</div>
                    <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                      {canTest && <button className="icon-text" onClick={() => { selectContractRelease(release.id); setTestRelease(release); }}><Beaker size={15} /> 环境验证</button>}
                      <button className="icon-text" onClick={() => { selectContractRelease(release.id); setInspectRelease(release); }}>查看详情</button>
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => { selectContractRelease(release.id); setEditingContract(true); }}><PencilLine size={15} /> 配置合同</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setEditRelease(release)}><FileCode2 size={15} /> Playbook</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setImageRelease(release)}><Container size={15} /> 构建镜像</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setArtifactRelease(release)}><Archive size={15} /> 组件介质</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" disabled={busy || (!release.candidate && !publishReady)} onClick={() => void toggleCandidate(release)}>{release.candidate ? '撤回候选' : '加入候选集'}</button>}
                      {mine && release.state === 'draft' && <button className="icon-text icon-text--primary" disabled={!publishReady} title={publishReady ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={() => void previewPublish(release)}><Rocket size={15} /> 发布</button>}
                      {mine && (release.state === 'draft' || release.state === 'released') && <button className="icon-text icon-text--danger" onClick={() => void previewDeprecate(release)}>{release.state === 'draft' ? '废弃草稿' : '废弃'}</button>}
                    </div>
                  </div>;
                })}
              </div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。" />}
            </article>

            <ComponentMappingOverview releases={releases} components={components ?? []} />
            {!contractReleaseId && !selectedReleaseId && selected.latestRelease && contractRelease && selected.latestRelease.id !== contractRelease.id ? <p className="mapping-empty contract-hint">当前展示 {contractRelease.version}，因为它有参数映射。最新版本 {selected.latestRelease.version} 没有映射。</p> : null}
            {showContractEditor && contractRelease ? (
              <ReleaseContractEditor key={contractRelease.id} release={contractRelease} components={components ?? []} focusSection={contractFocus} onCancel={() => { setEditingContract(false); setContractFocus(undefined); }} onSaved={() => { setEditingContract(false); setContractFocus(undefined); signalRefresh('components'); }} />
            ) : (
              <div className="two-column">
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{contractRelease ? `${contractRelease.version} 锁定的上游，以及引用了哪个公开参数` : '锁定上游版本，并标明引用了哪个公开参数'}</p></div></div>
                    {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑直接依赖" onClick={() => startEditingContract('dependencies')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <DependencyContractList dependencies={contractRelease?.dependencies ?? []} components={components ?? []} />
                </article>
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>{contractRelease ? `${contractRelease.version} 的公开参数可被下游引用；内部参数只给本组件使用` : '公开参数可被下游引用；内部参数只给本组件使用'}</p></div></div>
                    {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑参数合同" onClick={() => startEditingContract('parameters')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <ParameterContractList release={contractRelease} components={components ?? []} />
                </article>
              </div>
            )}
          </section> : <section className="panel"><EmptyState title="请选择组件" /></section>}
        </div>
        </>
      )}

      {createOpen && <CreateComponentModal onClose={() => setCreateOpen(false)} onDone={(component) => { setCreateOpen(false); setSearchParams({ selected: component.id }); setContractDraftIntent(undefined); setVersionBase(component); signalRefresh('components'); }} />}
      {componentImportOpen && <ComponentTemplateImportModal onClose={() => setComponentImportOpen(false)} onDone={() => { setComponentImportOpen(false); signalRefresh('components'); }} />}
      {editComponent && <EditComponentModal component={editComponent} onClose={() => setEditComponent(undefined)} onDone={() => { setEditComponent(undefined); signalRefresh('components'); }} />}
      {versionBase && <NewVersionModal component={versionBase} baseRelease={contractRelease ?? versionBase.latestRelease} contractIntent={contractDraftIntent} onClose={() => { setVersionBase(undefined); setContractDraftIntent(undefined); setContractFocus(undefined); }} onDone={(release) => {
        const intent = contractDraftIntent;
        setVersionBase(undefined);
        setContractDraftIntent(undefined);
        signalRefresh('components');
        if (!release) return;
        setContractReleaseId(release.id);
        setSearchParams({ selected: release.componentId, release: release.id });
        if (intent) {
          setPendingContractRelease(release);
          setContractFocus(intent === 'all' ? undefined : intent);
          setEditingContract(true);
        } else {
          setEditRelease(release);
        }
      }} />}
      {inspectRelease && <InspectReleaseModal release={inspectRelease} components={components ?? []} onClose={() => setInspectRelease(undefined)} onEdit={mine && inspectRelease.state === 'draft' ? () => { setInspectRelease(undefined); setContractReleaseId(inspectRelease.id); setEditingContract(true); } : undefined} />}
      {visibleEditRelease && <EditReleaseModal key={visibleEditRelease.id} release={visibleEditRelease} releases={releases} onClose={() => setEditRelease(undefined)} onDone={() => { setEditRelease(undefined); signalRefresh('components'); }} />}
      {testRelease && <TestReleaseModal release={testRelease} onClose={() => setTestRelease(undefined)} onDone={() => { setTestRelease(undefined); signalRefresh(['components', 'runs']); }} />}
      {imageRelease && <ImageBuildModal release={imageRelease} onClose={() => setImageRelease(undefined)} />}
      {artifactRelease && <ArtifactModal release={artifactRelease} onClose={() => setArtifactRelease(undefined)} />}
      {publishRelease && <Modal title={`发布 ${publishRelease.version}`} description="发布后版本不可修改；影响通知将发送给下游组件和场景 Owner。" onClose={() => setPublishRelease(undefined)}>
        <div className="modal-body">
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关场景 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <StatusExplanationPanel explanation={operationExplanation} title="发布操作被阻断" />
        <footer className="modal-actions"><button className="button button--quiet" onClick={() => { setPublishRelease(undefined); setOperationExplanation(undefined); }}>取消</button><button disabled={busy || !impact} className="button button--primary" onClick={() => void confirmPublish()}>{busy ? '发布中…' : '确认发布并通知'}</button></footer>
      </Modal>}
      {deprecateRelease && <Modal title={`${deprecateRelease.state === 'draft' ? '废弃草稿' : '废弃'} ${deprecateRelease.version}`} description={deprecateRelease.state === 'draft' ? '草稿将退出候选共享并保留在发布历史中；Playbook、介质保留，已被场景 Run 锁定的草稿不能废弃。' : '没有场景 Run 锁定时，该版本可停止作为推荐版本。'} onClose={() => { setDeprecateRelease(undefined); setOperationExplanation(undefined); }}>
        <div className="modal-body">
          <div className="warning-callout warning-callout--danger"><AlertTriangle size={19} /><div><strong>{deprecationBlocked ? '该组件版本不能废弃' : '这是影响下游选择的状态变更'}</strong><p>{deprecationBlocked ? `该版本已经被 ${deprecationScenarioRunCount} 个场景 Run 锁定；请保留该版本以维持运行记录与交付依据。` : '请先确认受影响组件和场景。仅被场景引用但从未运行的版本仍可废弃。'}</p></div></div>
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关场景 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <StatusExplanationPanel explanation={operationExplanation} title="废弃操作被阻断" />
        <footer className="modal-actions"><button className="button button--quiet" onClick={() => { setDeprecateRelease(undefined); setOperationExplanation(undefined); }}>取消</button><button disabled={busy || !impact || deprecationBlocked} className="button button--danger" onClick={() => void confirmDeprecate()}>{busy ? '废弃中…' : deprecateRelease.state === 'draft' ? '确认废弃草稿' : '确认废弃版本'}</button></footer>
      </Modal>}
    </div>
  );
}

const ACTIVE_IMAGE_BUILD_STATUSES = new Set(['queued', 'running']);

function ImageBuildModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user, notify } = useApp();
  const { data: environments, loading: environmentsLoading } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [tag, setTag] = useState(release.version.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'latest');
  const [environmentId, setEnvironmentId] = useState('');
  const [dockerfile, setDockerfile] = useState<File>();
  const [builds, setBuilds] = useState<ComponentImageBuild[]>([]);
  const [selectedBuild, setSelectedBuild] = useState<ComponentImageBuild>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!environmentId && environments?.length) {
      setEnvironmentId(environments.find((item) => item.currentRevision?.variables.IMAGE_REGISTRY)?.id ?? environments[0].id);
    }
  }, [environmentId, environments]);

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
    if (!environmentId) {
      notify('error', '请选择目标环境');
      return;
    }
    if (!dockerfile) {
      notify('error', '请选择 Dockerfile');
      return;
    }
    setBusy(true);
    try {
      const build = await api.startImageBuild(release.id, environmentId, dockerfile, tag);
      setBuilds((current) => [build, ...current]);
      setSelectedBuild(build);
      notify('success', '镜像构建已提交', build.imageRef);
    } catch (reason) {
      notify('error', '提交镜像构建失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const registry = selectedEnvironment?.currentRevision?.variables.IMAGE_REGISTRY;
  const logs = selectedBuild?.logs?.map((item) => `[${item.stream}] ${item.message}`).join('\n') ?? '';
  return <Modal title={`构建并发布镜像 · ${release.version}`} description="选择目标环境并上传单个 Dockerfile；平台会锁定环境 Revision 后构建并推送。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      <form onSubmit={(event) => void submit(event)}>
        <div className="warning-callout"><AlertTriangle size={19} /><div><strong>Dockerfile 会在平台构建机上执行</strong><p>构建上下文仅包含该 Dockerfile，不接受本地目录或主机路径；请只上传可信内容。</p></div></div>
        <div className="form-grid image-build-form">
          <label className="span-2"><span>目标环境</span><select aria-label="目标环境" required value={environmentId} disabled={environmentsLoading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{registry ? `IMAGE_REGISTRY=${registry}` : selectedEnvironment ? '该环境尚未配置 IMAGE_REGISTRY，请联系 Environment Owner。' : '正在读取可用环境…'}</small></label>
          <label className="span-2"><span>Dockerfile</span><input aria-label="Dockerfile" type="file" required onChange={(event) => setDockerfile(event.target.files?.[0])} /><small>UTF-8，最大 1 MiB，必须包含 FROM 指令</small></label>
          <label className="span-2"><span>镜像标签</span><input value={tag} required pattern="[a-z0-9][a-z0-9._-]{0,127}" onChange={(event) => setTag(event.target.value.toLowerCase())} /><small>镜像路径固定为 IMAGE_REGISTRY / components / 组件 slug : 标签</small></label>
        </div>
        <button disabled={busy || !dockerfile || !environmentId || !registry} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '提交中…' : '上传并构建'}</button>
      </form>
      <section className="image-build-results">
        <div className="image-build-history"><strong>构建记录</strong>{loading ? <span>读取中…</span> : builds.length ? builds.map((build) => <button key={build.id} type="button" className={selectedBuild?.id === build.id ? 'active' : ''} onClick={() => setSelectedBuild(build)}><span>{build.imageTag}</span><StatusPill status={build.status} /></button>) : <span>暂无构建记录</span>}</div>
        {selectedBuild ? <div className="image-build-detail">
          <div className="image-build-ref"><span>目标镜像</span><code>{selectedBuild.imageRef}</code>{selectedBuild.environmentId ? <><span>环境快照</span><code>{selectedBuild.environmentId} · {selectedBuild.environmentRevisionId ?? '历史记录未保存 Revision'}</code></> : null}{selectedBuild.imageDigest ? <code>{selectedBuild.imageDigest}</code> : null}{selectedBuild.error ? <p>{selectedBuild.error}</p> : null}</div>
          <pre aria-label="镜像构建日志">{logs || (ACTIVE_IMAGE_BUILD_STATUSES.has(selectedBuild.status) ? '等待构建日志…' : '无日志')}</pre>
        </div> : <EmptyState title="尚未选择构建" description="上传 Dockerfile 后可在这里查看实时日志与镜像摘要。" />}
      </section>
    </div>
    <footer className="modal-actions"><button className="button button--quiet" type="button" onClick={onClose}>关闭</button></footer>
  </Modal>;
}

function ArtifactModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user, notify, signalRefresh } = useApp();
  const { data: environments, loading } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [mode, setMode] = useState<'upload' | 'register'>('upload');
  const [environmentId, setEnvironmentId] = useState('');
  const [alias, setAlias] = useState('media');
  const [sha256, setSha256] = useState('');
  const [relativePath, setRelativePath] = useState('');
  const [file, setFile] = useState<File>();
  const [checksumFile, setChecksumFile] = useState<File>();
  const [artifacts, setArtifacts] = useState<ComponentArtifact[]>(release.artifacts ?? []);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!environmentId && environments?.length) {
      setEnvironmentId(environments.find((item) => item.currentRevision?.variables.FILE_STATION)?.id ?? environments[0].id);
    }
  }, [environmentId, environments]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    try {
      let saved: ComponentArtifact;
      if (mode === 'upload') {
        if (!file) throw new Error('请选择介质文件。');
        saved = await api.uploadArtifact(release.id, { environmentId, alias, sha256: sha256.trim() || undefined, artifact: file, checksumFile });
      } else {
        saved = await api.registerArtifact(release.id, { environmentId, alias, relativePath, sha256 });
      }
      setArtifacts((items) => [saved, ...items.filter((item) => item.alias !== saved.alias)].sort((a, b) => a.alias.localeCompare(b.alias)));
      notify('success', '组件介质已保存', `${saved.alias} · sha256:${saved.sha256}`);
      signalRefresh('components');
    } catch (reason) {
      notify('error', '保存组件介质失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function loadChecksum(next?: File) {
    setChecksumFile(next);
    if (!next) return;
    const value = (await next.text()).trim().split(/\s+/)[0] ?? '';
    setSha256(value);
  }

  async function detach(item: ComponentArtifact) {
    setBusy(true);
    try {
      await api.deleteArtifact(release.id, item.alias);
      setArtifacts((items) => items.filter((artifact) => artifact.alias !== item.alias));
      notify('success', '介质引用已移除', 'file-station 上的物理文件未删除。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '移除介质引用失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const station = selectedEnvironment?.currentRevision?.variables.FILE_STATION;
  return <Modal title={`组件介质 · ${release.version}`} description="介质固定保存到 components/组件 slug/版本/文件名；发布后不可修改。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      <form onSubmit={(event) => void submit(event)}>
        <div className="tabs" role="tablist"><button type="button" className={mode === 'upload' ? 'active' : ''} onClick={() => setMode('upload')}>上传文件</button><button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>登记已有路径</button></div>
        <div className="form-grid image-build-form">
          <label className="span-2"><span>介质所在环境</span><select required value={environmentId} disabled={loading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{station ? `FILE_STATION=${station}` : selectedEnvironment ? '该环境尚未配置 FILE_STATION，请联系 Environment Owner。' : '正在读取可用环境…'}</small></label>
          <label><span>介质别名</span><input required pattern="[a-z][a-z0-9_]*" value={alias} onChange={(event) => setAlias(event.target.value.toLowerCase())} /><small>运行时注入 alias_path、alias_url、alias_sha256</small></label>
          {mode === 'register' ? <label><span>file-station 相对路径</span><input required value={relativePath} placeholder="imports/example.tar.gz" onChange={(event) => setRelativePath(event.target.value)} /></label> : <label><span>介质文件</span><input type="file" required onChange={(event) => setFile(event.target.files?.[0])} /></label>}
          <label><span>SHA-256</span><input required={!checksumFile} value={sha256} pattern="[A-Fa-f0-9]{64}" placeholder="64 位十六进制" onChange={(event) => setSha256(event.target.value)} /></label>
          <label><span>SHA-256 文件</span><input type="file" accept=".sha256,text/plain" onChange={(event) => void loadChecksum(event.target.files?.[0])} /><small>可上传常见的 “hash 文件名” 格式</small></label>
        </div>
        <button disabled={busy || !environmentId || !station || (mode === 'upload' && !file)} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '保存中…' : mode === 'upload' ? '上传并校验' : '登记并校验'}</button>
      </form>
      <section className="image-build-results">
        <div className="image-build-history"><strong>当前介质</strong>{artifacts.length ? artifacts.map((item) => <div key={item.alias}><span>{item.alias}</span><button type="button" className="icon-button icon-button--danger" disabled={busy} aria-label={`移除介质 ${item.alias}`} onClick={() => void detach(item)}><Trash2 size={14} /></button></div>) : <span>尚未录入介质</span>}</div>
        <div className="image-build-detail"><div className="image-build-ref"><span>规则</span><code>FILE_STATION/components/&lt;slug&gt;/&lt;version&gt;/&lt;filename&gt;</code><p>实际环境的 FILE_STATION 不同时，Run 会先进入环境管理员审批；批准后平台平移并校验 SHA-256，再执行 Playbook。目标已存在同一指纹时复用。</p>{artifacts.map((item) => <div key={item.id}><span>{item.alias}</span><code>http://{item.fileStation}/{item.relativePath}</code><code>sha256:{item.sha256} · {item.sizeBytes} bytes</code></div>)}</div></div>
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

function CredentialNameEditor({ values, onChange }: { values: string[]; onChange: (values: string[]) => void }) {
  const [input, setInput] = useState('');
  function add() {
    const additions = splitCSV(input);
    if (!additions.length) return;
    onChange([...new Set([...values, ...additions])]);
    setInput('');
  }
  return <div className="span-2 credential-ref-editor">
    <div className="credential-ref-editor__label"><span>所需 CredentialRef</span>{values.length ? <button type="button" onClick={() => onChange([])}>清空全部</button> : null}</div>
    <div className="credential-ref-tags" aria-label="所需 CredentialRef 列表">
      {values.map((name) => <span key={name}>{name}<button type="button" aria-label={`删除 CredentialRef ${name}`} onClick={() => onChange(values.filter((item) => item !== name))}>×</button></span>)}
      {!values.length ? <small>未声明 CredentialRef</small> : null}
    </div>
    <div className="inline-field"><input aria-label="添加 CredentialRef" value={input} onChange={(event) => setInput(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ',') { event.preventDefault(); add(); } }} placeholder="输入名称后按回车" /><button type="button" className="button button--quiet" disabled={!input.trim()} onClick={add}>添加</button></div>
  </div>;
}

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
    const next: ActionDefinition = { name: 'install', type: 'install', playbook: '', timeoutSeconds: 1800, riskLevel: 'low' };
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
          updateAction({ type, name: !action.name || action.name === previousType ? type : action.name, idempotent: type === 'install' ? action.idempotent : false });
          if (filename === `${previousType}.yml` || filename === `${previousType}.yaml`) setFilename(`${type}.yml`);
        }}>{ACTION_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.value} · {option.label}</option>)}</select></label>
        <label><span>目标主机组</span><input value={action.hostGroup ?? ''} onChange={(event) => updateAction({ hostGroup: event.target.value })} placeholder="例如 k8snode" /></label>
        <label><span>超时（秒）</span><input type="number" min={1} value={action.timeoutSeconds ?? 1800} onChange={(event) => updateAction({ timeoutSeconds: Number(event.target.value) })} /></label>
        <label><span>风险级别</span><select value={action.riskLevel ?? 'low'} onChange={(event) => { const riskLevel = event.target.value as NonNullable<ActionDefinition['riskLevel']>; updateAction({ riskLevel, destructive: riskLevel === 'destructive' }); }}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <label className="span-2"><span>Playbook 路径</span><div className="inline-field"><input value={action.playbook} onChange={(event) => updateAction({ playbook: event.target.value })} placeholder="可填写服务器已有的相对路径，或在下方上传" /><button type="button" className="button button--quiet" disabled={!action.playbook || loading} onClick={() => void loadPlaybook()}>{loading ? '读取中…' : '载入编辑器'}</button></div></label>
        <label><span>Tags（逗号分隔）</span><input value={(action.tags ?? []).join(', ')} onChange={(event) => updateAction({ tags: splitCSV(event.target.value) })} /></label>
        <label><span>Limit 匹配</span><input value={action.limit ?? ''} onChange={(event) => updateAction({ limit: event.target.value })} placeholder="可选，例如 workers" /></label>
        <label><span>可用参数（逗号分隔）</span><input value={(action.allowedParameters ?? []).join(', ')} onChange={(event) => updateAction({ allowedParameters: splitCSV(event.target.value) })} /></label>
        <CredentialNameEditor values={action.requiredCredentials ?? []} onChange={(requiredCredentials) => updateAction({ requiredCredentials })} />
        {action.type === 'install' ? <label className="checkbox-field span-2 action-capability-field"><input type="checkbox" checked={action.idempotent ?? false} onChange={(event) => updateAction({ idempotent: event.target.checked })} /><span><strong>幂等安装，同时作为升级作业</strong><small>场景选择 upgrade 时复用这个 Playbook，无需再录入一份升级动作。</small></span></label> : null}
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

function NewVersionModal({ component, baseRelease, contractIntent, onClose, onDone }: { component: Component; baseRelease?: ComponentRelease; contractIntent?: ContractEditIntent; onClose: () => void; onDone: (release?: ComponentRelease) => void }) {
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
        ? await (async () => {
          const plan = await api.previewReleaseClone(source.id, input);
          if (!window.confirm(`复制预览\n${plan.sourceVersion} → ${plan.targetVersion}\n${plan.actions.length} 个 Action · ${plan.playbooks.length} 个 Playbook · ${plan.artifactCount} 个制品引用\n\n确认创建新 Draft？`)) return undefined;
          return api.cloneRelease(source.id, { ...input, expectedPlanDigest: plan.planDigest });
        })()
        : await api.createRelease(component.id, { ...input, type: String(form.get('releaseType')) as ComponentRelease['type'], state: 'draft' });
      if (!release) return;
      notify('success', 'Draft 已创建', '接下来为每个参数选择内部或公开，并映射上游公开参数。');
      onDone(release);
    } catch (reason) {
      notify('error', '创建版本失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }
  const contractLabel = contractIntent === 'dependencies' ? '直接依赖' : contractIntent === 'parameters' ? '参数合同' : '依赖和参数';
  const title = contractIntent ? `创建 Draft 编辑${contractLabel}` : `更新 ${component.name}`;
  const description = contractIntent && source
    ? `${source.version} 已发布且不可直接修改。请先克隆为新 Draft，创建后将自动进入${contractLabel}编辑。`
    : source ? `从 ${source.version} 克隆为新 Draft，可继续设置参数可见性和上游映射。` : '创建组件的首个 Draft Release。';
  return <Modal size="wide" title={title} description={description} onClose={onClose}>
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
  const { notify } = useApp();
  const { data: environments } = useApiData((signal) => api.environments(signal), [], 'environments');
  const { data: components } = useApiData((signal) => api.components(signal), [], 'components');
  const installAction = (['upgrade', 'install', 'configure', 'preflight', 'inspect'] as const)
    .map((type) => release.actions?.find((action) => action.type === type))
    .find((action) => action !== undefined);
  const rollbackAction = release.actions?.find((action) => action.type === 'rollback');
  const [mode, setMode] = useState<'install_verify' | 'rollback'>(() => installAction ? 'install_verify' : 'rollback');
  const selectedAction = mode === 'rollback' ? rollbackAction : installAction;
  const [environmentId, setEnvironmentId] = useState('');
  const [rollbackVerificationKind, setRollbackVerificationKind] = useState<'target_release' | 'rollback_only'>(() => rollbackAction?.toReleaseId ? 'target_release' : 'rollback_only');
  const [verifyReleaseId, setVerifyReleaseId] = useState(rollbackAction?.toReleaseId ?? '');
  const [busy, setBusy] = useState<'preview' | 'submit'>();
  const [plan, setPlan] = useState<ComponentTestPlan>();
  const [planError, setPlanError] = useState<string>();
  const [planExplanation, setPlanExplanation] = useState<WorkExplanation>();
  const [runInputValues, setRunInputValues] = useState<Record<string, string>>({});
  const component = components?.find((item) => item.id === release.componentId);
  const retainedVerifyReleases = (component?.releases ?? []).filter((item) => item.id !== release.id && (item.state === 'released' || item.state === 'deprecated') && item.actions?.some((action) => action.type === 'verify'));
  const verifyRelease = retainedVerifyReleases.find((item) => item.id === verifyReleaseId);
  const mapped = [...new Set([...mappedParameterNames(release), ...(verifyRelease ? mappedParameterNames(verifyRelease) : [])])];
  const [fixtureValues, setFixtureValues] = useState<Record<string, string>>({});
  useEffect(() => {
    const defaults = { ...defaultFixtureValues(release, components), ...(verifyRelease ? defaultFixtureValues(verifyRelease, components) : {}) };
    setFixtureValues((current) => ({ ...defaults, ...current }));
    setPlan(undefined);
  }, [components, release, verifyRelease]);
  const declaredRunInputs = uniqueRunInputs(selectedAction?.allowedParameters ?? []);

  function presetValues() {
    const dependencyFixtures = Object.fromEntries(mapped.map((name) => {
      const raw = fixtureValues[name] ?? '';
      try { return [name, JSON.parse(raw)]; } catch { return [name, raw]; }
    }));
    return { runInput: parseRunInput(declaredRunInputs, runInputValues), dependencyFixtures };
  }

  function applyPreset(values: { runInput?: Record<string, unknown>; dependencyFixtures?: Record<string, unknown> }) {
    const display = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value);
    setRunInputValues(Object.fromEntries(Object.entries(values.runInput ?? {}).map(([name, value]) => [name, display(value)])));
    setFixtureValues((current) => ({ ...current, ...Object.fromEntries(Object.entries(values.dependencyFixtures ?? {}).map(([name, value]) => [name, display(value)])) }));
    invalidatePlan();
  }

  function invalidatePlan() {
    setPlan(undefined);
    setPlanError(undefined);
    setPlanExplanation(undefined);
  }

  function requestInput(expectedPlanDigest?: string): ComponentTestRequest {
    const fixtures = Object.fromEntries(mapped.map((name) => {
      const raw = fixtureValues[name] ?? '';
      try { return [name, JSON.parse(raw)]; } catch { return [name, raw]; }
    }));
    return {
      environmentId,
      mode,
      rollbackVerification: mode === 'rollback'
        ? rollbackVerificationKind === 'rollback_only'
          ? { kind: 'rollback_only' }
          : { kind: 'target_release', releaseId: verifyReleaseId }
        : undefined,
      runInput: parseRunInput(declaredRunInputs, runInputValues),
      dependencyFixtures: fixtures,
      expectedPlanDigest,
    };
  }

  async function preview() {
    if (!environmentId || !selectedAction || (mode === 'rollback' && rollbackVerificationKind === 'target_release' && !verifyReleaseId)) return;
    setBusy('preview');
    setPlan(undefined);
    setPlanError(undefined);
    setPlanExplanation(undefined);
    try {
      setPlan(await api.previewReleaseTest(release.id, requestInput()));
    } catch (reason) {
      setPlanError(displayError(reason));
      setPlanExplanation(actionableExplanation(reason));
    } finally {
      setBusy(undefined);
    }
  }

  async function run() {
    if (!environmentId || !selectedAction || !plan) return;
    setBusy('submit');
    try {
      await api.testRelease(release.id, requestInput(plan.planDigest));
      const label = mode === 'rollback' ? '回退验证' : '安装验证';
      notify('success', `${label}已提交`, mode === 'rollback' ? '回退动作会继续遵循破坏性操作审批流程。' : mapped.length ? 'Fixture 只证明组件能消费参数，不能替代场景完整验证。' : '可以在运行中心查看 Ansible 日志。');
      onDone();
    } catch (reason) {
      setPlan(undefined);
      setPlanError(displayError(reason));
      setPlanExplanation(actionableExplanation(reason));
    } finally {
      setBusy(undefined);
    }
  }

  const releaseLabel = (id?: string) => {
    if (!id) return '未声明（独立回滚）';
    if (id === release.id) return `${release.version} · Draft`;
    const target = component?.releases?.find((item) => item.id === id);
    return target ? `${target.version} · ${target.state}` : id;
  };

  const releaseStateLabel = release.state === 'draft' ? 'Draft' : release.state === 'released' ? '已发布版本' : '已废弃版本';
  return <Modal title={`环境验证 ${release.version}`} description={`对${releaseStateLabel}执行生命周期作业；提交后会创建 Run，并可能修改目标环境。`} onClose={onClose}>
    <div className="modal-body">
      <div className={`validation-risk-summary${mode === 'rollback' ? ' validation-risk-summary--danger' : ''}`}><AlertTriangle size={19} /><div><strong>{mode === 'rollback' ? '回退验证将修改环境状态' : '安装验证会在目标环境执行主动作'}</strong><p>第一步只预览锁定计划，不创建 Run；点击最终确认后才会提交执行。</p></div></div>
      <label><span>验证模式</span><select aria-label="验证模式" value={mode} onChange={(event) => { setMode(event.target.value as 'install_verify' | 'rollback'); setRunInputValues({}); invalidatePlan(); }}><option value="install_verify" disabled={!installAction}>安装验证（主动作 + verify）</option><option value="rollback" disabled={!rollbackAction}>回退验证（rollback + 可选目标版本 verify）</option></select></label>
      {mode === 'rollback' ? <>
        <div className="warning-callout"><AlertTriangle size={19} /><div><strong>回退合同不可变</strong><p>Draft 回退始终按已保存的来源/目标合同执行；下方目标只决定追加哪个 Release 的 verify，不会覆盖回退合同。</p></div></div>
        <div className="rollback-contract"><strong>Draft 回退合同</strong><span>来源：{releaseLabel(rollbackAction?.fromReleaseId)}</span><span>目标：{releaseLabel(rollbackAction?.toReleaseId)}</span></div>
        <label><span>回退验证策略</span><select aria-label="回退验证策略" value={rollbackVerificationKind} onChange={(event) => { setRollbackVerificationKind(event.target.value as 'target_release' | 'rollback_only'); invalidatePlan(); }}><option value="target_release">Draft 回退 + 所选 Release verify</option><option value="rollback_only">仅执行 Draft 回退</option></select></label>
        {rollbackVerificationKind === 'target_release' ? <label><span>verify 目标 Release</span><select aria-label="verify 目标 Release" value={verifyReleaseId} onChange={(event) => { setVerifyReleaseId(event.target.value); invalidatePlan(); }}><option value="">请选择包含 verify 的保留版本</option>{retainedVerifyReleases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.state}{item.id === rollbackAction?.toReleaseId ? ' · 合同目标' : ''}</option>)}</select></label> : null}
      </> : null}
      <label><span>目标环境</span><select aria-label="目标环境" value={environmentId} onChange={(event) => { setEnvironmentId(event.target.value); invalidatePlan(); }}><option value="">请选择环境</option>{environments?.map((env: Environment) => <option key={env.id} value={env.id}>{env.name} · {env.status ?? 'ready'}</option>)}</select></label>
      <RunInputPresetPicker key={`${release.id}:${mode}`} resourceType="component_release" resourceId={release.id} context={mode === 'rollback' ? 'component_rollback' : 'component_install_verify'} values={presetValues()} onApply={applyPreset} />
      {mapped.length ? <div className="fixture-fields">{mapped.map((name) => <label key={name}><span>依赖 Fixture · {name}</span><input value={fixtureValues[name] ?? ''} onChange={(event) => { setFixtureValues((current) => ({ ...current, [name]: event.target.value })); invalidatePlan(); }} /></label>)}</div> : null}
      <RunInputFields names={declaredRunInputs} values={runInputValues} onChange={(name, value) => { setRunInputValues((current) => ({ ...current, [name]: value })); invalidatePlan(); }} />
      {planError ? <div className="form-validation" role="alert">{planError}</div> : null}
      <StatusExplanationPanel explanation={planExplanation} title="验证操作被阻断" />
      {plan ? <section className="test-plan-preview" aria-label="完整执行计划"><header><div><strong>完整执行计划</strong><small>Environment Revision {plan.environmentRevisionId}</small></div>{plan.requiresApproval ? <StatusPill status="awaiting_approval">需环境 Owner 审批</StatusPill> : <StatusPill status="ready">可直接排队</StatusPill>}</header><div>{plan.steps.map((step) => <article key={`${step.order}-${step.releaseId}-${step.action}`}><span>{step.order}</span><div><strong>{step.componentName} · {step.action}</strong><p>所属版本 {step.releaseVersion} · {step.releaseId}</p><small>Playbook {step.playbook}{step.limit ? ` · 目标 ${step.limit}` : ''}</small>{step.action === 'rollback' ? <small>不可变合同：{step.fromReleaseVersion || step.fromReleaseId || '独立'} → {step.toReleaseVersion || step.toReleaseId || '独立'}</small> : null}{step.backupRef ? <small>备份基线：Run {step.backupInstallRunId} · {step.backupRef}</small> : null}</div></article>)}</div></section> : null}
    </div>
    <footer className="modal-actions"><button type="button" className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>取消</button><button type="button" className="button button--quiet" disabled={!environmentId || !selectedAction || Boolean(busy) || (mode === 'rollback' && rollbackVerificationKind === 'target_release' && !verifyReleaseId)} onClick={() => void preview()}><Beaker size={16} /> {busy === 'preview' ? '规划中…' : plan ? '刷新执行计划' : '预览执行计划'}</button><button type="button" className={mode === 'rollback' ? 'button button--danger-soft' : 'button button--primary'} disabled={!plan || Boolean(busy)} onClick={() => void run()}>{busy === 'submit' ? '提交中…' : mode === 'rollback' ? '确认提交回退验证' : '确认提交安装验证'}</button></footer>
  </Modal>;
}

function EditReleaseModal({ release, releases, onClose, onDone }: { release: ComponentRelease; releases: ComponentRelease[]; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const { data: components } = useApiData((signal) => api.components(signal), [], 'components');
  const [busy, setBusy] = useState(false);
  const [constraints, setConstraints] = useState(() => parseConstraintSelection(release.environmentConstraints));
  const [parameters, setParameters] = useState<ParameterDefinition[]>(release.parameters ?? []);
  const [dependencies, setDependencies] = useState<ComponentDependency[]>(release.dependencies ?? []);
  const [actions, setActions] = useState<ActionDefinition[]>(release.actions ?? []);
  const [savedActions, setSavedActions] = useState(() => JSON.stringify(release.actions ?? []));
  const [playbookDirty, setPlaybookDirty] = useState(false);
  const actionsDirty = JSON.stringify(actions) !== savedActions;
  const contractErrors = parameterContractErrors(parameters, dependencies, components?.flatMap((item) => item.releases ?? []) ?? []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (playbookDirty) { notify('error', '请先保存 Playbook', '在线编辑器中仍有未保存内容。'); return; }
    setBusy(true); const form = new FormData(event.currentTarget);
    if (contractErrors.length) { notify('error', '参数合同无效', contractErrors.join('；')); setBusy(false); return; }
    try {
      const submittedActions = actions.map((item) => ({ ...item, requiredCredentials: item.requiredCredentials ?? [] }));
      const clearedCredentials = actions.reduce((count, action, index) => {
        const before = release.actions?.[index]?.requiredCredentials ?? [];
        const after = new Set(action.requiredCredentials ?? []);
        return count + before.filter((name) => !after.has(name)).length;
      }, 0);
      const saved = await api.updateRelease(release.id, {
        ...release,
        version: String(form.get('version')),
        type: String(form.get('releaseType')) as ComponentRelease['type'],
        releaseNotes: String(form.get('notes')),
        breaking: form.get('breaking') === 'on',
        environmentConstraints: serializeConstraintSelection(constraints),
        parameters,
        dependencies,
        actions: submittedActions,
      });
      setActions(saved.actions ?? []);
      setSavedActions(JSON.stringify(saved.actions ?? []));
      notify('success', 'Draft 配置已保存', clearedCredentials ? `已清除 ${clearedCredentials} 个 CredentialRef；其余 Draft 配置也已更新。` : '参数合同、依赖映射和 Ansible 动作已更新。');
      onDone();
    } catch (reason) { notify('error', '保存 Draft 失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  const close = () => { if (!busy) onClose(); };
  return <Modal size="wide" title={`配置 Draft ${release.version}`} description="维护版本信息、参数合同、依赖映射，以及可上传和在线编辑的 Playbook。" onClose={close}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>版本</span><input name="version" defaultValue={release.version} required /></label><label><span>Release 类型</span><select name="releaseType" defaultValue={release.type ?? 'atomic'}><option value="atomic">atomic · 原子组件</option><option value="bundle">bundle · 组合组件</option></select></label><label className="checkbox-field"><input type="checkbox" name="breaking" defaultChecked={release.breaking} /><span>包含不兼容变更</span></label><label className="span-2"><span>发布说明</span><textarea name="notes" defaultValue={release.releaseNotes} rows={3} required /></label><div className="span-2"><EnvironmentConstraintEditor value={constraints} onChange={setConstraints} /></div><div className="span-2"><PlaybookActionEditor releaseId={release.id} releases={releases} actions={actions} onChange={setActions} onDirtyChange={setPlaybookDirty} /></div><div className="span-2 contract-section"><h3>参数合同</h3><p>公开参数会出现在下游的「上游公开参数」列表中；内部参数不会。</p><ParameterTable parameters={parameters} onChange={setParameters} /></div><div className="span-2 contract-section"><h3>精确依赖与公开参数映射</h3><p>先选上游组件版本，再把它的公开参数映射到本组件参数。</p><DependencyEditor dependencies={dependencies} components={components ?? []} currentParameters={parameters} currentComponentId={release.componentId} onChange={setDependencies} /></div>{contractErrors.length ? <div className="span-2 form-validation">{contractErrors.map((item) => <span key={item}>{item}</span>)}</div> : null}</div><footer className="modal-actions">{playbookDirty ? <span className="modal-actions__hint">请先保存 Playbook 内容</span> : actionsDirty ? <span className="modal-actions__hint">动作配置有未保存变更</span> : null}<button type="button" className="button button--quiet" disabled={busy} onClick={close}>取消</button><button className="button button--primary" disabled={busy || playbookDirty}><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</button></footer></form></Modal>;
}

function InspectReleaseModal({ release, components, onClose, onEdit }: { release: ComponentRelease; components: Component[]; onClose: () => void; onEdit?: () => void }) {
  const [selectedAction, setSelectedAction] = useState(0);
  const [playbook, setPlaybook] = useState<PlaybookFile>();
  const [playbookLoading, setPlaybookLoading] = useState(false);
  const [playbookError, setPlaybookError] = useState('');
  const action = release.actions?.[selectedAction];

  useEffect(() => {
    setPlaybook(undefined);
    setPlaybookError('');
    setPlaybookLoading(false);
    if (!action?.playbook) return;
    const controller = new AbortController();
    setPlaybookLoading(true);
    void api.playbook(release.id, action.playbook, controller.signal)
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
          <div><span>Release 类型</span><strong>{release.type ?? 'atomic'}</strong></div>
          <div><span>风险等级</span><strong>{release.riskLevel ?? 'low'}</strong></div>
          <div><span>不兼容变更</span><strong>{release.breaking ? '是' : '否'}</strong></div>
        </div>
        <p>{release.releaseNotes || '未填写发布说明'}</p>
      </section>
      <section className="contract-section">
        <h3>适配环境</h3>
        <EnvironmentConstraints constraints={release.environmentConstraints} />
      </section>
      <ParameterContractList release={release} components={components} />
      <section className="contract-section">
        <h3>依赖映射</h3>
        <DependencyContractList dependencies={release.dependencies ?? []} components={components} />
      </section>
      <section className="contract-section release-action-inspector">
        <h3>生命周期动作与 Playbook</h3>
        {release.actions?.length ? <>
          <label><span>生命周期动作</span><select aria-label="查看生命周期动作" value={selectedAction} onChange={(event) => setSelectedAction(Number(event.target.value))}>{release.actions.map((item, index) => <option key={item.id ?? `${item.type}-${index}`} value={index}>{ACTION_OPTIONS.find((option) => option.value === item.type)?.label ?? item.type} · {item.name || item.type}</option>)}</select></label>
          {action && <div className="release-action-facts">
            <div><span>动作类型</span><strong>{actionLabel}</strong></div>
            <div><span>Playbook 路径</span><code>{action.playbook || '—'}</code></div>
            <div><span>主机组 / Limit</span><strong>{action.hostGroup || '—'} / {action.limit || '—'}</strong></div>
            <div><span>超时 / 风险</span><strong>{action.timeoutSeconds ?? 1800}s / {action.riskLevel ?? 'low'}</strong></div>
            <div><span>允许参数</span><strong>{action.allowedParameters?.join('、') || '无'}</strong></div>
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

function ComponentTemplateImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [text, setText] = useState('[\n  {\n    "component": {\n      "name": "示例组件",\n      "slug": "example-component",\n      "description": "单一职责说明",\n      "layer": "orchestration_core",\n      "category": "worker",\n      "kind": "software",\n      "requiredness": "core_required"\n    },\n    "release": {\n      "version": "1.0.0",\n      "type": "atomic",\n      "releaseNotes": "初始细粒度版本",\n      "environmentConstraints": {\n        "architecture": ["amd64"],\n        "operatingSystem": ["Ubuntu"],\n        "operatingSystemVersion": ["18.04"],\n        "dockerVersion": ["20.10.21"]\n      },\n      "parameters": [],\n      "dependencies": [],\n      "actions": []\n    },\n    "playbooks": []\n  }\n]');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState('');

  async function runImport() {
    setBusy(true);
    try {
      const entries = parseComponentImportTemplate(text);
      setProgress('服务端正在完整预检组件、依赖和 Playbook…');
      const plan = await api.previewComponentImport(entries);
      if (!window.confirm(`导入预览\n${plan.order.length} 个组件\n顺序：${plan.order.join(' → ')}\n\n确认按此计划导入？`)) return;
      setProgress('服务端正在原子写入组件、Draft 和 Playbook…');
      const result = await api.importComponents(entries, plan.planDigest);
      notify('success', `已原子录入 ${result.completedComponents.length} 个细粒度组件`, '服务端已复核计划指纹；版本仍为 Draft，可继续验证并加入候选发布集。');
      onDone();
    } catch (reason) {
      notify('error', '组件模板导入失败', displayError(reason));
    } finally { setBusy(false); setProgress(''); }
  }

  return <Modal size="wide" title="批量导入组件" description="服务端先完整预检且不写入；确认计划指纹后原子创建全部组件、Draft 和独立 Playbook，任一失败则整批不生效。" onClose={onClose}><div className="modal-body"><textarea aria-label="组件模板 JSON" className="code-editor" rows={22} value={text} disabled={busy} onChange={(event) => setText(event.target.value)} spellCheck={false} />{progress && <div className="inline-warning"><span>{progress}</span></div>}</div><footer className="modal-actions"><button className="button button--quiet" disabled={busy} onClick={onClose}>取消</button><button className="button button--primary" disabled={busy} onClick={() => void runImport()}><Upload size={16} /> {busy ? '处理中…' : '预检并导入'}</button></footer></Modal>;
}

function SaveIcon() { return <PencilLine size={15} />; }
