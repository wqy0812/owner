import { ComponentUsagePanel } from '../components/ComponentUsagePanel';
import { useEffect, useMemo, useRef, useState, type Dispatch, type FormEvent, type SetStateAction } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { AlertTriangle, Archive, Beaker, Boxes, CheckCircle2, ChevronDown, ChevronRight, CircleDashed, Container, ExternalLink, FileCode2, FilePlus2, Filter, GitBranch, History, PencilLine, Plus, Rocket, Search, Shield, Trash2, Undo2, Upload, UserRound } from 'lucide-react';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import {
  COMPONENT_LAYERS,
  componentLayer,
} from '../types/componentClassification';
import { ComponentMappingOverview, defaultContractRelease, DependencyContractList, DependencyEditor, mappingCount, ParameterContractList, ParameterTable, parameterContractErrors } from '../components/ParameterEditors';
import { EnvironmentConstraintEditor } from '../components/EnvironmentConstraintEditor';
import type { ComponentSummary, EvidenceSummary, ReleaseEvidenceSummary, RunSummary } from '../types/domain';
import { environmentConstraintDimensions, environmentConstraintGroups, parseConstraintSelection, serializeConstraintSelection } from '../types/environmentConstraints';
import { parseComponentImportTemplate } from './componentTemplateImport';
import type { ActionDefinition, Component, ComponentArtifact, ComponentDependency, ComponentImage, ComponentImageBuild, ComponentLayer, ComponentRelease, ComponentTestPlan, ComponentTestRequest, Environment, ImpactPreview, ParameterDefinition, PlaybookFile, PlaybookWorkspace, WorkExplanation } from '../types/domain';

type ContractSection = 'dependencies' | 'parameters';
type ContractEditIntent = ContractSection | 'all';
type CatalogFilter = 'all' | 'mine' | 'draft' | 'attention';
const REQUIRED_LIFECYCLE_ACTIONS: Array<{ type: ActionDefinition['type']; label: string }> = [
  { type: 'install', label: '安装' },
  { type: 'verify', label: '验证' },
  { type: 'rollback', label: '回退' },
];

function lifecycleSummary(actions: ActionDefinition[] = [], evolution = false) {
  const configured = new Set(actions.map((action) => action.type));
  if (actions.some((action) => action.type === 'install' && action.idempotent)) configured.add('upgrade');
  const required = evolution ? [{ type: 'upgrade' as const, label: '升级' }, ...REQUIRED_LIFECYCLE_ACTIONS.filter((item) => item.type !== 'install')] : REQUIRED_LIFECYCLE_ACTIONS;
  const completed = required.filter((action) => configured.has(action.type));
  return {
    completed: completed.length,
    total: required.length,
    labels: completed.map((action) => action.label).join('、') || '尚未配置',
  };
}

function CompatibilityBadge({ release }: { release: ComponentRelease }) {
  const label = release.compatibility === 'not_applicable' ? '全新基线' : release.compatibility === 'compatible' ? '兼容升级' : '破坏性升级';
  return <span className={release.compatibility === 'breaking' ? 'breaking-badge' : 'release-line-badge'}>{label}</span>;
}

function releaseIsReady(release: ComponentRelease) {
  return release.readiness.status !== 'blocked';
}

function componentNeedsAttention(component: ComponentSummary) {
  return component.needsAttention;
}

function releaseEvidence(evidence: ReleaseEvidenceSummary | undefined, mode: 'install' | 'rollback') {
  return mode === 'install' ? evidence?.currentInstall ?? evidence?.historicalInstall : evidence?.currentRollback ?? evidence?.historicalRollback;
}

function releaseReadyForPublish(release: ComponentRelease) {
  return releaseIsReady(release);
}

function EvidenceLink({ run }: { run?: EvidenceSummary }) {
  if (!run) return <small className="verification-evidence verification-evidence--missing">暂无 Run 证据</small>;
  const stale = !run.matchesContract;
  return <Link className={`verification-evidence${run.status === 'succeeded' && !stale ? ' verification-evidence--passed' : ' verification-evidence--failed'}`} to={`/runs?selected=${run.id}`} onClick={(event) => event.stopPropagation()}>
    {run.environmentName ?? '未知环境'} · {formatTime(run.finishedAt ?? run.createdAt)}
    {run.status === 'failed' || run.status === 'interrupted' ? ' · 验证失败' : run.status === 'cancelled' ? ' · 已取消' : ''}
    {stale ? ' · 合同已变更' : ''}
    <ExternalLink size={12} aria-hidden="true" />
  </Link>;
}

function componentEvidenceLabel(release: ComponentRelease, run: RunSummary) {
  if (run.id === release.readiness.transitionEvidenceRunId) return '当前升级闭环证据';
  if (run.id === release.readiness.installEvidenceRunId) return '当前安装证据';
  if (run.id === release.readiness.rollbackEvidenceRunId) return '当前回退证据';
  return '历史证据';
}

function componentRunLabel(run: RunSummary) {
  switch (run.action) {
    case 'rollback': return '回退验证';
    case 'upgrade': return '升级闭环验证';
    case 'preflight': return '预检验证';
    case 'configure': return '配置验证';
    case 'inspect': return '检查验证';
    case 'install': return '安装验证';
    default: return '组件验证';
  }
}

function scenarioRunLabel(run: RunSummary) {
  return run.kind === 'scenario_test' ? '完整测试' : '正式运行';
}

interface ScenarioRunEvidenceGroup {
  id: string;
  name: string;
  runs: RunSummary[];
  testCount: number;
  runCount: number;
}

function ReleaseRunEvidenceModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user } = useApp();
  const { data: runs, loading, error, reload } = useApiData((signal) => api.releaseRunEvidence(release.id, signal), [release.id, user.id], 'runs');
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

function ReleaseReviewDetails({ release }: { release: ComponentRelease }) {
  const { users } = useApp();
  const { review } = release;
  if (review.status !== 'rejected') return null;
  const reviewer = users.find((item) => item.id === review.reviewedBy)?.name ?? review.reviewedBy;
  return <section className="contract-section release-review-details" aria-label="平台合同审批意见">
    <h3>平台合同审批意见</h3>
    <div className="release-detail-grid">
      <div><span>审核结果</span><StatusPill status={review.status}>已驳回</StatusPill></div>
      <div><span>审批人</span><strong>{reviewer || '—'}</strong></div>
      <div><span>提交时间</span><strong>{review.submittedAt ? formatTime(review.submittedAt) : '—'}</strong></div>
      <div><span>审批时间</span><strong>{review.reviewedAt ? formatTime(review.reviewedAt) : '—'}</strong></div>
    </div>
    <p className="release-review-comment">{review.comment?.trim() ? review.comment : '平台 Owner 未填写审批意见。'}</p>
  </section>;
}

function DraftReadiness({ release, evidence, onContract, onReview, onLifecycle, onImage, onArtifact, onValidate, onPublish }: {
  release: ComponentRelease;
  evidence?: ReleaseEvidenceSummary;
  onContract: () => void;
  onReview: () => void;
  onLifecycle: () => void;
  onImage: () => void;
  onArtifact: () => void;
  onValidate: () => void;
  onPublish: () => void;
}) {
  const lifecycle = lifecycleSummary(release.actions, Boolean(release.parentReleaseId));
  const install = releaseEvidence(evidence, 'install');
  const rollback = releaseEvidence(evidence, 'rollback');
  const transition = evidence?.currentTransition ?? evidence?.historicalTransition;
  const evolution = Boolean(release.parentReleaseId);
  const blockerCodes = new Set(release.readiness.blockers.map((item) => item.code));
  const checks = [
    ![...blockerCodes].some((code) => code.includes('contract') || code.includes('dependency')),
    !blockerCodes.has('lifecycle_action_missing'),
    release.review.status === 'approved',
    ...(evolution ? [Boolean(release.readiness.transitionEvidenceRunId)] : [Boolean(release.readiness.installEvidenceRunId), Boolean(release.readiness.rollbackEvidenceRunId)]),
  ];
  const complete = checks.filter(Boolean).length;
  const ready = complete === checks.length;
  return <article className="panel readiness-panel" aria-label={`Draft ${release.version} 发布就绪度`}>
    <header className="readiness-header">
      <div><span className="panel__icon"><CheckCircle2 size={18} /></span><div><h2>Draft 发布就绪度</h2><p>{release.version} · {evolution ? '按升级与回退往返验证闭环' : '按合同、生命周期、安装验证和回退验证逐项闭环'}</p></div></div>
      <div className={`readiness-score${ready ? ' readiness-score--ready' : ''}`}><strong>{complete}/{checks.length}</strong><span>{ready ? '可以发布' : '仍有阻断项'}</span></div>
    </header>
    <div className="readiness-progress" aria-label={`已完成 ${complete} 项，共 ${checks.length} 项`}><span style={{ width: `${complete / checks.length * 100}%` }} /></div>
    {release.readiness.blockers.length ? <div className="form-validation">{release.readiness.blockers.map((blocker) => <Link key={`${blocker.code}:${blocker.message}`} to={blocker.actionUrl}>{blocker.message}</Link>)}</div> : null}
    <div className="readiness-steps">
      <section className="readiness-step readiness-step--done"><CheckCircle2 size={18} /><div><strong>Release 合同</strong><p>{release.dependencies?.length ?? 0} 项依赖 · {release.parameters?.length ?? 0} 个参数</p></div><button className="icon-text" onClick={onContract}>编辑合同</button></section>
      <section className={release.review.status === 'approved' ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{release.review.status === 'approved' ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>平台合同审核</strong><p>{release.review.status === 'approved' ? '当前参数、Action 与环境绑定已批准' : release.review.status === 'pending' ? '等待平台 Owner 审核' : release.review.status === 'rejected' ? `已驳回：${release.review.comment || '请修改后重提'}` : '请在版本列表提交审核'}</p></div>{release.review.status === 'rejected' && <button className="icon-text" onClick={onReview}>查看审批意见</button>}</section>
      <section className={lifecycle.completed === lifecycle.total ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{lifecycle.completed === lifecycle.total ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>生命周期动作</strong><p>{lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</p></div><button className="icon-text" onClick={onLifecycle}>配置动作</button></section>
      {evolution ? <section className={release.readiness.transitionEvidenceRunId ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{release.readiness.transitionEvidenceRunId ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>升级与回退闭环</strong><EvidenceLink run={transition} /></div><button className="icon-text" onClick={onValidate}>环境验证</button></section> : <>
        <section className={release.readiness.installEvidenceRunId ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{release.readiness.installEvidenceRunId ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>安装与验证</strong><EvidenceLink run={install} /></div><button className="icon-text" onClick={onValidate}>环境验证</button></section>
        <section className={release.readiness.rollbackEvidenceRunId ? 'readiness-step readiness-step--done' : 'readiness-step readiness-step--blocked'}>{release.readiness.rollbackEvidenceRunId ? <CheckCircle2 size={18} /> : <CircleDashed size={18} />}<div><strong>回退与回退后验证</strong><EvidenceLink run={rollback} /></div><button className="icon-text" onClick={onValidate}>环境验证</button></section>
      </>}
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
  const { data: components, loading, error, isRefreshing, reload } = useApiData((signal) => api.componentSummaries(signal), [user.id], 'components');
  const selectedId = searchParams.get('selected') ?? undefined;
  const selectedReleaseId = searchParams.get('release') ?? undefined;
  const deepLinkAction = searchParams.get('action') ?? undefined;
  const handledDeepLink = useRef<string>();
  const [catalogSearch, setCatalogSearch] = useState('');
  const [catalogFilter, setCatalogFilter] = useState<CatalogFilter>('all');
  const [createOpen, setCreateOpen] = useState(false);
  const [componentImportOpen, setComponentImportOpen] = useState(false);
  const [versionBase, setVersionBase] = useState<Component>();
  const [blankVersionBase, setBlankVersionBase] = useState<Component>();
  const [editComponent, setEditComponent] = useState<Component>();
  const [publishRelease, setPublishRelease] = useState<ComponentRelease>();
  const [deprecateRelease, setDeprecateRelease] = useState<ComponentRelease>();
  const [editRelease, setEditRelease] = useState<ComponentRelease>();
  const [impact, setImpact] = useState<ImpactPreview>();
  const [operationExplanation, setOperationExplanation] = useState<WorkExplanation>();
  const [testRelease, setTestRelease] = useState<ComponentRelease>();
  const [evidenceRelease, setEvidenceRelease] = useState<ComponentRelease>();
  const [inspectRelease, setInspectRelease] = useState<ComponentRelease>();
  const [reviewReleaseId, setReviewReleaseId] = useState<string>();
  const [imageRelease, setImageRelease] = useState<ComponentRelease>();
  const [artifactRelease, setArtifactRelease] = useState<ComponentRelease>();
  const [contractReleaseId, setContractReleaseId] = useState<string>();
  const [editingContract, setEditingContract] = useState(false);
  const [contractFocus, setContractFocus] = useState<ContractSection>();
  const [contractDraftIntent, setContractDraftIntent] = useState<ContractEditIntent>();
  const [pendingContractRelease, setPendingContractRelease] = useState<ComponentRelease>();
  const [layerOpen, setLayerOpen] = useState<Partial<Record<ComponentLayer, boolean>>>({});
  const [busy, setBusy] = useState(false);

  const detailId = selectedId ?? components?.[0]?.id;
  const detailQuery = useApiData((signal) => detailId ? api.component(detailId, signal) : Promise.resolve(undefined), [user.id, detailId], ['components', 'runs', 'workbench']);
  const selected = detailQuery.data;
  const { data: editorComponents, error: editorComponentsError, loading: editorComponentsLoading, reload: reloadEditorComponents } = useApiData((signal) => editingContract ? api.components(signal) : Promise.resolve(undefined), [user.id, editingContract], 'components');
  const contractComponents = selected ? [selected] : [];
  const releases = selected?.releases?.length ? selected.releases : selected?.latestRelease ? [selected.latestRelease] : [];
  const releaseGroups = selected?.releaseLines?.length ? selected.releaseLines : releases.length ? [{ id: 'default', name: '默认发布线', releases }] : [];
  const reviewRelease = releases.find((release) => release.id === reviewReleaseId && release.review.status === 'rejected');
  const contractRelease = releases.find((release) => release.id === (contractReleaseId ?? selectedReleaseId))
    ?? (pendingContractRelease?.id === contractReleaseId ? pendingContractRelease : undefined)
    ?? releases.find((release) => release.id === components?.find((component) => component.id === selected?.id)?.defaultReleaseId)
    ?? defaultContractRelease(releases, selected?.latestRelease);
  const editableDrafts = releases.filter((release) => release.state === 'draft');
  const editableDraft = editableDrafts[0];
  const activeDraft = contractRelease?.state === 'draft' ? contractRelease : undefined;
  const releaseWorkItem = selected?.readContext?.workItems.find((item) => item.subject.type === 'component_release' && item.subject.id === contractRelease?.id);
  const visibleEditRelease = editRelease?.componentId === selected?.id ? editRelease : undefined;
  const showContractEditor = Boolean(editingContract && contractRelease?.state === 'draft');
  const deprecationScenarioRunCount = deprecateRelease ? impact?.scenarioRunCount ?? 0 : 0;
  const deprecationBlocked = deprecateRelease?.state === 'released' && deprecationScenarioRunCount > 0;
  const mine = selected?.ownerId === user.id && user.role === 'component_owner';
  const canTest = mine || user.role === 'environment_owner';
  useEffect(() => {
    setContractReleaseId(selectedReleaseId);
    setEvidenceRelease((current) => current?.id === selectedReleaseId ? current : undefined);
    setTestRelease((current) => current?.id === selectedReleaseId ? current : undefined);
  }, [selectedReleaseId]);
  useEffect(() => {
    setContractReleaseId(undefined);
    setEditingContract(false);
    setContractFocus(undefined);
    setContractDraftIntent(undefined);
    setBlankVersionBase(undefined);
    setPendingContractRelease(undefined);
    setEditComponent(undefined);
    setPublishRelease(undefined);
    setDeprecateRelease(undefined);
    setImpact(undefined);
    setTestRelease(undefined);
    setEvidenceRelease(undefined);
    setInspectRelease(undefined);
    setEditRelease(undefined);
    setImageRelease(undefined);
    setArtifactRelease(undefined);
  }, [selected?.id, user.id]);
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
    if (activeDraft) {
      setPendingContractRelease(undefined);
      setEditingContract(true);
      setContractFocus(section);
      return;
    }
    if (editableDrafts.length === 1 && editableDraft) {
      setPendingContractRelease(undefined);
      setContractReleaseId(editableDraft.id);
      setEditingContract(true);
      setContractFocus(section);
      notify('info', '已切换到可编辑 Draft', `${contractRelease?.version ?? '当前版本'} 已不可修改，正在编辑 ${editableDraft.version}。`);
      return;
    }
    if (editableDrafts.length > 1) {
      notify('info', '请先选择 Draft', '发布历史中有多个可编辑 Draft，请先选择目标版本再编辑合同。');
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
      if (catalogFilter === 'draft') return component.ownerId === user.id && component.hasDraft;
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
    return (components?.find((component) => component.id === detailId)?.layer ?? selected?.layer) === layer;
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
      setImpact(await api.releaseImpact(release.id, 'publish'));
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
      setImpact(await api.releaseImpact(release.id, 'deprecate'));
    } catch (reason) {
      notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function confirmPublish() {
    if (!publishRelease) return;
    if (!releaseReadyForPublish(publishRelease)) {
      notify('error', '暂不能发布', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    setBusy(true);
    setOperationExplanation(undefined);
    try {
      await api.publishRelease(publishRelease.id);
      notify('success', '组件版本已发布', publishRelease.parentReleaseId ? '已向精确锁定父 Release 的下游 Owner 生成影响通知。' : '全新发布线已进入 Catalog，不替换现有锁定版本。');
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
    if (!release.candidate && !releaseReadyForPublish(release)) {
      notify('error', '暂不能加入候选集', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    setBusy(true);
    try {
      await api.setReleaseCandidate(release.id, !release.candidate);
      notify('success', release.candidate ? '已撤回候选版本' : '已加入候选发布集', release.candidate ? '集群 Owner 将不再能新引用此 Draft。' : '集群 Owner 现在可以编排和测试；发布场景时将原子发布全部候选版本。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '候选状态更新失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function submitReview(release: ComponentRelease) {
    setBusy(true);
    try {
      await api.submitReleaseReview(release.id);
      notify('success', '参数合同已提交平台审核', '审核绑定当前参数、Action 和环境字段契约；后续修改会自动失效。');
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      notify('error', '提交审核失败', displayError(reason));
    } finally { setBusy(false); }
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

  async function restoreRelease(release: ComponentRelease) {
    if (!window.confirm(`确认恢复 ${release.version}？\n该版本将重新成为可编辑 Draft；若发布线已有新的 Draft 或后继版本，后端会拒绝恢复。`)) return;
    setBusy(true);
    try {
      await api.restoreRelease(release.id);
      notify('success', '组件草稿已恢复', `${release.version} 已恢复为 Draft，可继续编辑和验证。`);
      selectContractRelease(release.id);
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      notify('error', '恢复失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function deleteRelease(release: ComponentRelease) {
    if (!window.confirm(`确认永久删除 ${release.version}？\n将删除该未发布 Release 的合同、Action、托管 Playbook、介质和镜像登记。\n\n只有已废弃、从未发布且没有 Run、构建或任何引用的 Release 可以删除；此操作不可恢复。`)) return;
    setBusy(true);
    try {
      await api.deleteRelease(release.id);
      if (contractReleaseId === release.id || selectedReleaseId === release.id) {
        setContractReleaseId(undefined);
        if (selected) setSearchParams({ selected: selected.id }, { replace: true });
      }
      notify('success', '组件版本已永久删除', `${release.version} 及其未发布内容已从目录移除。`);
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      notify('error', '永久删除失败', displayError(reason));
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
              ] as Array<[CatalogFilter, string]>).map(([value, label]) => <button key={value} type="button" className={catalogFilter === value ? 'active' : ''} aria-pressed={catalogFilter === value} title={value === 'attention' ? '我负责的组件中，尚无版本或有 Draft 的组件' : undefined} onClick={() => setCatalogFilter(value)}>{label}</button>)}
            </div>
            {catalogFilter === 'attention' ? <p>待处理：我负责且尚无版本或有 Draft 的组件。文件与验证问题请查看组件详情。</p> : null}
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
                  <button key={component.id} type="button" disabled={Boolean(editRelease) && selected?.id !== component.id} className={`catalog-item${detailId === component.id ? ' active' : ''}`} onClick={() => selectComponent(component.id)}>
                    <span className="catalog-item__icon"><Boxes size={18} /></span>
                    <span><strong>{component.name}</strong><small>{component.tags.length ? component.tags.join(' · ') : '暂无标签'}</small></span>
                    {component.ownerId === user.id && <span className="mine-dot" title="我负责的组件" />}
                  </button>
                )) : <div className="catalog-layer__empty">本层暂无组件</div>}</div> : null}
              </section>;
            }) : components?.length ? <EmptyState title="没有匹配的组件" description="请调整搜索词或筛选条件。" /> : <EmptyState title="暂无组件" description="组件 Owner 可以创建第一个组件。" />}
          </aside>

          {detailQuery.error && !selected ? <section className="panel"><ErrorBlock message={detailQuery.error} onRetry={() => void detailQuery.reload()} /></section> : detailId && !selected ? <section className="panel"><LoadingBlock label="正在读取组件详情…" /></section> : selected ? <section className="detail-stack">
            <RefreshNotice loading={detailQuery.isRefreshing} error={detailQuery.error} onRetry={() => void detailQuery.reload()} />
            {!selectedInCatalog ? <div className="catalog-selection-notice"><span>当前详情不在目录筛选结果中：{selected.name}</span><button type="button" className="icon-text" onClick={() => { setCatalogSearch(''); setCatalogFilter('all'); }}>清除筛选</button></div> : null}
            <article className="panel component-hero">
              <div className="component-hero__title">
                <span className="component-logo"><Boxes size={26} /></span>
                <div><div className="eyebrow">{componentLayer(selected.layer).code} · {selected.slug ?? 'component'}</div><h2>{selected.name}</h2><p>{selected.description ?? '暂无组件说明'}</p><div className="classification-badges"><span>{componentLayer(selected.layer).label}</span>{selected.tags.map((tag) => <span key={tag}>{tag}</span>)}</div></div>
              </div>
              <div className="component-hero__meta">
                <span><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}</span>
                <span><GitBranch size={15} /> {selected.releaseCount ?? releases.length} 个版本</span>
                {selected.latestRelease && <StatusPill status={selected.latestRelease.state} />}
              </div>
              {mine && <div className="row-actions"><button className="button button--quiet" onClick={() => setEditComponent(selected)}><PencilLine size={16} /> 编辑组件</button><button className="button button--secondary" onClick={() => startEditingContract()}><PencilLine size={16} /> {activeDraft ? '编辑依赖和参数' : editableDrafts.length ? '选择 Draft 编辑合同' : '创建 Draft 编辑合同'}</button><button className="button button--quiet" onClick={() => setBlankVersionBase(selected)}><FilePlus2 size={16} /> 新建空白 Draft</button>{activeDraft ? <button className="button button--quiet" onClick={() => setEditRelease(activeDraft)}><FileCode2 size={16} /> 编辑版本与 Playbook</button> : null}</div>}
            </article>

            <StatusExplanationPanel item={releaseWorkItem} />
            {(mine || user.role === "platform_admin") && <ComponentUsagePanel key={selected.id} component={selected} />}

            {mine && activeDraft ? <DraftReadiness
              release={activeDraft}
              evidence={selected.readContext?.evidence[activeDraft.id]}
              onContract={() => { selectContractRelease(activeDraft.id); setEditingContract(true); }}
              onReview={() => setReviewReleaseId(activeDraft.id)}
              onLifecycle={() => setEditRelease(activeDraft)}
              onImage={() => setImageRelease(activeDraft)}
              onArtifact={() => setArtifactRelease(activeDraft)}
              onValidate={() => { selectContractRelease(activeDraft.id); setTestRelease(activeDraft); }}
              onPublish={() => void previewPublish(activeDraft)}
            /> : null}

            <article className="panel">
              <header className="panel__header"><div><span className="panel__icon"><Rocket size={18} /></span><div><h2>发布历史</h2><p>点击版本可切换下方依赖和参数合同；已发布版本不可修改</p></div></div></header>
              {releases.length ? <div className="release-table">
                <div className="release-table__head"><span>版本</span><span>适配标签</span><span>验证</span><span>依赖 / 动作</span><span>创建 / 发布时间</span><span /></div>
                {releaseGroups.flatMap((line) => [<div key={`line:${line.id}`} className="release-line-header"><GitBranch size={15} /><strong>{line.name}</strong><span>{line.releases.length} 个版本</span></div>, ...line.releases.map((release) => {
                  const active = contractRelease?.id === release.id;
                  const lifecycle = lifecycleSummary(release.actions, Boolean(release.parentReleaseId));
                  const evidence = releaseEvidence(selected.readContext?.evidence[release.id], 'install');
                  const publishReady = releaseReadyForPublish(release);
                  return <div key={release.id} className={`release-row${active ? ' release-row--active' : ''}`} onClick={() => selectContractRelease(release.id)}>
                    <button type="button" className="release-row__version" aria-pressed={active} aria-label={`查看 ${release.version} 的依赖和参数合同`} onClick={() => selectContractRelease(release.id)}>
                      <span className="release-state-line"><strong>{release.version}</strong><StatusPill status={release.state} />{release.candidate && <StatusPill status="candidate">候选集</StatusPill>}<StatusPill status={release.review.status}>{release.review.status === 'approved' ? '合同已审核' : release.review.status === 'pending' ? '合同审核中' : release.review.status === 'rejected' ? '合同已驳回' : '合同待提交'}</StatusPill><CompatibilityBadge release={release} /></span>
                      <small>{release.releaseNotes ?? '未填写发布说明'}</small>
                    </button>
                    <EnvironmentConstraints constraints={release.environmentConstraints} />
                    <div className="release-verification"><StatusPill status={release.readiness.status} /><EvidenceLink run={evidence} /></div>
                    <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖</span><span>{mappingCount(release)} 个参数映射</span><span>{publicCount(release)} 个公开参数</span><span className={`lifecycle-completeness${lifecycle.completed === lifecycle.total ? ' lifecycle-completeness--complete' : ''}`}>动作 {lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</span></div>
                    <div>{release.releasedAt ? formatTime(release.releasedAt) : `创建 ${formatTime(release.createdAt)}`}</div>
                    <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                      {canTest && <button className="icon-text" onClick={() => { selectContractRelease(release.id); setTestRelease(release); }}><Beaker size={15} /> 环境验证</button>}
                      {mine && <button className="icon-text" onClick={() => setEvidenceRelease(release)}><History size={15} /> Run 证据</button>}
                      <button className="icon-text" onClick={() => { selectContractRelease(release.id); setInspectRelease(release); }}>查看详情</button>
                      {release.review.status === 'rejected' && <button className="icon-text" onClick={() => setReviewReleaseId(release.id)}><Shield size={15} /> 审批意见</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => { selectContractRelease(release.id); setEditingContract(true); }}><PencilLine size={15} /> 配置合同</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" onClick={() => setEditRelease(release)}><FileCode2 size={15} /> Playbook</button>}
                      {mine && <button className="icon-text" onClick={() => setImageRelease(release)}><Container size={15} /> 构建镜像</button>}
                      {mine && <button className="icon-text" onClick={() => setArtifactRelease(release)}><Archive size={15} /> 组件介质</button>}
                      {mine && release.state === 'draft' && release.review.status !== 'pending' && release.review.status !== 'approved' && <button className="icon-text" disabled={busy} onClick={() => void submitReview(release)}><Shield size={15} /> 提交合同审核</button>}
                      {mine && release.state === 'draft' && <button className="icon-text" disabled={busy || (!release.candidate && (!publishReady || release.review.status !== 'approved'))} onClick={() => void toggleCandidate(release)}>{release.candidate ? '撤回候选' : '加入候选集'}</button>}
                      {mine && release.state === 'draft' && <button className="icon-text icon-text--primary" disabled={!publishReady || release.review.status !== 'approved'} title={release.review.status !== 'approved' ? '请先通过平台 Owner 合同审核' : publishReady ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={() => void previewPublish(release)}><Rocket size={15} /> 发布</button>}
                      {mine && (release.state === 'draft' || release.state === 'released') && <button className="icon-text icon-text--danger" onClick={() => void previewDeprecate(release)}>{release.state === 'draft' ? '废弃草稿' : '废弃'}</button>}
                      {mine && release.state === 'deprecated' && !release.releasedAt && <button className="icon-text" disabled={busy} onClick={() => void restoreRelease(release)}><Undo2 size={15} /> 恢复 Draft</button>}
                      {mine && release.state === 'deprecated' && !release.releasedAt && <button className="icon-text icon-text--danger" disabled={busy} onClick={() => void deleteRelease(release)}><Trash2 size={15} /> 永久删除</button>}
                    </div>
                  </div>;
                })])}
              </div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。" />}
            </article>

            <ComponentMappingOverview releases={releases} components={contractComponents} />
            {!contractReleaseId && !selectedReleaseId && selected.latestRelease && contractRelease && selected.latestRelease.id !== contractRelease.id ? <p className="mapping-empty contract-hint">当前展示 {contractRelease.lineName} · {contractRelease.version}，因为它有参数映射。默认版本 {selected.latestRelease.lineName} · {selected.latestRelease.version} 没有映射。</p> : null}
            {showContractEditor && contractRelease ? (
              editorComponentsError ? <ErrorBlock message={editorComponentsError} onRetry={() => void reloadEditorComponents()} /> : editorComponentsLoading || !editorComponents ? <LoadingBlock label="正在读取可引用的组件合同…" /> : <ReleaseContractEditor key={contractRelease.id} release={contractRelease} components={editorComponents} focusSection={contractFocus} onCancel={() => { setEditingContract(false); setContractFocus(undefined); }} onSaved={() => { setEditingContract(false); setContractFocus(undefined); signalRefresh('components'); }} />
            ) : (
              <div className="two-column">
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{contractRelease ? `${contractRelease.version} 锁定的上游，以及引用了哪个公开参数` : '锁定上游版本，并标明引用了哪个公开参数'}</p></div></div>
                    {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑直接依赖" onClick={() => startEditingContract('dependencies')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <DependencyContractList dependencies={contractRelease?.dependencies ?? []} components={contractComponents} />
                </article>
                <article className="panel">
                  <header className="panel__header">
                    <div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>{contractRelease ? `${contractRelease.version} 的公开参数可被下游引用；内部参数只给本组件使用` : '公开参数可被下游引用；内部参数只给本组件使用'}</p></div></div>
                    {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑参数合同" onClick={() => startEditingContract('parameters')}><PencilLine size={15} /> 编辑</button> : null}
                  </header>
                  <ParameterContractList release={contractRelease} components={contractComponents} consumers={selected.readContext?.parameterConsumers} />
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
      {blankVersionBase && <NewVersionModal component={blankVersionBase} blank contractIntent="all" onClose={() => setBlankVersionBase(undefined)} onDone={(release) => {
        setBlankVersionBase(undefined);
        signalRefresh('components');
        if (!release) return;
        setPendingContractRelease(release);
        setContractReleaseId(release.id);
        setSearchParams({ selected: release.componentId, release: release.id });
        setContractFocus(undefined);
        setEditingContract(true);
      }} />}
      {inspectRelease && <InspectReleaseModal release={inspectRelease} components={components ?? []} onClose={() => setInspectRelease(undefined)} onEdit={mine && inspectRelease.state === 'draft' ? () => { setInspectRelease(undefined); setContractReleaseId(inspectRelease.id); setEditingContract(true); } : undefined} />}
      {reviewRelease && <Modal title={`${reviewRelease.version} 审批意见`} description="查看当前版本合同的平台 Owner 审核结果与完整备注。" onClose={() => setReviewReleaseId(undefined)}>
        <div className="modal-body"><ReleaseReviewDetails release={reviewRelease} /></div>
        <footer className="modal-actions"><button type="button" className="button button--quiet" onClick={() => setReviewReleaseId(undefined)}>关闭</button></footer>
      </Modal>}
      {visibleEditRelease && <EditReleaseModal key={visibleEditRelease.id} release={visibleEditRelease} releases={releases} onClose={() => setEditRelease(undefined)} onDone={() => { setEditRelease(undefined); signalRefresh('components'); }} />}
      {testRelease && <TestReleaseModal release={testRelease} onClose={() => setTestRelease(undefined)} onDone={() => { setTestRelease(undefined); signalRefresh(['components', 'runs']); }} />}
      {evidenceRelease && <ReleaseRunEvidenceModal release={evidenceRelease} onClose={() => setEvidenceRelease(undefined)} />}
      {imageRelease && <ImageBuildModal release={imageRelease} onClose={() => setImageRelease(undefined)} />}
      {artifactRelease && <ArtifactModal release={artifactRelease} onClose={() => setArtifactRelease(undefined)} />}
      {publishRelease && <Modal title={`发布 ${publishRelease.version}`} description={publishRelease.parentReleaseId ? '发布后版本不可修改；只通知精确锁定父 Release 的下游 Owner。' : '这是全新发布线，不替换现有锁定 Release，也不发送影响通知。'} onClose={() => setPublishRelease(undefined)}>
        <div className="modal-body">
          {impact?.changeKind === 'new_line' ? <div className="warning-callout"><GitBranch size={19} /><div><strong>全新发布线 · {impact.lineName}</strong><p>兼容性不适用；现有组件和场景继续锁定原 Release。</p></div></div> : null}
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关集群 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <StatusExplanationPanel explanation={operationExplanation} title="发布操作被阻断" />
        <footer className="modal-actions"><button className="button button--quiet" onClick={() => { setPublishRelease(undefined); setOperationExplanation(undefined); }}>取消</button><button disabled={busy || !impact} className="button button--primary" onClick={() => void confirmPublish()}>{busy ? '发布中…' : impact?.changeKind === 'new_line' ? '确认发布新线' : '确认发布并通知'}</button></footer>
      </Modal>}
      {deprecateRelease && <Modal title={`${deprecateRelease.state === 'draft' ? '废弃草稿' : '废弃'} ${deprecateRelease.version}`} description={deprecateRelease.state === 'draft' ? '未发布草稿可以随时废弃；废弃后仍可恢复为 Draft，或在没有 Run、构建和引用时永久删除。' : '没有场景 Run 锁定时，该版本可停止作为推荐版本。'} onClose={() => { setDeprecateRelease(undefined); setOperationExplanation(undefined); }}>
        <div className="modal-body">
          <div className="warning-callout warning-callout--danger"><AlertTriangle size={19} /><div><strong>{deprecationBlocked ? '该组件版本不能废弃' : deprecateRelease.state === 'draft' ? '草稿将进入可恢复的废弃状态' : '这是影响下游选择的状态变更'}</strong><p>{deprecationBlocked ? `该版本已经被 ${deprecationScenarioRunCount} 个场景 Run 锁定；请保留该版本以维持运行记录与交付依据。` : deprecateRelease.state === 'draft' ? '废弃不会删除合同或证据；之后可以恢复，满足删除门禁时也可永久删除。' : '请先确认受影响组件和场景。仅被场景引用但从未运行的版本仍可废弃。'}</p></div></div>
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关集群 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
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
  const { user, notify, signalRefresh } = useApp();
  const { data: environments, loading: environmentsLoading } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [mode, setMode] = useState<'build' | 'register'>('build');
  const [tag, setTag] = useState(release.version.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'latest');
  const [environmentId, setEnvironmentId] = useState('');
  const [dockerfile, setDockerfile] = useState<File>();
  const [logicalName, setLogicalName] = useState('main');
  const [sourceRef, setSourceRef] = useState('');
  const [expectedDigest, setExpectedDigest] = useState('');
  const [images, setImages] = useState<ComponentImage[]>(release.images ?? []);
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
    if (mode === 'register') {
      setBusy(true);
      try {
        const image = await api.registerImage(release.id, { logicalName, sourceRef, digest: expectedDigest.trim() || undefined });
        setImages((current) => [image, ...current.filter((item) => item.logicalName !== image.logicalName)].sort((a, b) => a.logicalName.localeCompare(b.logicalName)));
        notify('success', '镜像内容已登记', `${image.logicalName} · ${image.digest}`);
        signalRefresh('components');
      } catch (reason) {
        notify('error', '登记镜像失败', displayError(reason));
      } finally {
        setBusy(false);
      }
      return;
    }
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

  async function repairSource(image: ComponentImage) {
    const next = window.prompt(`更新 ${image.logicalName} 的来源 Ref（内容必须仍为 ${image.digest}）`, image.sourceRef)?.trim();
    if (!next || next === image.sourceRef) return;
    setBusy(true);
    try {
      const updated = await api.updateImageSource(release.id, image.logicalName, next);
      setImages((current) => current.map((item) => item.logicalName === updated.logicalName ? updated : item));
      notify('success', '镜像来源已更新', '内容 Digest、Release 状态和历史证据保持不变。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '更新镜像来源失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function removeImage(image: ComponentImage) {
    setBusy(true);
    try {
      await api.deleteImage(release.id, image.logicalName);
      setImages((current) => current.filter((item) => item.logicalName !== image.logicalName));
      notify('success', '镜像内容已移除');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '移除镜像失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const registry = selectedEnvironment?.currentRevision?.variables.IMAGE_REGISTRY;
  const logs = selectedBuild?.logs?.map((item) => `[${item.stream}] ${item.message}`).join('\n') ?? '';
  return <Modal title={`组件镜像 · ${release.version}`} description="Digest 是内容身份；来源 Ref 可在任何 Release 状态下修复。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      {release.state === 'draft' ? <form onSubmit={(event) => void submit(event)}>
        <div className="tabs" role="tablist"><button type="button" className={mode === 'build' ? 'active' : ''} onClick={() => setMode('build')}>Dockerfile 构建</button><button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>登记已有镜像</button></div>
        {mode === 'build' ? <>
        <div className="warning-callout"><AlertTriangle size={19} /><div><strong>Dockerfile 会在平台构建机上执行</strong><p>构建上下文仅包含该 Dockerfile，不接受本地目录或主机路径；请只上传可信内容。</p></div></div>
        <div className="form-grid image-build-form">
          <label className="span-2"><span>目标环境</span><select aria-label="目标环境" required value={environmentId} disabled={environmentsLoading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{registry ? `IMAGE_REGISTRY=${registry}` : selectedEnvironment ? '该环境尚未配置 IMAGE_REGISTRY，请联系环境 Owner。' : '正在读取可用环境…'}</small></label>
          <label className="span-2"><span>Dockerfile</span><input aria-label="Dockerfile" type="file" required onChange={(event) => setDockerfile(event.target.files?.[0])} /><small>UTF-8，最大 1 MiB，必须包含 FROM 指令</small></label>
          <label className="span-2"><span>镜像标签</span><input value={tag} required pattern="[a-z0-9][a-z0-9._\-]{0,127}" onChange={(event) => setTag(event.target.value.toLowerCase())} /><small>镜像路径固定为 IMAGE_REGISTRY / components / 组件 slug : 标签</small></label>
        </div>
        <button disabled={busy || !dockerfile || !environmentId || !registry} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '提交中…' : '上传并构建'}</button>
        </> : <>
          <div className="form-grid image-build-form">
            <label><span>逻辑名称</span><input required pattern="[a-z][a-z0-9_]{0,63}" value={logicalName} onChange={(event) => setLogicalName(event.target.value.toLowerCase())} /></label>
            <label className="span-2"><span>OCI 来源 Ref</span><input required value={sourceRef} placeholder="registry.example/team/image:tag" onChange={(event) => setSourceRef(event.target.value)} /></label>
            <label className="span-2"><span>预期 Digest（可选）</span><input value={expectedDigest} pattern="sha256:[a-f0-9]{64}" placeholder="sha256:…" onChange={(event) => setExpectedDigest(event.target.value.toLowerCase())} /><small>平台会拉取并解析不可变 Digest；填写后必须完全匹配。</small></label>
          </div>
          <button disabled={busy || !logicalName || !sourceRef} className="button button--primary" type="submit">{busy ? '登记中…' : '探测并登记'}</button>
        </>}
      </form> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>已发布内容身份不可修改</strong><p>仍可在右侧为同一 Digest 修复来源 Ref。</p></div></div>}
      <section className="image-build-results">
        <div className="image-build-history"><strong>当前内容</strong>{images.length ? images.map((image) => <div key={image.logicalName}><span>{image.logicalName}</span><button type="button" className="icon-text" disabled={busy} onClick={() => void repairSource(image)}>修复来源</button>{release.state === 'draft' ? <button type="button" className="icon-button icon-button--danger" disabled={busy} aria-label={`移除镜像 ${image.logicalName}`} onClick={() => void removeImage(image)}><Trash2 size={14} /></button> : null}</div>) : <span>尚未登记镜像内容</span>}</div>
        <div className="image-build-detail"><div className="image-build-ref">{images.map((image) => <div key={image.id}><span>{image.logicalName}</span><code>{image.digest}</code><code>{image.sourceRef}</code></div>)}</div></div>
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
  const [filename, setFilename] = useState('');
  const [sourceUrl, setSourceUrl] = useState('');
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
        saved = await api.registerArtifact(release.id, { alias, filename, sourceUrl, sha256 });
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

  async function repairSource(item: ComponentArtifact) {
    const next = window.prompt(`更新 ${item.alias} 的来源 URL（内容必须仍为 sha256:${item.sha256}）`, item.sourceUrl)?.trim();
    if (!next || next === item.sourceUrl) return;
    setBusy(true);
    try {
      const updated = await api.updateArtifactSource(release.id, item.alias, next);
      setArtifacts((items) => items.map((artifact) => artifact.alias === updated.alias ? updated : artifact));
      notify('success', '介质来源已更新', '内容身份、Release 状态和历史证据保持不变。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '更新介质来源失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const station = selectedEnvironment?.currentRevision?.variables.FILE_STATION;
  return <Modal title={`组件介质 · ${release.version}`} description="文件名和 SHA-256 是内容身份；来源 URL 可在任何 Release 状态下修复。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      {release.state === 'draft' ? <form onSubmit={(event) => void submit(event)}>
        <div className="tabs" role="tablist"><button type="button" className={mode === 'upload' ? 'active' : ''} onClick={() => setMode('upload')}>上传文件</button><button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>登记已有路径</button></div>
        <div className="form-grid image-build-form">
          {mode === 'upload' ? <label className="span-2"><span>上传到环境 FSS</span><select required value={environmentId} disabled={loading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{station ? `FILE_STATION=${station}` : selectedEnvironment ? '该环境尚未配置 FILE_STATION，请联系环境 Owner。' : '正在读取可用环境…'}</small></label> : null}
          <label><span>介质别名</span><input required pattern="[a-z][a-z0-9_]*" value={alias} onChange={(event) => setAlias(event.target.value.toLowerCase())} /><small>运行时注入 alias_path、alias_url、alias_sha256</small></label>
          {mode === 'register' ? <><label><span>文件名</span><input required value={filename} placeholder="example.tar.gz" onChange={(event) => setFilename(event.target.value)} /></label><label className="span-2"><span>来源 URL</span><input required value={sourceUrl} placeholder="https://files.example/example.tar.gz" onChange={(event) => setSourceUrl(event.target.value)} /></label></> : <label><span>介质文件</span><input type="file" required onChange={(event) => setFile(event.target.files?.[0])} /></label>}
          <label><span>SHA-256</span><input required={!checksumFile} value={sha256} pattern="[A-Fa-f0-9]{64}" placeholder="64 位十六进制" onChange={(event) => setSha256(event.target.value)} /></label>
          <label><span>SHA-256 文件</span><input type="file" accept=".sha256,text/plain" onChange={(event) => void loadChecksum(event.target.files?.[0])} /><small>可上传常见的 “hash 文件名” 格式</small></label>
        </div>
        <button disabled={busy || (mode === 'upload' ? (!environmentId || !station || !file) : (!filename || !sourceUrl))} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '保存中…' : mode === 'upload' ? '上传并校验' : '探测并登记'}</button>
      </form> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>已发布内容身份不可修改</strong><p>仍可在右侧为同一 SHA-256 修复来源 URL。</p></div></div>}
      <section className="image-build-results">
        <div className="image-build-history"><strong>当前介质</strong>{artifacts.length ? artifacts.map((item) => <div key={item.alias}><span>{item.alias}</span><button type="button" className="icon-text" disabled={busy} onClick={() => void repairSource(item)}>修复来源</button>{release.state === 'draft' ? <button type="button" className="icon-button icon-button--danger" disabled={busy} aria-label={`移除介质 ${item.alias}`} onClick={() => void detach(item)}><Trash2 size={14} /></button> : null}</div>) : <span>尚未录入介质</span>}</div>
        <div className="image-build-detail"><div className="image-build-ref"><span>内容身份与当前来源</span>{artifacts.map((item) => <div key={item.id}><span>{item.alias} · {item.filename}</span><code>{item.sourceUrl}</code><code>sha256:{item.sha256} · {item.sizeBytes} bytes</code></div>)}</div></div>
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
  const editorRef = useRef<HTMLDivElement>(null);
  const dependenciesRef = useRef<HTMLElement>(null);
  const parametersRef = useRef<HTMLElement>(null);
  const contractErrors = parameterContractErrors(parameters, dependencies, components.flatMap((item) => item.releases ?? []));
  useEffect(() => {
    const node = focusSection === 'parameters' ? parametersRef.current : focusSection === 'dependencies' ? dependenciesRef.current : editorRef.current;
    if (!node) return;
    if (focusSection) node.focus({ preventScroll: true });
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
    <div id="release-contract-editor" className="contract-panels contract-panels--editing" ref={editorRef}>
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

function PlaybookActionEditor({ releaseId, releases, actions, onChange, onPersisted, onDirtyChange }: { releaseId: string; releases: ComponentRelease[]; actions: ActionDefinition[]; onChange: Dispatch<SetStateAction<ActionDefinition[]>>; onPersisted: (actions: ActionDefinition[]) => void; onDirtyChange: (dirty: boolean) => void }) {
	const { notify, platformOptionCategories } = useApp();
  const hostGroups = platformOptionCategories.find((category) => category.kind === 'host_group')?.options ?? [];
  const [selected, setSelected] = useState(0);
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
  const [savedSHA256, setSavedSHA256] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const pendingSelection = useRef<number>();
  const loadRequest = useRef(0);
  const action = actions[selected];
  const releaseIds = new Set(releases.map((release) => release.id));
  const dirty = content !== savedContent;

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
    setSavedContent('');
    setSavedSHA256('');
  }, [actions.length, selected]);

  function selectAction(index: number) {
    if (index === selected) return;
    if (dirty && !window.confirm('当前 Playbook 有未保存内容，确认放弃并切换动作？')) return;
    loadRequest.current += 1;
    setLoading(false);
    setSelected(index);
    setContent('');
    setSavedContent('');
    setSavedSHA256('');
  }

  function updateAction(patch: Partial<ActionDefinition>, index = selected) {
    // Playbook saves are asynchronous. Always merge into the latest parent
    // state so a completed save cannot replace actions added while it ran.
    onChange((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
  }

  function addAction() {
    if (dirty && !window.confirm('当前 Playbook 有未保存内容，确认放弃并新增动作？')) return;
    const next: ActionDefinition = { name: 'install', type: 'install', playbook: '', hostGroup: hostGroups[0]?.value, timeoutSeconds: 1800, riskLevel: 'low' };
    loadRequest.current += 1;
    setLoading(false);
    onChange((current) => {
      pendingSelection.current = current.length;
      return [...current, next];
    });
    const template = '---\n- name: Install component\n  hosts: all\n  become: true\n  tasks: []\n';
    setContent(template);
    setSavedContent('');
    setSavedSHA256('');
  }

  async function currentWorkspaceExpectation() {
    const workspace = await api.playbookWorkspace(releaseId);
    return {
      fileSHA256: savedSHA256 || workspace.files.find((file) => file.path === `${action?.type}.yml`)?.sha256 || '',
      treeSHA256: workspace.treeSha256,
    };
  }

  async function removeAction() {
		if (!action || !window.confirm(action.id
			? `确认立即删除 ${action.type} Action 及入口 ${action.type}.yml？该操作不会因关闭 Draft 编辑器而撤销。`
			: `确认移除尚未保存的 ${action.type} Action？`)) return;
		if (action.id) {
      try {
        const expected = await currentWorkspaceExpectation();
        await api.deleteActionPlaybook(releaseId, action.type, expected.fileSHA256, expected.treeSHA256);
      } catch (reason) {
        notify('error', '删除动作入口失败', displayError(reason));
        return;
      }
    }
		const remaining = actions.filter((_, index) => index !== selected);
		if (action.id) onPersisted(remaining);
		else onChange(remaining);
    loadRequest.current += 1;
    setLoading(false);
    setSelected(Math.max(0, selected - 1));
    setContent('');
    setSavedContent('');
    setSavedSHA256('');
		if (action.id) notify('success', 'Action 已删除', '动作配置、入口文件与工作区清单已原子更新。');
  }

  async function loadPlaybook() {
    if (!action) return;
    const request = ++loadRequest.current;
    setLoading(true);
    try {
      const playbook = await api.playbook(releaseId, action.type);
      if (request !== loadRequest.current) return;
      setContent(playbook.content);
      setSavedContent(playbook.content);
      setSavedSHA256(playbook.sha256);
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
      if (!action) return;
      const expected = await currentWorkspaceExpectation();
      const playbook = await api.uploadPlaybook(releaseId, action, file, expected.fileSHA256, expected.treeSHA256);
		const persistedAction = { ...(playbook.action ?? action), playbook: playbook.path };
		const nextActions = actions.map((item, index) => index === actionIndex ? persistedAction : item);
		setContent(playbook.content);
		onPersisted(nextActions);
      setSavedContent(playbook.content);
      setSavedSHA256(playbook.sha256);
	  notify('success', 'Action 已保存', '动作配置、入口文件与工作区清单已作为一个操作保存。');
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
      const expected = await currentWorkspaceExpectation();
		const playbook = await api.savePlaybook(releaseId, action, content, expected.fileSHA256, expected.treeSHA256);
		const persistedAction = { ...(playbook.action ?? action), playbook: playbook.path };
		const nextActions = actions.map((item, index) => index === actionIndex ? persistedAction : item);
		onPersisted(nextActions);
      setSavedContent(content);
      setSavedSHA256(playbook.sha256);
	  notify('success', 'Action 已保存', '动作配置、入口文件与工作区清单已作为一个操作保存。');
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
        <label><span>动作类型</span><select value={action.type} disabled={Boolean(action.id)} onChange={(event) => {
          const type = event.target.value as ActionDefinition['type'];
          setContent('');
          setSavedContent('');
          setSavedSHA256('');
          updateAction({ type, name: !action.name || action.name === action.type ? type : action.name, idempotent: type === 'install' ? action.idempotent : false });
        }}>{ACTION_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.value} · {option.label}</option>)}</select>{action.id ? <small>已保存动作的类型不可修改；如需更换，请删除后新建。</small> : null}</label>
        <label><span>目标主机组</span><select required value={action.hostGroup ?? ''} onChange={(event) => updateAction({ hostGroup: event.target.value })}><option value="">请选择主机组</option>{hostGroups.map((option) => <option key={option.id} value={option.value}>{option.label}</option>)}</select></label>
        <label><span>超时（秒）</span><input type="number" min={1} value={action.timeoutSeconds ?? 1800} onChange={(event) => updateAction({ timeoutSeconds: Number(event.target.value) })} /></label>
        <label><span>风险级别</span><select value={action.riskLevel ?? 'low'} onChange={(event) => { const riskLevel = event.target.value as NonNullable<ActionDefinition['riskLevel']>; updateAction({ riskLevel, destructive: riskLevel === 'destructive' }); }}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <div className="span-2 platform-managed-path"><span>平台入口</span><code>{action.type}.yml</code><button type="button" className="button button--quiet" disabled={loading} onClick={() => void loadPlaybook()}>{loading ? '读取中…' : '载入编辑器'}</button></div>
        <label><span>Tags（逗号分隔）</span><input value={(action.tags ?? []).join(', ')} onChange={(event) => updateAction({ tags: splitCSV(event.target.value) })} /></label>
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
          <span>入口文件名由动作类型固定</span><span>{dirty ? '有未保存内容' : content ? '内容已保存' : '可上传或载入现有文件'}</span>
        </div>
        <textarea className="code-editor playbook-source__editor" aria-label="Playbook 在线编辑器" spellCheck={false} value={content} placeholder={'---\n- name: Install component\n  hosts: all\n  tasks: []'} onChange={(event) => setContent(event.target.value)} />
        <div className="playbook-source__actions"><button type="button" className="icon-text icon-text--danger" disabled={saving} onClick={() => void removeAction()}><Trash2 size={14} /> 移除动作</button><button type="button" className="button button--secondary" disabled={saving || !content.trim()} onClick={() => void savePlaybook()}><FileCode2 size={15} /> {saving ? '保存中…' : '保存 Playbook'}</button></div>
      </div>
      <PlaybookWorkspaceEditor releaseId={releaseId} refreshToken={`${action.type}:${savedSHA256}`} notify={notify} />
    </> : <><div className="playbook-editor__empty"><FileCode2 size={24} /><p>尚未配置生命周期动作。新增动作后即可上传或在线编写 Playbook。</p><button type="button" className="button button--secondary" onClick={addAction}><Plus size={15} /> 新增第一个动作</button></div><PlaybookWorkspaceEditor releaseId={releaseId} refreshToken="" notify={notify} /></>}
  </section>;
}

export function PlaybookWorkspaceEditor({ releaseId, refreshToken, notify }: { releaseId: string; refreshToken: string; notify: (tone: 'success' | 'error' | 'info', title: string, message?: string) => void }) {
  const [workspace, setWorkspace] = useState<PlaybookWorkspace>();
  const [selectedPath, setSelectedPath] = useState('');
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
	const [loadedSHA256, setLoadedSHA256] = useState('');
  const [editable, setEditable] = useState(false);
  const [newPath, setNewPath] = useState('templates/example.j2');
  const [busy, setBusy] = useState(false);
  const selected = workspace?.files.find((file) => file.path === selectedPath);

  async function refresh(select?: string) {
    const next = await api.playbookWorkspace(releaseId);
	const activePath = select ?? selectedPath;
	const remote = activePath ? next.files.find((file) => file.path === activePath) : undefined;
	const remoteChanged = (remote?.sha256 ?? '') !== loadedSHA256;
	const reloadFile = Boolean(select) || remoteChanged;
	if (reloadFile && content !== savedContent && !window.confirm('辅助文件有未保存内容。是否放弃本地内容并重新载入？')) return;
	if (reloadFile) {
	  if (remote) {
		const file = await api.workspaceFile(releaseId, activePath);
		setContent(file.content ?? ''); setSavedContent(file.content ?? ''); setLoadedSHA256(file.sha256);
		setSelectedPath(activePath); setEditable(file.editable === true);
	  } else {
		setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
		setEditable(false);
	  }
	}
    setWorkspace(next);
    if (select && next.files.some((file) => file.path === select)) setSelectedPath(select);
    else if (selectedPath && !next.files.some((file) => file.path === selectedPath)) {
      setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
    }
  }

  useEffect(() => {
    void refresh().catch((reason) => notify('error', '读取工作区失败', displayError(reason)));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [releaseId, refreshToken]);

  async function openFile(path: string) {
    if (content !== savedContent && !window.confirm('辅助文件有未保存内容，确认放弃？')) return;
    setBusy(true);
    try {
      const file = await api.workspaceFile(releaseId, path);
      setSelectedPath(path);
      setContent(file.content ?? '');
      setSavedContent(file.content ?? '');
	  setLoadedSHA256(file.sha256);
      setEditable(file.editable === true);
    } catch (reason) { notify('error', '读取文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function createTextFile() {
    if (!newPath.trim()) return;
    setBusy(true);
    try {
	  await api.saveWorkspaceFile(releaseId, newPath.trim(), '', '', workspace?.treeSha256 ?? '');
      await refresh(newPath.trim());
      notify('success', '文件已创建', newPath.trim());
    } catch (reason) { notify('error', '创建文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function createDirectory() {
    const directory = newPath.trim().replace(/\/+$/, '');
    if (!directory) return;
    setBusy(true);
    try {
      const marker = `${directory}/.gitkeep`;
	  await api.saveWorkspaceFile(releaseId, marker, '', '', workspace?.treeSha256 ?? '');
      await refresh(marker);
      notify('success', '目录已创建', directory);
    } catch (reason) { notify('error', '创建目录失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function saveSelected() {
    if (!selectedPath) return;
    setBusy(true);
    try {
      if (!selected) return;
	  const saved = await api.saveWorkspaceFile(releaseId, selectedPath, content, loadedSHA256, workspace?.treeSha256 ?? '');
      setSavedContent(content);
	  setLoadedSHA256(saved.sha256);
      setWorkspace(await api.playbookWorkspace(releaseId));
      notify('success', '辅助文件已保存', selectedPath);
    } catch (reason) { notify('error', '保存文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function upload(file?: File) {
    if (!file) return;
    const path = newPath.trim() || file.name;
    setBusy(true);
    try {
      const existingSHA256 = workspace?.files.find((item) => item.path === path)?.sha256 ?? '';
	  await api.uploadWorkspaceFile(releaseId, path, file, existingSHA256, workspace?.treeSha256 ?? '');
      await refresh(path);
      notify('success', '文件已上传', path);
    } catch (reason) { notify('error', '上传文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function renameSelected() {
    if (!selectedPath || isActionEntry(selectedPath)) return;
    const next = window.prompt('输入新的工作区相对路径', selectedPath)?.trim();
    if (!next || next === selectedPath) return;
    setBusy(true);
    try {
      if (!selected) return;
	  const updated = await api.renameWorkspaceFile(releaseId, selectedPath, next, loadedSHA256, workspace?.treeSha256 ?? '');
      setWorkspace(updated); setSelectedPath(next);
      notify('success', '文件已改名', `${selectedPath} → ${next}`);
    } catch (reason) { notify('error', '改名失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function deleteSelected() {
    if (!selectedPath || isActionEntry(selectedPath) || !window.confirm(`确认删除 ${selectedPath}？`)) return;
    setBusy(true);
    try {
      if (!selected) return;
	  setWorkspace(await api.deleteWorkspaceFile(releaseId, selectedPath, loadedSHA256, workspace?.treeSha256 ?? ''));
	  setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
      notify('success', '文件已删除');
    } catch (reason) { notify('error', '删除文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  return <section className="workspace-browser">
    <header><div><h4>Ansible 工作区</h4><p><code>{workspace?.root ?? 'managed/…'}</code> · {workspace?.files.length ?? 0}/1000 文件 · 树摘要 <code>{workspace?.treeSha256?.slice(0, 12) || '—'}</code></p></div><button type="button" className="button button--quiet" disabled={busy} onClick={() => void refresh()}>刷新</button></header>
    <div className="workspace-browser__create"><input aria-label="工作区相对路径" value={newPath} onChange={(event) => setNewPath(event.target.value)} placeholder="templates/config.j2" /><button type="button" className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createDirectory()}>新建目录</button><button type="button" className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createTextFile()}>新建文本文件</button><label className="button button--quiet"><Upload size={14} /> 上传/替换<input type="file" disabled={busy} onChange={(event) => { void upload(event.target.files?.[0]); event.currentTarget.value = ''; }} /></label></div>
    <div className="workspace-browser__body">
      <nav aria-label="Ansible 工作区文件树">{workspace?.files.map((file) => <button type="button" key={file.path} disabled={busy} className={file.path === selectedPath ? 'active' : ''} style={{ paddingLeft: `${12 + Math.max(0, file.path.split('/').length - 1) * 14}px` }} onClick={() => void openFile(file.path)}><span>{file.path}</span><small>{formatBytes(file.sizeBytes)}{isActionEntry(file.path) ? ' · 动作入口' : ''}</small></button>)}{workspace && !workspace.files.length ? <p>工作区尚无文件。</p> : null}</nav>
      <div className="workspace-browser__editor">{selected ? <><div className="workspace-browser__selection"><strong>{selected.path}</strong><span>{selected.mediaType} · {formatBytes(selected.sizeBytes)}</span></div>{!editable ? <p>该文件较大或不是 UTF-8 文本，只能下载、替换或删除。</p> : <textarea className="code-editor" aria-label="辅助文件在线编辑器" disabled={busy || isActionEntry(selected.path)} spellCheck={false} value={content} onChange={(event) => setContent(event.target.value)} />}<div className="playbook-source__actions"><a className="button button--quiet" href={api.workspaceFileDownloadURL(releaseId, selected.path)}>下载</a>{!isActionEntry(selected.path) ? <><button type="button" className="button button--quiet" disabled={busy} onClick={() => void renameSelected()}>改名</button><button type="button" className="icon-text icon-text--danger" disabled={busy} onClick={() => void deleteSelected()}>删除</button></> : null}{editable && !isActionEntry(selected.path) ? <button type="button" className="button button--secondary" disabled={busy || content === savedContent} onClick={() => void saveSelected()}>保存辅助文件</button> : null}</div></> : <p>从左侧选择文件。动作入口请在上方生命周期编辑器维护。</p>}</div>
    </div>
  </section>;
}

function isActionEntry(path: string) {
  return ACTION_OPTIONS.some((option) => path === `${option.value}.yml`);
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
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
  return <Modal title={`编辑 ${component.name}`} description="名称和分类在这里改。参数公开/内部、以及依赖哪个上游参数，属于 Release 合同。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" defaultValue={component.name} required /></label><label><span>标识</span><input name="slug" defaultValue={component.slug} required pattern="[a-z0-9\-]+" /></label><ClassificationFields component={component} /><label className="span-2"><span>说明</span><textarea name="description" defaultValue={component.description} rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存组件'}</button></footer></form></Modal>;
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
  return <Modal title="新建组件" description="选择稳定的架构分层；用途和检索维度使用自由标签。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>组件名称</span><input name="name" required placeholder="例如 containerd" /></label><label><span>标识</span><input name="slug" required pattern="[a-z0-9\-]+" placeholder="containerd" /></label><ClassificationFields /><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建组件'}</button></footer></form></Modal>;
}

function componentInput(form: FormData): Partial<Component> {
  return {
    name: String(form.get('name')),
    slug: String(form.get('slug')),
    description: String(form.get('description')),
    layer: String(form.get('layer')) as ComponentLayer,
    tags: String(form.get('tags') ?? '').split(',').map((tag) => tag.trim()).filter(Boolean),
  };
}

function ClassificationFields({ component }: { component?: Component }) {
  const [layer, setLayer] = useState<ComponentLayer>(component?.layer ?? COMPONENT_LAYERS[0].value);
  return <>
    <label><span>组件层级</span><select name="layer" value={layer} onChange={(event) => setLayer(event.target.value as ComponentLayer)}>{COMPONENT_LAYERS.map((item) => <option key={item.value} value={item.value}>{item.code} · {item.label}</option>)}</select></label>
    <label><span>标签</span><input name="tags" defaultValue={component?.tags.join(', ') ?? ''} placeholder="runtime, core（最多 8 个）" /></label>
  </>;
}

function NewVersionModal({ component, baseRelease, blank = false, contractIntent, onClose, onDone }: { component: Component; baseRelease?: ComponentRelease; blank?: boolean; contractIntent?: ContractEditIntent; onClose: () => void; onDone: (release?: ComponentRelease) => void }) {
	const { notify, platformOptionCategories, signalRefresh } = useApp();
  const dimensions = environmentConstraintDimensions(platformOptionCategories);
  const [busy, setBusy] = useState(false);
  const lines = component.releaseLines ?? [];
  const releasedParents = lines.flatMap((line) => {
    if (!line.evolutionEligible || !line.evolutionParentId) return [];
    const release = line.releases.find((item) => item.id === line.evolutionParentId);
    return release ? [release] : [];
  });
  const eligibleBase = baseRelease?.state === 'released' && releasedParents.some((item) => item.id === baseRelease.id) ? baseRelease : undefined;
  const suggestedParent = eligibleBase ?? releasedParents[0];
  const initialMode: 'new_line' | 'evolution' = blank || !suggestedParent ? 'new_line' : 'evolution';
  const [mode, setMode] = useState<'new_line' | 'evolution'>(initialMode);
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
    setConstraintsDirty(false);
  }
  function changeMode(nextMode: 'new_line' | 'evolution') {
    const nextParentID = parentReleaseId || releasedParents[0]?.id || '';
    const nextSourceID = nextMode === 'evolution' ? nextParentID : templateSourceReleaseId;
    const nextSource = component.releases?.find((item) => item.id === nextSourceID);
    changeConstraintSource(nextSource, () => {
      setMode(nextMode);
      if (nextMode === 'evolution' && !parentReleaseId) setParentReleaseId(nextParentID);
    });
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
      environmentConstraints,
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
      if (!window.confirm(`Draft 预览\n${relation}\n${plan.actions.length} 个 Action · ${plan.playbooks.length} 个 Playbook · ${plan.artifactCount} 个介质 · ${plan.imageCount} 个镜像${removed}\n\n确认创建？`)) return;
      const release = await api.createReleaseDraft(component.id, { ...input, expectedPlanDigest: plan.planDigest });
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
  const title = contractIntent ? `创建 Draft 编辑${contractLabel}` : `创建 ${component.name} Draft`;
  const description = '先选择创建全新发布线，或基于现有发布线最新 Released 版本继续演进。复制内容不等于建立升级关系。';
  return <Modal size="wide" title={title} description={description} onClose={onClose}>
    <form onSubmit={(event) => void submit(event)}>
      <div className="form-grid">
        <label className="span-2"><span>创建方式</span><select value={mode} onChange={(event) => changeMode(event.target.value as 'new_line' | 'evolution')}><option value="new_line">创建全新发布线</option>{releasedParents.length ? <option value="evolution">基于现有发布线演进</option> : null}</select></label>
        {blockedEvolutionReasons.length ? <div className="span-2"><small>{blockedEvolutionReasons.map((line) => `${line.name}：${line.evolutionBlockedReason}`).join('；')}</small></div> : null}
        {mode === 'new_line' ? <>
          <label><span>发布线名称</span><input name="lineName" required placeholder="例如 Kubernetes 1.34" /></label>
          <label><span>内容模板（可选）</span><select value={templateSourceReleaseId} onChange={(event) => changeTemplate(event.target.value)}><option value="">空白创建</option>{(component.releases ?? []).filter((item) => item.state === 'released' || item.state === 'deprecated').map((item) => <option key={item.id} value={item.id}>{item.lineName} · {item.version}</option>)}</select></label>
        </> : <>
          <label><span>演进来源</span><select value={parentReleaseId} required onChange={(event) => changeParent(event.target.value)}>{releasedParents.map((item) => <option key={item.id} value={item.id}>{item.lineName} · {item.version}</option>)}</select></label>
          <label><span>升级兼容性</span><select name="compatibility" defaultValue="compatible"><option value="compatible">兼容升级</option><option value="breaking">破坏性升级</option></select></label>
        </>}
        <label><span>新版本</span><input name="version" required placeholder="v1.1.0" /></label>
        <label><span>风险级别</span><select name="riskLevel" defaultValue={source?.riskLevel ?? 'low'}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <label className="span-2"><span>发布说明</span><textarea name="notes" required rows={4} placeholder="说明变化和下游注意事项" /></label>
        <div className="span-2"><EnvironmentConstraintEditor value={constraints} onChange={(next) => { setConstraints(next); setConstraintsDirty(true); }} /></div>
      </div>
      <footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建 Draft'}</button></footer>
    </form>
  </Modal>;
}

function TestReleaseModal({ release, onClose, onDone }: { release: ComponentRelease; onClose: () => void; onDone: () => void }) {
  const { notify, user } = useApp();
  const { data: environments } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const { data: components } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const installAction = (['upgrade', 'install', 'configure', 'preflight', 'inspect'] as const)
    .map((type) => release.actions?.find((action) => action.type === type))
    .find((action) => action !== undefined);
  const rollbackAction = release.actions?.find((action) => action.type === 'rollback');
  const rollbackOnlyAllowed = !rollbackAction?.fromReleaseId && !rollbackAction?.toReleaseId;
  const [mode, setMode] = useState<'install_verify' | 'rollback' | 'evolution_round_trip'>(() => release.parentReleaseId ? 'evolution_round_trip' : installAction ? 'install_verify' : 'rollback');
  const selectedAction = mode === 'rollback' ? rollbackAction : installAction;
  const [environmentId, setEnvironmentId] = useState('');
  const [rollbackVerificationKind, setRollbackVerificationKind] = useState<'target_release' | 'rollback_only'>(() => rollbackAction?.toReleaseId ? 'target_release' : 'rollback_only');
  const [verifyReleaseId, setVerifyReleaseId] = useState(rollbackAction?.toReleaseId ?? '');
  const [busy, setBusy] = useState<'preview' | 'submit'>();
  const [plan, setPlan] = useState<ComponentTestPlan>();
  const [planError, setPlanError] = useState<string>();
  const [planExplanation, setPlanExplanation] = useState<WorkExplanation>();
  const component = components?.find((item) => item.id === release.componentId);
  const retainedVerifyReleases = (component?.releases ?? []).filter((item) => item.id !== release.id && (item.state === 'released' || item.state === 'deprecated') && item.actions?.some((action) => action.type === 'verify'));
  function invalidatePlan() {
    setPlan(undefined);
    setPlanError(undefined);
    setPlanExplanation(undefined);
  }

  function requestInput(expectedPlanDigest?: string): ComponentTestRequest {
    return {
      environmentId,
      mode,
      rollbackVerification: mode === 'rollback'
        ? rollbackOnlyAllowed && rollbackVerificationKind === 'rollback_only'
          ? { kind: 'rollback_only' }
          : { kind: 'target_release', releaseId: verifyReleaseId }
        : undefined,
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
      const label = mode === 'rollback' ? '回退验证' : mode === 'evolution_round_trip' ? '升级闭环验证' : '安装验证';
      notify('success', `${label}已提交`, mode === 'rollback' ? '回退动作会继续遵循破坏性操作审批流程。' : '参数来自 Release 测试值与所选 Environment Revision。');
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
      <div className={`validation-risk-summary${mode === 'rollback' || mode === 'evolution_round_trip' ? ' validation-risk-summary--danger' : ''}`}><AlertTriangle size={19} /><div><strong>{mode === 'rollback' ? '回退验证将修改环境状态' : mode === 'evolution_round_trip' ? '升级闭环会安装父版本、升级并回退' : '安装验证会在目标环境执行主动作'}</strong><p>第一步只预览锁定计划，不创建 Run；点击最终确认后才会提交执行。</p></div></div>
      <label><span>验证模式</span><select aria-label="验证模式" value={mode} onChange={(event) => { setMode(event.target.value as 'install_verify' | 'rollback' | 'evolution_round_trip'); invalidatePlan(); }}>{release.parentReleaseId ? <option value="evolution_round_trip">升级闭环验证（父版本安装 → 升级 → 回退）</option> : <><option value="install_verify" disabled={!installAction}>安装验证（主动作 + verify）</option><option value="rollback" disabled={!rollbackAction}>回退验证（rollback + 可选目标版本 verify）</option></>}</select></label>
      {mode === 'rollback' ? <>
        <div className="warning-callout"><AlertTriangle size={19} /><div><strong>回退合同不可变</strong><p>Draft 回退始终按已保存的来源/目标合同执行；下方目标只决定追加哪个 Release 的 verify，不会覆盖回退合同。</p></div></div>
        <div className="rollback-contract"><strong>Draft 回退合同</strong><span>来源：{releaseLabel(rollbackAction?.fromReleaseId)}</span><span>目标：{releaseLabel(rollbackAction?.toReleaseId)}</span></div>
        <label><span>回退验证策略</span><select aria-label="回退验证策略" value={rollbackVerificationKind} onChange={(event) => { setRollbackVerificationKind(event.target.value as 'target_release' | 'rollback_only'); invalidatePlan(); }}><option value="target_release">Draft 回退 + 所选 Release verify</option>{rollbackOnlyAllowed ? <option value="rollback_only">仅执行 Draft 回退</option> : null}</select></label>
        {rollbackVerificationKind === 'target_release' ? <label><span>verify 目标 Release</span><select aria-label="verify 目标 Release" value={verifyReleaseId} onChange={(event) => { setVerifyReleaseId(event.target.value); invalidatePlan(); }}><option value="">请选择包含 verify 的保留版本</option>{retainedVerifyReleases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.state}{item.id === rollbackAction?.toReleaseId ? ' · 合同目标' : ''}</option>)}</select></label> : null}
      </> : null}
      <label><span>目标环境</span><select aria-label="目标环境" value={environmentId} onChange={(event) => { setEnvironmentId(event.target.value); invalidatePlan(); }}><option value="">请选择环境</option>{environments?.map((env: Environment) => <option key={env.id} value={env.id}>{env.name} · {env.status ?? 'ready'}</option>)}</select></label>
      <div className="inline-warning"><span>本次验证不接受临时 Run 参数；组件参数使用 Release 固定值/测试值，环境参数使用所选 Environment Revision。</span></div>
      {planError ? <div className="form-validation" role="alert">{planError}</div> : null}
      <StatusExplanationPanel explanation={planExplanation} title="验证操作被阻断" />
      {plan ? <section className="test-plan-preview" aria-label="完整执行计划"><header><div><strong>完整执行计划</strong><small>Environment Revision {plan.environmentRevisionId}</small></div>{plan.requiresApproval ? <StatusPill status="awaiting_approval">需环境 Owner 审批</StatusPill> : <StatusPill status="ready">可直接排队</StatusPill>}</header><div>{plan.steps.map((step) => <article key={`${step.order}-${step.releaseId}-${step.action}`}><span>{step.order}</span><div><strong>{step.componentName} · {step.action}</strong><p>所属版本 {step.releaseVersion} · {step.releaseId}</p><small>Playbook {step.playbook}{step.limit ? ` · 目标 ${step.limit}` : ''}</small>{step.action === 'rollback' ? <small>不可变合同：{step.fromReleaseVersion || step.fromReleaseId || '独立'} → {step.toReleaseVersion || step.toReleaseId || '独立'}</small> : null}{step.backupRef ? <small>备份基线：Run {step.backupInstallRunId} · {step.backupRef}</small> : null}</div></article>)}</div>{plan.deliveryRequirements.length ? <div className="backup-list"><strong>需要环境 Owner 逐项决定</strong>{plan.deliveryRequirements.map((item) => <section key={item.id}><strong>{item.kind === 'artifact' ? '介质' : '镜像'} · {item.name}</strong><p>{item.identity}</p><small>来源可读 · 目标{item.target ? '未命中' : '未配置'} · {item.transferAvailable ? '可选择直用或平移' : '只能直接使用来源'}</small></section>)}</div> : null}</section> : null}
    </div>
    <footer className="modal-actions"><button type="button" className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>取消</button><button type="button" className="button button--quiet" disabled={!environmentId || !selectedAction || Boolean(busy) || (mode === 'rollback' && rollbackVerificationKind === 'target_release' && !verifyReleaseId)} onClick={() => void preview()}><Beaker size={16} /> {busy === 'preview' ? '规划中…' : plan ? '刷新执行计划' : '预览执行计划'}</button><button type="button" className={mode === 'rollback' || mode === 'evolution_round_trip' ? 'button button--danger-soft' : 'button button--primary'} disabled={!plan || Boolean(busy)} onClick={() => void run()}>{busy === 'submit' ? '提交中…' : mode === 'rollback' ? '确认提交回退验证' : mode === 'evolution_round_trip' ? '确认提交升级闭环验证' : '确认提交安装验证'}</button></footer>
  </Modal>;
}

function EditReleaseModal({ release, releases, onClose, onDone }: { release: ComponentRelease; releases: ComponentRelease[]; onClose: () => void; onDone: () => void }) {
  const { notify, user, platformOptionCategories, signalRefresh } = useApp();
  const dimensions = environmentConstraintDimensions(platformOptionCategories);
  const { data: components } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const [busy, setBusy] = useState(false);
  const [constraints, setConstraints] = useState(() => parseConstraintSelection(release.environmentConstraints, dimensions));
  const [constraintsCatalogReady, setConstraintsCatalogReady] = useState(dimensions.length > 0);
  useEffect(() => {
    if (constraintsCatalogReady || !dimensions.length) return;
    setConstraints(parseConstraintSelection(release.environmentConstraints, dimensions));
    setConstraintsCatalogReady(true);
  }, [constraintsCatalogReady, dimensions, release.environmentConstraints]);
  const [parameters, setParameters] = useState<ParameterDefinition[]>(release.parameters ?? []);
  const [dependencies, setDependencies] = useState<ComponentDependency[]>(release.dependencies ?? []);
  const [actions, setActions] = useState<ActionDefinition[]>(release.actions ?? []);
  const [savedActions, setSavedActions] = useState(() => JSON.stringify(release.actions ?? []));
  const [playbookDirty, setPlaybookDirty] = useState(false);
  const actionsDirty = JSON.stringify(actions) !== savedActions;
  const contractErrors = parameterContractErrors(parameters, dependencies, components?.flatMap((item) => item.releases ?? []) ?? []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
		if (playbookDirty || actionsDirty) { notify('error', '请先保存当前 Action', 'Action 配置或在线编辑器内容仍有未保存变更。'); return; }
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
        releaseNotes: String(form.get('notes')),
        compatibility: release.parentReleaseId ? String(form.get('compatibility')) as 'compatible' | 'breaking' : 'not_applicable',
        riskLevel: String(form.get('riskLevel')) as NonNullable<ComponentRelease['riskLevel']>,
        environmentConstraints: serializeConstraintSelection(constraints, dimensions),
        parameters,
        dependencies,
        actions: submittedActions,
      });
      setActions(saved.actions ?? []);
      setSavedActions(JSON.stringify(saved.actions ?? []));
		notify('success', 'Draft 配置已保存', clearedCredentials ? `已清除 ${clearedCredentials} 个 CredentialRef；其余 Draft 配置也已更新。` : '版本信息、参数合同和依赖映射已更新。');
      onDone();
    } catch (reason) { notify('error', '保存 Draft 失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  const close = () => { if (!busy) onClose(); };
  return <Modal size="wide" title={`配置 Draft ${release.version}`} description="维护版本信息、参数合同、依赖映射，以及可上传和在线编辑的 Playbook。Action 保存/删除会立即原子持久化，关闭编辑器不会撤销。" onClose={close}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>版本</span><input name="version" defaultValue={release.version} required /></label><label><span>风险级别</span><select name="riskLevel" defaultValue={release.riskLevel ?? 'low'}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>{release.parentReleaseId ? <label><span>升级兼容性</span><select name="compatibility" defaultValue={release.compatibility}><option value="compatible">兼容升级</option><option value="breaking">破坏性升级</option></select></label> : <div><span>版本关系</span><strong>全新基线 · 不适用升级兼容性</strong></div>}<label className="span-2"><span>发布说明</span><textarea name="notes" defaultValue={release.releaseNotes} rows={3} required /></label><div className="span-2"><EnvironmentConstraintEditor value={constraints} onChange={setConstraints} /></div><div className="span-2"><PlaybookActionEditor releaseId={release.id} releases={releases} actions={actions} onChange={setActions} onPersisted={(persistedActions) => { setActions(persistedActions); setSavedActions(JSON.stringify(persistedActions)); signalRefresh('components'); }} onDirtyChange={setPlaybookDirty} /></div><div className="span-2 contract-section"><h3>参数合同</h3><p>公开参数会出现在下游的「上游公开参数」列表中；内部参数不会。</p><ParameterTable parameters={parameters} onChange={setParameters} /></div><div className="span-2 contract-section"><h3>精确依赖与公开参数映射</h3><p>先选上游组件版本，再把它的公开参数映射到本组件参数。</p><DependencyEditor dependencies={dependencies} components={components ?? []} currentParameters={parameters} currentComponentId={release.componentId} onChange={setDependencies} /></div>{contractErrors.length ? <div className="span-2 form-validation">{contractErrors.map((item) => <span key={item}>{item}</span>)}</div> : null}</div><footer className="modal-actions">{playbookDirty || actionsDirty ? <span className="modal-actions__hint">请先保存当前 Action</span> : null}<button type="button" className="button button--quiet" disabled={busy} onClick={close}>取消</button><button className="button button--primary" disabled={busy || playbookDirty || actionsDirty}><SaveIcon /> {busy ? '保存中…' : '保存 Draft'}</button></footer></form></Modal>;
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
	if (!action) return;
    const controller = new AbortController();
    setPlaybookLoading(true);
	void api.playbook(release.id, action.type, controller.signal)
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
          {action && <div className="release-action-facts">
            <div><span>动作类型</span><strong>{actionLabel}</strong></div>
            <div><span>Playbook 路径</span><code>{action.playbook || '—'}</code></div>
            <div><span>主机组</span><strong>{action.hostGroup || '—'}</strong></div>
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
      {onEdit && <button type="button" className="button button--primary" onClick={onEdit}><PencilLine size={15} /> 编辑可见性与映射</button>}
    </footer>
  </Modal>;
}

function publicCount(release: ComponentRelease) {
  return (release.parameters ?? []).filter((item) => item.visibility === 'public').length;
}

function EnvironmentConstraints({ constraints }: { constraints?: Record<string, unknown> }) {
  const { platformOptionCategories } = useApp();
  const groups = environmentConstraintGroups(constraints, environmentConstraintDimensions(platformOptionCategories));
  if (!groups.length) return <div className="env-constraints env-constraints--empty">未声明适配标签</div>;
  return <div className="env-constraints">{groups.map((group) => (
    <span className="env-constraint" key={group.key}>
      <em>{group.label}</em>
      {group.values.map((value) => <b key={value}>{value}</b>)}
    </span>
  ))}</div>;
}

function ComponentTemplateImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [text, setText] = useState('[\n  {\n    "component": {\n      "name": "示例组件",\n      "slug": "example-component",\n      "description": "单一职责说明",\n      "layer": "orchestration_core",\n      "tags": ["worker", "core"]\n    },\n    "release": {\n      "version": "1.0.0",\n      "releaseNotes": "初始细粒度版本",\n      "environmentConstraints": {\n        "architecture": ["amd64"],\n        "operatingSystem": ["Ubuntu"],\n        "operatingSystemVersion": ["18.04"]\n      },\n      "parameters": [],\n      "dependencies": [],\n      "actions": []\n    },\n    "playbooks": []\n  }\n]');
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
