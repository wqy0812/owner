import { EnvironmentConstraintEditor } from '../components/EnvironmentConstraintEditor';
import { parseConstraintSelection, environmentConstraintDimensions, serializeConstraintSelection, type ConstraintSelection } from '../types/environmentConstraints';
import { adaptationValues, scenarioAdaptationIssues, environmentAdaptationIssues, commonAdaptationRanges } from '../types/adaptation';
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import {
  Background,
  Controls,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Beaker, Boxes, CheckCircle2, Download, GitCommitHorizontal, LockKeyhole, Network, Plus, Rocket, Save, Settings2, Table2, Trash2, Undo2, Upload } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { describeParameterMapping, mappedParameterNames, ParameterValueEditor } from '../components/ParameterEditors';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { COMPONENT_LAYERS, componentLayer } from '../types/componentClassification';
import { executableActionTypes, type CandidateReleaseSet, type Component, type ComponentRelease, type Environment, type ParameterDefinition, type Scenario, type ScenarioEdge, type ScenarioNodeData, type WorkExplanation } from '../types/domain';
import { isScenarioNodeReachable, reconcileScenarioGraph, scenarioDependenciesForNode, scenarioDependencyKey } from './scenarioGraph';
import { parseScenarioTemplate, serializeScenarioTemplate, validateScenarioTemplateReferences } from './scenarioTemplate';

type FlowNode = Node<ScenarioNodeData>;
type FlowEdge = Edge & ScenarioEdge;

export function isNodeReachable(source: string, target: string, edges: Array<Pick<Edge, 'source' | 'target'>>) {
  return isScenarioNodeReachable(source, target, edges);
}

function ComponentNode({ data, selected }: NodeProps<FlowNode>) {
  return <div className={`flow-node${selected ? ' selected' : ''}`}>
    <Handle type="target" position={Position.Left} />
    <div className="flow-node__top"><span><Boxes size={15} /></span><small>{data.layer ? componentLayer(data.layer).code : 'COMPONENT'}</small></div>
    <strong>{data.label}</strong>
    <div className="flow-node__meta"><span>{data.version ?? '—'}</span><span>{data.action ?? 'install'}</span></div>
    <Handle type="source" position={Position.Right} />
  </div>;
}

const nodeTypes = { component: ComponentNode };

function inheritedHostGroup(release: ComponentRelease | undefined, action: ScenarioNodeData['action']): string {
  const definition = release?.actions?.find((item) => item.type === action)
    ?? (action === 'upgrade' ? release?.actions?.find((item) => item.type === 'install' && item.idempotent) : undefined);
  return definition?.hostGroup ?? '';
}

export function ScenariosPage() {
  const { user, notify, signalRefresh, platformOptionCategories } = useApp();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: scenarios, loading, error, isRefreshing, reload } = useApiData((signal) => api.scenarios(signal), [user.id], 'scenarios');
  const { data: components } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const { data: environments } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench');
  const selectedScenarioId = searchParams.get('selected') ?? '';
  const selectedScenario = useMemo(() => scenarios?.find((item) => item.id === selectedScenarioId) ?? scenarios?.[0], [scenarios, selectedScenarioId]);
  const currentRevision = selectedScenario?.currentRevision;
  const selectedRevisionId = searchParams.get('revision') ?? undefined;
  const loadedRevisionRef = useRef<{ scenarioId?: string; currentRevisionId?: string }>({});
  const revision = useMemo(() => selectedScenario?.revisions?.find((item) => item.id === selectedRevisionId) ?? currentRevision, [currentRevision, selectedRevisionId, selectedScenario?.revisions]);
  const revisionWorkItem = workbench?.items.find((item) => item.subject.type === 'scenario_revision' && item.subject.id === revision?.id);
  const [constraints, setConstraints] = useState<ConstraintSelection>({});
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<FlowEdge>([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  const [validation, setValidation] = useState<{ valid: boolean; errors: string[] }>();
  const [testOpen, setTestOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [candidateSet, setCandidateSet] = useState<CandidateReleaseSet>();
  const [operationExplanation, setOperationExplanation] = useState<WorkExplanation>();
  const [deleteExplanation, setDeleteExplanation] = useState<WorkExplanation>();
  const [view, setView] = useState<'graph' | 'table' | 'parameters'>('graph');
  const [testEnvironment, setTestEnvironment] = useState('');
  const [busy, setBusy] = useState<string>();

  const isCurrentRevision = Boolean(revision && revision.id === (selectedScenario?.currentRevisionId ?? currentRevision?.id));
  const editable = Boolean(user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && isCurrentRevision && revision?.state === 'draft');
  const canLaunch = Boolean((user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id) || user.role === 'environment_owner');
  const canLaunchRevision = Boolean(canLaunch && revision && (isCurrentRevision || revision.state === 'released'));
  const canAbandonDraft = Boolean(editable && selectedScenario?.revisions?.some((item) => item.id !== revision?.id && (item.state === 'released' || item.state === 'deprecated')));
  const canRequestScenarioDelete = Boolean(
    selectedScenario?.ownerId === user.id
    && selectedScenario.revisions?.length
    && selectedScenario.revisions.every((item) => item.state === 'draft'),
  );
  useEffect(() => {
    const previous = loadedRevisionRef.current;
    const followedPreviousCurrent = !selectedRevisionId || selectedRevisionId === previous.currentRevisionId;
    if (selectedScenario && previous.scenarioId === selectedScenario.id && previous.currentRevisionId && previous.currentRevisionId !== currentRevision?.id && followedPreviousCurrent) {
      setSearchParams({ selected: selectedScenario.id, ...(currentRevision?.id ? { revision: currentRevision.id } : {}) }, { replace: true });
    }
    loadedRevisionRef.current = { scenarioId: selectedScenario?.id, currentRevisionId: currentRevision?.id };
  }, [currentRevision?.id, selectedRevisionId, selectedScenario?.id, setSearchParams]);

  useEffect(() => {
    const action = searchParams.get('action');
    if (!action || !selectedScenario || !revision) return;
    if (action === 'test' && canLaunchRevision) setTestOpen(true);
    if (action === 'publish' && revision.state === 'test_passed' && selectedScenario.ownerId === user.id) void previewPublish();
    if (action === 'inspect') setView('table');
    const next = new URLSearchParams(searchParams);
    next.delete('action');
    setSearchParams(next, { replace: true });
  }, [canLaunchRevision, revision, searchParams, selectedScenario, setSearchParams, user.id]);

  useEffect(() => {
    setConstraints(Object.fromEntries(Object.entries(revision?.environmentConstraints ?? {}).map(([key, value]) => [key, adaptationValues(value)])));
    setNodes((revision?.nodes ?? []).map((node) => ({ ...node, type: 'component' })) as FlowNode[]);
    setEdges((revision?.edges ?? []) as FlowEdge[]);
    setSelectedNodeId(undefined);
    setValidation(undefined);
  }, [revision?.id, setEdges, setNodes]);

  useEffect(() => setDeleteExplanation(undefined), [selectedScenario?.id]);

  const releaseMetadata = useMemo(() => new Map(
    components?.flatMap((component) => (component.releases ?? []).map((release) => [release.id, { component, release }] as const)) ?? [],
  ), [components]);
  const releasesByID = useMemo(() => new Map([...releaseMetadata].map(([releaseID, item]) => [releaseID, item.release])), [releaseMetadata]);
  const reconciledGraph = useMemo(() => reconcileScenarioGraph(nodes, edges, releasesByID), [edges, nodes, releasesByID]);

  useEffect(() => {
    if (!editable || releasesByID.size === 0) return;
    const sourceSignature = (items: FlowNode[]) => JSON.stringify(items.map((node) => [node.id, node.data.dependencySources ?? {}]));
    const edgeSignature = (items: Array<Pick<ScenarioEdge, 'id' | 'source' | 'target' | 'kind' | 'dependencyId'>>) => JSON.stringify(items.map((edge) => [edge.id, edge.source, edge.target, edge.kind, edge.dependencyId]));
    if (sourceSignature(nodes) !== sourceSignature(reconciledGraph.nodes as FlowNode[])) setNodes(reconciledGraph.nodes as FlowNode[]);
    if (edgeSignature(edges) !== edgeSignature(reconciledGraph.edges)) setEdges(reconciledGraph.edges as FlowEdge[]);
  }, [editable, edges, nodes, reconciledGraph, releasesByID.size, setEdges, setNodes]);
  const displayNodes = useMemo(() => nodes.map((node) => {
    const metadata = releaseMetadata.get(node.data.releaseId);
    return {
      ...node,
      data: {
        ...node.data,
        componentId: node.data.componentId || metadata?.component.id || '',
        label: node.data.label || metadata?.component.name || node.id,
        version: node.data.version ?? metadata?.release.version,
        layer: node.data.layer ?? metadata?.component.layer,
      },
    };
  }), [nodes, releaseMetadata]);
  const displayEdges = useMemo(() => reconciledGraph.edges.map((edge) => ({
    ...edge,
    className: edge.kind === 'dependency' ? 'scenario-edge scenario-edge--dependency' : 'scenario-edge scenario-edge--sequence',
    label: edge.kind === 'dependency' ? '🔒' : undefined,
    deletable: editable && edge.kind !== 'dependency',
    focusable: edge.kind !== 'dependency',
  })), [editable, reconciledGraph.edges]);
  const dependencyIssues = reconciledGraph.issues;

  const selectedNode = (reconciledGraph.nodes as FlowNode[]).find((node) => node.id === selectedNodeId);
  const selectedNodeMetadata = selectedNode ? releaseMetadata.get(selectedNode.data.releaseId) : undefined;
  const selectedNodeComponent = selectedNodeMetadata?.component ?? components?.find((component) => component.id === selectedNode?.data.componentId);
  const selectedRelease = selectedNodeMetadata?.release;
  const mappedTargets = mappedParameterNames(selectedRelease);
  const nodeDependencies = selectedNode ? scenarioDependenciesForNode(selectedNode, selectedRelease) : [];
  const sourceOptionsByDependency = Object.fromEntries(nodeDependencies.map((dependency) => {
    const candidates = displayNodes.filter((node) => node.id !== selectedNodeId && node.data.releaseId === dependency.releaseId);
    return [selectedRelease ? scenarioDependencyKey(selectedRelease, dependency) : dependency.releaseId, candidates];
  }));
  const availableNodeActions = useMemo(() => {
    const explicit = executableActionTypes(selectedRelease?.actions);
    return [...new Set(explicit.length ? explicit : [selectedNode?.data.action ?? 'install'])];
  }, [selectedNode?.data.action, selectedRelease?.actions]);

  function addComponent(component: Component, release: ComponentRelease) {
    if (!editable) return;
    const declaredActions = executableActionTypes(release.actions);
    const defaultAction = declaredActions.includes('install') ? 'install' : declaredActions.includes('upgrade') ? 'upgrade' : declaredActions.includes('configure') ? 'configure' : declaredActions.includes('preflight') ? 'preflight' : declaredActions.includes('inspect') ? 'inspect' : declaredActions.includes('verify') ? 'verify' : declaredActions[0] ?? 'install';
    setNodes((items) => {
      const count = items.length;
      return [...items, {
        id: `node-${component.id}-${Date.now()}-${count}`,
        type: 'component',
        position: { x: 80 + (count % 3) * 260, y: 80 + Math.floor(count / 3) * 170 },
        data: { label: component.name, componentId: component.id, releaseId: release.id, version: release.version, action: defaultAction, hostGroup: inheritedHostGroup(release, defaultAction), parameterValues: {}, layer: component.layer },
      }];
    });
  }

  function connect(connection: Connection) {
    if (!editable || !connection.source || !connection.target) return;
    const source = nodes.find((node) => node.id === connection.source);
    const target = nodes.find((node) => node.id === connection.target);
    const targetRelease = target ? releasesByID.get(target.data.releaseId) : undefined;
    if (!source || !target || !targetRelease) return;
    const matchingDependency = scenarioDependenciesForNode(target, targetRelease).find((dependency) => dependency.releaseId === source.data.releaseId);
    if (matchingDependency && matchingDependency.kind !== 'configuration') {
      const dependencyId = scenarioDependencyKey(targetRelease, matchingDependency);
      if (target.data.dependencySources?.[dependencyId] === source.id) {
        notify('info', '依赖顺序已存在', '该 Release 依赖已经自动绑定到这个上游节点。');
        return;
      }
      chooseDependencySource(target.id, dependencyId, source.id);
      return;
    }
    if (isNodeReachable(source.id, target.id, edges)) {
      notify('info', '执行顺序已存在', '现有依赖或顺序路径已经保证该先后关系。');
      return;
    }
    if (source.id === target.id || isNodeReachable(target.id, source.id, edges)) {
      notify('error', '不能添加顺序线', '该连线会与现有依赖方向冲突或形成环。');
      return;
    }
    setEdges((items) => [...items, {
      id: `sequence:${source.id}:${target.id}:${Date.now()}`, source: source.id, target: target.id, kind: 'sequence',
    } as FlowEdge]);
  }

  function chooseDependencySource(targetID: string, dependencyID: string, sourceID: string) {
    if (!sourceID) {
      setNodes((items) => items.map((node) => {
        if (node.id !== targetID) return node;
        const dependencySources = { ...node.data.dependencySources };
        delete dependencySources[dependencyID];
        return { ...node, data: { ...node.data, dependencySources } };
      }));
      return;
    }
    const targetNode = nodes.find((node) => node.id === targetID);
    const targetRelease = targetNode ? releasesByID.get(targetNode.data.releaseId) : undefined;
    const reference = targetRelease?.dependencies?.find((dependency) => scenarioDependencyKey(targetRelease, dependency) === dependencyID);
    const configurationOnly = reference?.kind === 'configuration';
    const remaining = edges.filter((edge) => !(edge.kind === 'dependency' && edge.target === targetID && edge.dependencyId === dependencyID));
    if (!configurationOnly && isNodeReachable(targetID, sourceID, remaining)) {
      notify('error', '不能选择依赖来源', '该选择会与现有手工顺序线冲突或形成环。');
      return;
    }
    setNodes((items) => items.map((node) => node.id === targetID ? {
      ...node, data: { ...node.data, dependencySources: { ...node.data.dependencySources, [dependencyID]: sourceID } },
    } : node));
    notify('success', '引用来源已选择', configurationOnly ? '配置来源已锁定，执行顺序保持不变。' : '系统已生成只读依赖线。');
  }

  function handleEdgesChange(changes: EdgeChange<FlowEdge>[]) {
    const protectedIDs = new Set(edges.filter((edge) => edge.kind === 'dependency').map((edge) => edge.id));
    onEdgesChange(changes.filter((change) => change.type !== 'remove' || !protectedIDs.has(change.id)));
  }

  function updateSelected(patch: Partial<ScenarioNodeData>) {
    if (!selectedNodeId || !editable) return;
    setNodes((items) => items.map((node) => node.id === selectedNodeId ? { ...node, data: { ...node.data, ...patch } } : node));
  }

  const adaptationReleases = nodes.flatMap(node => { const release = releasesByID.get(node.data.releaseId); return release ? [{name: node.data.label,release}] : []; });
  const adaptationIssues = scenarioAdaptationIssues(constraints, adaptationReleases, true, adaptationLabel);
  const commonRanges = commonAdaptationRanges(adaptationReleases);
  function adaptationLabel(key:string,value?:string) { const category=platformOptionCategories.find(c=>c.key===key);return value ? category?.options.find(o=>o.value===value)?.label ?? value : category?.label ?? key; }

  function environmentIssues(environment: Environment) {
    const facts = environment.currentRevision?.facts ?? {};
    return [...environmentAdaptationIssues(selectedScenario?.name ?? '场景', constraints, facts, adaptationLabel), ...adaptationReleases.flatMap(({name,release}) => environmentAdaptationIssues(name, release.environmentConstraints ?? {}, facts, adaptationLabel))];
  }
  async function save() {
    if (!revision) return;
    const conflicts = scenarioAdaptationIssues(constraints, adaptationReleases, false, adaptationLabel);
    if (conflicts.length) { notify('error', '适配标签不匹配', conflicts.slice(0, 3).join('；') + (conflicts.length > 3 ? `；另有 ${conflicts.length - 3} 项，请查看适配标签区。` : '')); return; }
    const normalized = reconcileScenarioGraph(nodes, edges, releasesByID);
    const topologyIssues = normalized.issues.filter((issue) => issue.code === 'sequence_conflicts_dependency' || issue.code === 'sequence_redundant');
    if (topologyIssues.length) {
      notify('error', '顺序线不合法', topologyIssues.map((issue) => issue.message).join('；'));
      return;
    }
    const issues = scenarioParameterIssues(normalized.nodes as FlowNode[], releaseMetadata);
    if (issues.length) {
      setView('parameters');
      notify('error', '参数总览仍有待处理项', issues.join('；'));
      return;
    }
    setBusy('save');
    try {
      await api.saveGraph(revision.id, {
        nodes: normalized.nodes.map(({ id, position, data }) => ({ id, type: 'component' as const, position, data })),
        edges: normalized.edges,
        environmentConstraints: serializeConstraintSelection(constraints, environmentConstraintDimensions(platformOptionCategories)),
      });
      notify('success', '场景图已保存', '图或参数变更会使之前的测试结果失效。'); signalRefresh('scenarios');
    } catch (reason) { notify('error', '保存失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function validate() {
    if (!revision) return; setBusy('validate');
    try { const result = await api.validateScenario(revision.id); setValidation(result); notify(result.valid ? 'success' : 'error', result.valid ? 'DAG 校验通过' : 'DAG 校验未通过', result.errors?.join('；')); }
    catch (reason) { notify('error', '校验失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function test() {
    if (!revision || !testEnvironment || (revision.state !== 'released' && nodes.length === 0)) return; setBusy('test');
    setOperationExplanation(undefined);
    try {
      if (revision.state === 'released') await api.runScenario(revision.id, testEnvironment);
      else await api.testScenario(revision.id, testEnvironment);
      notify('success', revision.state === 'released' ? '场景运行已提交' : '完整场景测试已提交', revision.state === 'released' ? '可以在运行中心查看执行进度。' : '场景状态将进入 Testing。'); setTestOpen(false); signalRefresh(['scenarios', 'runs', 'environments', 'workbench']);
    }
    catch (reason) { setOperationExplanation(actionableExplanation(reason)); notify('error', '测试提交失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function previewPublish() {
    if (!revision) return; setBusy('publish-preview');
    setOperationExplanation(undefined);
    try { setCandidateSet(await api.candidateReleaseSet(revision.id)); }
    catch (reason) { setOperationExplanation(actionableExplanation(reason)); notify('error', '候选发布集检查失败', displayError(reason)); }
    finally { setBusy(undefined); }
  }

  async function publish() {
    if (!revision) return; setBusy('publish');
    setOperationExplanation(undefined);
    try { await api.publishScenario(revision.id); notify('success', '候选发布集已原子发布', `场景 Revision 与 ${candidateSet?.releases.length ?? 0} 个候选组件版本已一次提交。`); setCandidateSet(undefined); signalRefresh(['scenarios', 'components', 'workbench']); }
    catch (reason) { setOperationExplanation(actionableExplanation(reason)); notify('error', '发布失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  function importTemplate(text: string) {
    try {
      const parsed = parseScenarioTemplate(text);
      validateScenarioTemplateReferences(parsed, components ?? []);
      setConstraints(parseConstraintSelection(parsed.environmentConstraints, environmentConstraintDimensions(platformOptionCategories)));
      setNodes(parsed.nodes.map((node) => {
        const release = releaseMetadata.get(node.data.releaseId)?.release;
        return { ...node, data: { ...node.data, hostGroup: inheritedHostGroup(release, node.data.action) } };
      }));
      setEdges(parsed.edges as FlowEdge[]);
      setImportOpen(false);
      setValidation(undefined);
      notify('success', '场景模板已载入', `${parsed.nodes.length} 个节点 · ${parsed.edges.length} 条边；请检查后保存草稿。`);
    } catch (reason) { notify('error', '模板导入失败', displayError(reason)); }
  }

  function exportTemplate() {
    try {
      const normalized = reconcileScenarioGraph(nodes, edges, releasesByID);
      const content = serializeScenarioTemplate({
        nodes: normalized.nodes.map(({ id, position, data }) => ({ id, type: 'component', position, data })),
        edges: normalized.edges,
        environmentConstraints: serializeConstraintSelection(constraints, environmentConstraintDimensions(platformOptionCategories)),
      });
      const filenameBase = (selectedScenario?.slug || selectedScenario?.name || 'scenario').toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'scenario';
      const href = URL.createObjectURL(new Blob([`${content}\n`], { type: 'application/json;charset=utf-8' }));
      const anchor = document.createElement('a');
      anchor.href = href;
      anchor.download = `${filenameBase}-r${revision?.revision ?? 0}.json`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(href);
      notify('success', '场景 JSON 已导出', `${normalized.nodes.length} 个节点 · ${normalized.edges.length} 条边。`);
    } catch (reason) { notify('error', '导出 JSON 失败', displayError(reason)); }
  }

  async function cloneRevision() {
    if (!selectedScenario || !revision) return; setBusy('clone');
    try {
      const plan = await api.previewScenarioClone(selectedScenario.id, revision.id);
      if (!window.confirm(`复制预览\nRevision ${plan.sourceRevision} → Revision ${plan.nextRevision}\n${plan.nodeCount} 个节点 · ${plan.edgeCount} 条依赖\n\n确认创建当前 Draft？`)) return;
      const created = await api.cloneScenarioRevision(selectedScenario.id, revision.id, plan.planDigest);
      setSearchParams({ selected: selectedScenario.id, revision: created.id }, { replace: true });
      notify('success', '新 Revision 已创建', '已从当前不可变版本克隆为 Draft。');
      signalRefresh('scenarios');
    }
    catch (reason) { notify('error', '创建 Revision 失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function abandonDraft() {
    if (!revision || !window.confirm(`确认放弃 Revision ${revision.revision} 草稿？\n系统将恢复到最近的不可变 Revision，当前草稿会保留在历史记录中。`)) return; setBusy('abandon');
    try {
      const scenario = await api.abandonScenarioRevision(revision.id);
      const restoredRevisionId = scenario.currentRevision?.id;
      setSearchParams({
        selected: selectedScenario?.id ?? scenario.id,
        ...(restoredRevisionId ? { revision: restoredRevisionId } : {}),
      }, { replace: true });
      notify('success', '草稿已放弃', `已恢复到 Revision ${scenario.currentRevision?.revision ?? '—'}。`);
      signalRefresh('scenarios');
    } catch (reason) { notify('error', '放弃草稿失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function deprecateRevision() {
    if (!revision || !window.confirm(`确认废弃 Revision ${revision.revision}？`)) return; setBusy('deprecate');
    try { await api.deprecateScenario(revision.id); notify('success', '场景 Revision 已废弃'); signalRefresh('scenarios'); }
    catch (reason) { notify('error', '废弃失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function deleteScenario() {
    if (!selectedScenario) return;
    const revisionCount = selectedScenario.revisions?.length ?? 0;
    if (!window.confirm(`确认永久删除场景“${selectedScenario.name}”？\n将删除整个场景及 ${revisionCount} 个未发布 Revision。\n\n仅从未发布且从未产生 Run 的场景允许删除；此操作不可恢复。`)) return;
    setBusy('delete-scenario');
    setDeleteExplanation(undefined);
    try {
      await api.deleteScenario(selectedScenario.id);
      setSearchParams({}, { replace: true });
      notify('success', '场景已删除', `“${selectedScenario.name}”及其未发布 Revision 已永久删除。`);
      signalRefresh(['scenarios', 'workbench']);
    } catch (reason) {
      setDeleteExplanation(actionableExplanation(reason));
      notify('error', '删除场景失败', displayError(reason));
    } finally { setBusy(undefined); }
  }

  return <div className="page page--scenario">
    <PageHeader eyebrow="Scenario composer" title="场景编排" description="集群 Owner 将精确组件版本编译为可测试、可发布的集群搭建 DAG。" actions={user.role === 'scenario_owner' ? <button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建场景</button> : undefined} />
    <RefreshNotice loading={isRefreshing} error={scenarios ? error : undefined} onRetry={() => void reload()} />
    {loading && !scenarios ? <LoadingBlock label="正在加载场景图…" /> : error && !scenarios ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <>
      <div className="scenario-toolbar panel">
        <label><span>当前场景</span><select value={selectedScenario?.id ?? ''} onChange={(event) => setSearchParams({ selected: event.target.value })}>{scenarios?.map((scenario) => <option key={scenario.id} value={scenario.id}>{scenario.name}</option>)}</select></label>
				{revision && <label><span>Revision</span><select aria-label="Revision" value={revision.id} onChange={(event) => { if (selectedScenario) setSearchParams({ selected: selectedScenario.id, revision: event.target.value }); }}>{selectedScenario?.revisions?.map((item) => <option key={item.id} value={item.id}>r{item.revision} · {item.state === 'draft' ? '草稿' : item.state === 'testing' ? '测试中' : item.state === 'test_passed' ? '测试通过' : item.state === 'released' ? '已发布' : item.state === 'abandoned' ? '已放弃' : '已废弃'}{item.id === (selectedScenario.currentRevisionId ?? currentRevision?.id) ? ' · 当前' : ''}</option>)}</select></label>}
        {revision && <div className="scenario-revision"><span>{isCurrentRevision ? '当前 Revision' : '历史 Revision'}</span><StatusPill status={revision.state} /></div>}
        <div className="scenario-toolbar__actions">
          <button className="button button--quiet" disabled={!revision || busy === 'validate'} onClick={() => void validate()}><CheckCircle2 size={16} /> 校验</button>
          {editable && <button className="button button--quiet" onClick={() => setImportOpen(true)}><Upload size={16} /> 导入模板</button>}
          {revision && <button className="button button--quiet" onClick={exportTemplate}><Download size={16} /> 导出 JSON</button>}
          {editable && <button className="button button--secondary" disabled={busy === 'save'} onClick={() => void save()}><Save size={16} /> 保存草稿</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && (revision?.state === 'test_passed' || revision?.state === 'released' || revision?.state === 'deprecated') && !selectedScenario.revisions?.some((item) => item.id !== revision.id && ['draft', 'testing', 'test_passed'].includes(item.state)) && <button className="button button--secondary" disabled={busy === 'clone'} onClick={() => void cloneRevision()}><Plus size={16} /> 复制为新 Revision</button>}
          {canAbandonDraft && <button className="button button--danger-soft" disabled={busy === 'abandon'} onClick={() => void abandonDraft()}><Undo2 size={16} /> 放弃草稿</button>}
          {canRequestScenarioDelete && <button className="button button--danger-soft" disabled={busy === 'delete-scenario'} onClick={() => void deleteScenario()}><Trash2 size={16} /> 删除场景</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'released' && <button className="button button--danger-soft" disabled={busy === 'deprecate'} onClick={() => void deprecateRevision()}>废弃</button>}
          {canLaunchRevision && <button className="button button--secondary" onClick={() => setTestOpen(true)}><Beaker size={16} /> {revision?.state === 'released' ? '环境运行' : '环境测试'}</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && isCurrentRevision && revision?.state === 'test_passed' && <button className="button button--primary" disabled={busy === 'publish-preview'} onClick={() => void previewPublish()}><Rocket size={16} /> 预览候选集并发布</button>}
        </div>
      </div>
      <StatusExplanationPanel item={revisionWorkItem} />
      <StatusExplanationPanel explanation={deleteExplanation} title="场景删除被阻断" />
      <section className="panel adaptation-panel"><fieldset disabled={!editable}><EnvironmentConstraintEditor title="场景支持范围" value={constraints} onChange={setConstraints} /></fieldset>{Object.entries(commonRanges).filter(([key])=>!(constraints[key]?.length)).map(([key,values])=><p key={key}>{adaptationLabel(key)} · 组件共同支持：{values.map(v=>adaptationLabel(key,v)).join("、") || "无共同范围"}；请由场景 Owner 选择。</p>)}{adaptationIssues.length ? <div className="form-validation">{adaptationIssues.slice(0, 3).map(issue => <p key={issue}>{issue}</p>)}{adaptationIssues.length > 3 ? <details><summary>查看其余 {adaptationIssues.length - 3} 项匹配问题</summary>{adaptationIssues.slice(3).map(issue => <p key={issue}>{issue}</p>)}</details> : null}</div> : null}</section>
        {selectedScenario ? <div className="scenario-editor">
        <aside className="scenario-palette panel">
          <header><h3>组件版本</h3><p>{editable ? '点击加入画布' : '当前为只读视图'}</p></header>
          <div className="palette-list">{COMPONENT_LAYERS.map((layer) => {
            const available = components?.filter((component) => component.layer === layer.value).flatMap((component) =>
              (component.releases ?? []).filter((release) => release.state === 'released' || release.candidate).map((release) => ({ component, release })),
            ) ?? [];
            return <section className="palette-layer" key={layer.value}><div className="palette-layer__header"><span>{layer.code}</span><strong>{layer.label}</strong></div>{available.length ? available.map(({ component, release }) => {
              const used = nodes.filter((node) => node.data.releaseId === release.id).length;
              const conflicts = scenarioAdaptationIssues(constraints, [...adaptationReleases, {name: component.name, release}], false, adaptationLabel);
              return <button key={release.id} disabled={!editable || conflicts.length > 0} title={conflicts.join('；')} onClick={() => addComponent(component, release)}><span><Boxes size={16} /></span><div><strong>{component.name}</strong><small>{release.lineName} · {release.version}{release.candidate ? ' · 候选' : ''}{used ? ` · 已使用 ${used} 次` : ''}</small></div><Plus size={15} /></button>;
            }) : <div className="palette-layer__empty">本层暂无已发布或候选组件</div>}</section>;
          })}</div>
          <div className="palette-hint"><GitCommitHorizontal size={17} /><p>Release 依赖线由系统自动维护；手工连线只增加本场景的执行顺序。</p></div>
        </aside>
        <section className="flow-canvas panel" aria-label="场景 DAG 画布">
          <div className="scenario-view-toggle"><button className={view === 'graph' ? 'active' : ''} onClick={() => setView('graph')}><Network size={14} /> DAG</button><button className={view === 'table' ? 'active' : ''} onClick={() => setView('table')}><Table2 size={14} /> 节点表</button><button className={view === 'parameters' ? 'active' : ''} onClick={() => setView('parameters')}><Settings2 size={14} /> 参数总览</button></div>
          {nodes.length && view === 'graph' ? <ReactFlow nodes={displayNodes} edges={displayEdges} nodeTypes={nodeTypes} onNodesChange={editable ? onNodesChange : undefined} onEdgesChange={editable ? handleEdgesChange : undefined} onConnect={connect} onNodeClick={(_, node) => setSelectedNodeId(node.id)} nodesDraggable={editable} nodesConnectable={editable} elementsSelectable fitView deleteKeyCode={editable ? ['Backspace', 'Delete'] : null}>
            <Background gap={22} size={1} color="#d8deeb" /><MiniMap pannable zoomable nodeColor="#6075e8" /><Controls showInteractive={false} />
          </ReactFlow> : nodes.length && view === 'table' ? <div className="scenario-node-table"><div><strong>节点</strong><strong>版本</strong><strong>动作</strong><strong>主机组</strong><strong>前置/后置</strong></div>{displayNodes.map((node) => <button key={node.id} className={selectedNodeId === node.id ? 'active' : ''} onClick={() => setSelectedNodeId(node.id)}><span>{node.data.label}</span><span>{node.data.version}</span><span>{node.data.action}</span><span>{node.data.hostGroup}</span><span>{displayEdges.filter((edge) => edge.target === node.id).length} / {displayEdges.filter((edge) => edge.source === node.id).length}</span></button>)}</div> : nodes.length && view === 'parameters' ? <ScenarioParameterOverviewPanel nodes={displayNodes} releases={releaseMetadata} editable={editable} onChange={(nodeId, parameterValues) => setNodes((items) => items.map((node) => node.id === nodeId ? { ...node, data: { ...node.data, parameterValues } } : node))} onLocate={(nodeId) => { setSelectedNodeId(nodeId); setView('graph'); }} /> : <EmptyState title="场景画布为空" description={editable ? '从左侧添加已发布或候选组件版本。' : '这个场景还没有组件节点。'} />}
          {nodes.length && view === 'graph' ? <div className="scenario-edge-legend"><span><i className="dependency" /><LockKeyhole size={12} /> 自动依赖</span><span><i className="sequence" /> 手工顺序</span></div> : null}
          {dependencyIssues.length ? <div className="scenario-dependency-issues" role="status"><strong>{dependencyIssues.length} 项依赖待处理</strong>{dependencyIssues.map((issue) => <span key={`${issue.nodeId}-${issue.dependencyId}`}>{displayNodes.find((node) => node.id === issue.nodeId)?.data.label ?? issue.nodeId}：{issue.message}</span>)}</div> : null}
          {validation && <div className={`validation-result${validation.valid ? ' validation-result--ok' : ''}`}><strong>{validation.valid ? '校验通过' : `${validation.errors.length} 个问题`}</strong>{validation.errors.map((item) => <span key={item}>{item}</span>)}</div>}
        </section>
        <aside className="node-inspector panel">
          <header><Settings2 size={17} /><div><h3>节点配置</h3><p>参数与目标主机组</p></div></header>
          {selectedNode ? <div className="inspector-form"><label><span>显示名称</span><input value={selectedNode.data.label} disabled={!editable} onChange={(event) => updateSelected({ label: event.target.value })} /></label><label><span>组件分层</span><input value={selectedNodeComponent ? `${componentLayer(selectedNodeComponent.layer).code} · ${componentLayer(selectedNodeComponent.layer).label}` : '—'} disabled /></label><label><span>精确版本</span><input value={selectedNode.data.version ?? ''} disabled /></label><label><span>生命周期动作</span><select value={selectedNode.data.action ?? availableNodeActions[0]} disabled={!editable} onChange={(event) => { const action = event.target.value as ScenarioNodeData['action']; updateSelected({ action, hostGroup: inheritedHostGroup(selectedRelease, action) }); }}>{availableNodeActions.map((action) => <option key={action} value={action}>{action}</option>)}</select></label><label><span>主机组（继承组件 Action）</span><input value={selectedNode.data.hostGroup ?? inheritedHostGroup(selectedRelease, selectedNode.data.action)} disabled /></label>{nodeDependencies.length ? <div className="source-picker"><strong>版本依赖与配置来源</strong>{nodeDependencies.map((dependency) => {
            const dependencyId = selectedRelease ? scenarioDependencyKey(selectedRelease, dependency) : dependency.releaseId;
            const options = sourceOptionsByDependency[dependencyId] ?? [];
            const selectedSource = selectedNode.data.dependencySources?.[dependencyId];
            return <div key={dependencyId} className="source-picker__item">
              <p className="parameter-lineage">{dependency.componentName ?? dependency.componentId} · {dependency.version ?? dependency.releaseId}{dependency.purpose ? ` · ${dependency.purpose}` : ''}</p>
              {(dependency.parameterMappings ?? []).map((mapping) => <p key={`${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">{describeParameterMapping(dependency, mapping, components ?? [])}</p>)}
              {options.length <= 1
                ? <label><span>来源节点</span><input value={options[0] ? `${options[0].data.label} · ${options[0].data.version} · ${options[0].data.hostGroup} · 自动绑定` : '缺少匹配的上游节点'} disabled /></label>
                : <label><span>来源节点</span><select required value={selectedSource ?? ''} disabled={!editable} onChange={(event) => chooseDependencySource(selectedNode.id, dependencyId, event.target.value)}><option value="">请选择来源节点</option>{options.map((option) => <option key={option.id} value={option.id}>{option.data.label} · {option.data.version} · {option.data.hostGroup}</option>)}</select></label>}
            </div>;
          })}</div> : null}{mappedTargets.size ? <p className="palette-hint">已由上游映射的参数不可再配置：{[...mappedTargets].join(', ')}</p> : null}<ScenarioParameterFields parameters={scenarioOwnedParameters(selectedRelease)} values={selectedNode.data.parameterValues ?? {}} editable={editable} onChange={(parameterValues) => updateSelected({ parameterValues })} />{editable && <button className="button button--danger-soft" onClick={() => { setNodes((items) => items.filter((node) => node.id !== selectedNode.id)); setEdges((items) => items.filter((edge) => edge.source !== selectedNode.id && edge.target !== selectedNode.id)); setSelectedNodeId(undefined); }}><Trash2 size={15} /> 删除节点</button>}</div> : <EmptyState title="选择一个节点" description="查看版本、动作和节点参数。" />}
        </aside>
      </div> : <div className="panel"><EmptyState title="暂无场景" description="请先由集群 Owner 创建一个场景。" /></div>}
    </>}
    {testOpen && revision && <Modal title={revision.state === 'released' ? '运行已发布场景' : '场景完整测试'} description="运行将锁定当前场景、组件、Environment Revision、参数来源和 Playbook 摘要；不接受临时 Run 参数。" onClose={() => setTestOpen(false)}><div className="modal-body"><label><span>共享测试环境</span><select value={testEnvironment} onChange={(event) => setTestEnvironment(event.target.value)}><option value="">请选择</option>{environments?.map((environment: Environment) => <option key={environment.id} value={environment.id} disabled={environmentIssues(environment).length > 0}>{environment.name} · {environmentIssues(environment).length ? "不匹配" : environment.status || "ready"}</option>)}</select></label>{environments?.filter(environment => environmentIssues(environment).length > 0).map(environment => <details key={environment.id} className="environment-mismatch"><summary>{environment.name}：不匹配（{environmentIssues(environment).length} 项）</summary>{environmentIssues(environment).map((issue, index) => <p key={index}>{issue}</p>)}</details>)}{revision.state !== 'released' && nodes.length === 0 ? <div className="form-validation" role="alert">当前 DAG 为空；请先添加组件节点并保存草稿。</div> : null}<StatusExplanationPanel explanation={operationExplanation} title="场景运行被阻断" /></div><footer className="modal-actions"><button className="button button--quiet" onClick={() => { setTestOpen(false); setOperationExplanation(undefined); }}>取消</button><button className="button button--primary" disabled={!testEnvironment || environments?.some(environment => environment.id === testEnvironment && environmentIssues(environment).length > 0) || busy === 'test' || (revision.state !== 'released' && nodes.length === 0)} onClick={() => void test()}><Beaker size={16} /> {revision.state === 'released' ? '开始运行' : '开始完整测试'}</button></footer></Modal>}
    {createOpen && <CreateScenarioModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('scenarios'); }} />}
    {importOpen && <ScenarioTemplateModal onClose={() => setImportOpen(false)} onImport={importTemplate} />}
    {candidateSet && <Modal title="候选发布集" description="以下 Draft 与场景 Revision 将在同一事务中发布；任一项变化都会整体失败。" onClose={() => setCandidateSet(undefined)}><div className="modal-body candidate-release-set">{candidateSet.releases.length ? candidateSet.releases.map((item) => <div key={item.releaseId}><strong>{item.componentName}</strong><span>{item.version}</span></div>) : <p>本场景只引用已发布组件；本次仅发布场景 Revision。</p>}{candidateSet.issues.map((issue) => <div className="inline-warning" key={`${issue.nodeId}-${issue.code}`}><span>{issue.nodeId ? `${issue.nodeId}：` : ''}{issue.message}</span></div>)}<StatusExplanationPanel explanation={operationExplanation} title="场景发布被阻断" /></div><footer className="modal-actions"><button className="button button--quiet" onClick={() => { setCandidateSet(undefined); setOperationExplanation(undefined); }}>取消</button><button className="button button--primary" disabled={!candidateSet.ready || busy === 'publish'} onClick={() => void publish()}><Rocket size={16} /> {busy === 'publish' ? '原子发布中…' : '确认原子发布'}</button></footer></Modal>}
  </div>;
}

function scenarioOwnedParameters(release?: ComponentRelease) {
  return (release?.parameters ?? []).filter((parameter) => parameter.modifiable && parameter.valueProvider === 'scenario_owner');
}

function parameterValueError(parameter: ParameterDefinition, value: unknown): string | undefined {
  if (value === undefined || value === null || value === '') return parameter.required ? `${parameter.name} 为必填项` : undefined;
  const validType = parameter.type === 'string' ? typeof value === 'string'
    : parameter.type === 'boolean' ? typeof value === 'boolean'
      : parameter.type === 'integer' ? typeof value === 'number' && Number.isInteger(value)
        : parameter.type === 'number' ? typeof value === 'number' && Number.isFinite(value)
          : parameter.type === 'array' ? Array.isArray(value)
            : typeof value === 'object' && !Array.isArray(value);
  if (!validType) return `${parameter.name} 必须是 ${parameter.type}`;
  if (parameter.minLength && typeof value === 'string' && [...value].length < parameter.minLength) return `${parameter.name} 长度不能小于 ${parameter.minLength}`;
  if (parameter.enum?.length && !parameter.enum.some((item) => JSON.stringify(item) === JSON.stringify(value))) return `${parameter.name} 不在允许选项中`;
  return undefined;
}

function scenarioParameterIssues(nodes: FlowNode[], releases: Map<string, { component: Component; release: ComponentRelease }>) {
  return nodes.flatMap((node) => {
    const parameters = scenarioOwnedParameters(releases.get(node.data.releaseId)?.release);
    const allowed = new Set(parameters.map((parameter) => parameter.name));
    const values = node.data.parameterValues ?? {};
    return [
      ...parameters.flatMap((parameter) => {
        const issue = parameterValueError(parameter, values[parameter.name]);
        return issue ? [`${node.data.label || node.id}：${issue}`] : [];
      }),
      ...Object.keys(values).filter((key) => !allowed.has(key)).map((key) => `${node.data.label || node.id}：失效字段 ${key} 必须清理`),
    ];
  });
}

function ScenarioParameterFields({ parameters, values, editable, onChange }: { parameters: ParameterDefinition[]; values: Record<string, unknown>; editable: boolean; onChange: (values: Record<string, unknown>) => void }) {
  const allowed = new Set(parameters.map((parameter) => parameter.name));
  const staleKeys = Object.keys(values).filter((key) => !allowed.has(key));
  function setValue(name: string, value: unknown) {
    const next = { ...values };
    if (value === undefined) delete next[name]; else next[name] = value;
    onChange(next);
  }
  if (!parameters.length && !staleKeys.length) return <EmptyState title="该节点没有集群 Owner 参数" description="组件固定值、环境字段和上游映射不会进入填写区。" />;
  return <div className="scenario-parameter-fields">
    {parameters.map((parameter) => {
      const issue = parameterValueError(parameter, values[parameter.name]);
      const present = values[parameter.name] !== undefined;
      return <label key={parameter.name} className={issue ? 'field-invalid' : ''}><span>{parameter.name}{parameter.required ? '（必填）' : '（可选）'}</span><small>{parameter.description} · {parameter.type}</small><ParameterValueEditor parameter={parameter} value={values[parameter.name]} disabled={!editable} optional={!parameter.required} onChange={(value) => setValue(parameter.name, value)} />{editable && !present && parameter.suggestedValue !== undefined ? <button type="button" className="icon-text" onClick={() => setValue(parameter.name, parameter.suggestedValue)}>采用建议值 {String(parameter.suggestedValue)}</button> : null}{issue ? <em>{issue}</em> : null}</label>;
    })}
    {staleKeys.map((key) => <div className="inline-warning" key={key}><span>失效字段 {key} 不再属于当前 Release 契约。</span>{editable ? <button type="button" className="button button--quiet button--small" onClick={() => setValue(key, undefined)}>确认清理</button> : null}</div>)}
  </div>;
}

function ScenarioParameterOverviewPanel({ nodes, releases, editable, onChange, onLocate }: { nodes: FlowNode[]; releases: Map<string, { component: Component; release: ComponentRelease }>; editable: boolean; onChange: (nodeId: string, values: Record<string, unknown>) => void; onLocate: (nodeId: string) => void }) {
  const groups = new Map<string, { component: Component; release: ComponentRelease; nodes: FlowNode[] }>();
  for (const node of nodes) {
    const metadata = releases.get(node.data.releaseId);
    if (!metadata) continue;
    const parameters = scenarioOwnedParameters(metadata.release);
    if (!parameters.length && !Object.keys(node.data.parameterValues ?? {}).length) continue;
    const group = groups.get(metadata.release.id) ?? { ...metadata, nodes: [] };
    group.nodes.push(node);
    groups.set(metadata.release.id, group);
  }
  const ordered = [...groups.values()].sort((left, right) => `${left.component.name}:${left.release.version}`.localeCompare(`${right.component.name}:${right.release.version}`));
  if (!ordered.length) return <EmptyState title="没有集群 Owner 参数" description="当前 DAG 的参数全部由组件、环境或上游映射提供。" />;
  return <div className="scenario-parameter-overview">
    {ordered.map((group) => <section key={group.release.id} className="parameter-overview-release"><header><div><strong>{group.component.name}</strong><span>{group.release.version} · {group.release.id}</span></div></header>{group.nodes.map((node) => {
      const parameters = scenarioOwnedParameters(group.release);
      const values = node.data.parameterValues ?? {};
      const required = parameters.filter((parameter) => parameter.required);
      const completed = required.filter((parameter) => !parameterValueError(parameter, values[parameter.name])).length;
      return <article key={node.id}><div className="parameter-overview-node"><div><strong>{node.data.label}</strong><small>{node.data.action} · {node.data.hostGroup}</small></div><span>{completed}/{required.length} 已完成</span><button type="button" className="icon-text" onClick={() => onLocate(node.id)}>定位节点</button></div><ScenarioParameterFields parameters={parameters} values={values} editable={editable} onChange={(next) => onChange(node.id, next)} /></article>;
    })}</section>)}
  </div>;
}

function ScenarioTemplateModal({ onClose, onImport }: { onClose: () => void; onImport: (text: string) => void }) {
  const [text, setText] = useState('{\n  "nodes": [],\n  "edges": []\n}');
  return <Modal size="wide" title="导入场景模板" description="一次导入节点和边；载入后仍需人工检查并保存。" onClose={onClose}><div className="modal-body"><textarea aria-label="场景模板 JSON" className="code-editor" rows={18} value={text} onChange={(event) => setText(event.target.value)} spellCheck={false} /></div><footer className="modal-actions"><button className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" onClick={() => onImport(text)}><Upload size={16} /> 载入草稿</button></footer></Modal>;
}

function CreateScenarioModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try { await api.createScenario({ name: String(form.get('name')), slug: String(form.get('slug')), description: String(form.get('description')) }); notify('success', '场景已创建', '初始 Draft 已准备好，可以加入组件节点。'); onDone(); }
    catch (reason) { notify('error', '创建场景失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建场景" description="场景会创建一个可编辑的初始 Draft Revision。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>场景名称</span><input name="name" required placeholder="例如 Kylin Kubernetes 集群" /></label><label><span>标识</span><input name="slug" required pattern="[a-z0-9-]+" placeholder="kylin-k8s-cluster" /></label><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建场景'}</button></footer></form></Modal>;
}
