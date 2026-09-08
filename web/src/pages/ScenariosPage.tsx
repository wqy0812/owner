import { Tabs } from 'antd';
import { ActionMenu } from '../components/ActionMenu';
import { useDialogs } from '../components/UIProvider';
import { Field } from '../components/Field';
import { Button, Select, Input } from 'antd';
import { activeWorkbench } from '../hooks/activeWork';
import { HostGroupName, useHostGroupLabel } from '../components/HostGroupName';
import { scenarioGraphSignature } from '../types/scenarioGraphContent';
import { ScenarioAcceptanceEditor } from '../components/ScenarioAcceptanceEditor';
import { ScenarioExecutionPanel, ScenarioEvidence } from '../components/ScenarioExecutionPanel';
import { ScenarioCreateModal } from '../components/ScenarioCreateModal';
import { replaceScenarioNodeRelease, changedReleaseParameterIssues, changedReleaseDependencyIssues, scenarioParameterError } from '../features/scenarios/model';
import type { ScenarioClonePlan } from '../types/domain';
import { JobPlanPreview } from '../components/JobPlanPreview';
import type { ComponentTestPlan } from '../types/domain';
import { BranchScope } from '../components/BranchScope';
import { parseConstraintSelection, environmentConstraintDimensions, serializeConstraintSelection, type ConstraintSelection } from '../types/environmentConstraints';
import { adaptationValues, scenarioAdaptationIssues, environmentAdaptationIssues, commonAdaptationRanges } from '../types/adaptation';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Background, Controls, Handle, MiniMap, Position, ReactFlow, useEdgesState, useNodesState, type Connection, type Edge, type EdgeChange, type Node, type NodeProps, type ReactFlowInstance, } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Beaker, Boxes, CheckCircle2, Download, GitCommitHorizontal, Inbox, LockKeyhole, Network, Plus, Rocket, Save, Settings2, Table2, Trash2, Undo2, Upload } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { describeParameterMapping, mappedParameterNames, ParameterValueEditor } from '../components/ParameterEditors';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { COMPONENT_LAYERS, componentLayer } from '../types/componentClassification';
import { type CandidateReleaseSet, type Component, type ComponentRelease, type Environment, type ParameterDefinition, type ScenarioEdge, type ScenarioNodeData, type WorkExplanation } from '../types/domain';
import { scenarioExecutionOrderNodes, isScenarioNodeReachable, reconcileScenarioGraph, scenarioDependenciesForNode, scenarioDependencyKey } from './scenarioGraph';
import { parseScenarioTemplate, serializeScenarioTemplate, validateScenarioTemplateReferences } from './scenarioTemplate';
type FlowNode = Node<ScenarioNodeData>;
type FlowEdge = Edge & ScenarioEdge;
export function isNodeReachable(source: string, target: string, edges: Array<Pick<Edge, 'source' | 'target'>>) {
    return isScenarioNodeReachable(source, target, edges);
}
function ComponentNode({ data, selected }: NodeProps<FlowNode>) {
    return <div className={`flow-node${selected ? ' selected' : ''}`}>
    <Handle type="target" position={Position.Left}/>
    <div className="flow-node__top"><span><Boxes size={15}/></span><small>{data.layer ? componentLayer(data.layer).code : 'COMPONENT'}</small></div>
    <strong>{data.label}</strong>
    <div className="flow-node__meta"><span>{data.version ?? '—'}</span><span>{data.action ?? 'install'}</span></div>
    <Handle type="source" position={Position.Right}/>
  </div>;
}
const nodeTypes = { component: ComponentNode };
function inheritedHostGroup(release: ComponentRelease | undefined, action: ScenarioNodeData['action']): string {
    const definition = release?.actions?.find((item) => item.type === action)
        ?? (action === 'upgrade' ? release?.actions?.find((item) => item.type === 'install' && item.idempotent) : undefined);
    return definition?.hostGroup ?? '';
}
export function ScenariosPage() {
    const [paletteCollapsed, setPaletteCollapsed] = useState(false);
    const [inspectorCollapsed, setInspectorCollapsed] = useState(false);
    const { confirm } = useDialogs();
    const hostGroupLabel = useHostGroupLabel();
    const { user, notify, signalRefresh, platformOptionCategories } = useApp();
    const [searchParams, setSearchParams] = useSearchParams();
    const { data: scenarios, loading, error, isRefreshing, reload } = useApiData((signal) => api.scenarios(signal), [user.id], 'scenarios');
    const { data: components, loading: componentsLoading, error: componentsError, reload: reloadComponents } = useApiData((signal) => api.components(signal), [user.id], 'components');
    const { data: environments } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
    const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench', activeWorkbench);
    const selectedScenarioId = searchParams.get('selected') ?? '';
    const selectedScenario = useMemo(() => scenarios?.find((item) => item.id === selectedScenarioId) ?? scenarios?.[0], [scenarios, selectedScenarioId]);
    const currentRevision = selectedScenario?.currentRevision;
    const selectedRevisionId = searchParams.get('revision') ?? undefined;
    const loadedRevisionRef = useRef<{
        scenarioId?: string;
        currentRevisionId?: string;
    }>({});
    const revision = useMemo(() => selectedScenario?.revisions?.find((item) => item.id === selectedRevisionId) ?? currentRevision, [currentRevision, selectedRevisionId, selectedScenario?.revisions]);
    const revisionWorkItem = workbench?.items.find((item) => item.subject.type === 'scenario_revision' && item.subject.id === revision?.id);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const settingsRef = useRef<HTMLDetailsElement>(null);
    useEffect(() => setSettingsOpen(false), [revision?.id]);
    const [constraints, setConstraints] = useState<ConstraintSelection>({});
    const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>([]);
    const [edges, setEdges, onEdgesChange] = useEdgesState<FlowEdge>([]);
    const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<FlowNode, FlowEdge>>();
    const [locateNodeId, setLocateNodeId] = useState<string>();
    const [selectedNodeId, setSelectedNodeId] = useState<string>();
    const [validation, setValidation] = useState<{
        valid: boolean;
        errors: string[];
    }>();
    const [testOpen, setTestOpen] = useState(false);
    const [createOpen, setCreateOpen] = useState(false);
    const [forkSource, setForkSource] = useState('');
    const [section, setSection] = useState<'target' | 'upgrade' | 'acceptance'>('target');
    const [clonePlan, setClonePlan] = useState<ScenarioClonePlan>();
    const [replacementIssues, setReplacementIssues] = useState<string[]>([]);
    const [importOpen, setImportOpen] = useState(false);
    const [candidateSet, setCandidateSet] = useState<CandidateReleaseSet>();
    const [operationExplanation, setOperationExplanation] = useState<WorkExplanation>();
    const [deleteExplanation, setDeleteExplanation] = useState<WorkExplanation>();
    const [view, setView] = useState<'graph' | 'table' | 'parameters'>('graph');
    const [testEnvironment, setTestEnvironment] = useState('');
    const [busy, setBusy] = useState<string>();
    const [savedGraphSignature, setSavedGraphSignature] = useState('');
    const [loadedGraphDigest, setLoadedGraphDigest] = useState('');
    const [graphReloadToken, setGraphReloadToken] = useState(0);
    const graphRevisionRef = useRef<string>();
    const editorVisit = useRef({ key: '', generation: 0 });
    const editorKey = `${user.id}:${revision?.id ?? ''}:${graphReloadToken}`;
    if (editorVisit.current.key !== editorKey)
        editorVisit.current = { key: editorKey, generation: editorVisit.current.generation + 1 };
    const graphSignature = useMemo(() => scenarioGraphSignature({ nodes, edges, environmentConstraints: constraints }), [nodes, edges, constraints]);
    const currentEditor = useRef({ generation: editorVisit.current.generation, signature: graphSignature });
    currentEditor.current = { generation: editorVisit.current.generation, signature: graphSignature };
    const graphDirty = Boolean(savedGraphSignature && savedGraphSignature !== graphSignature);
    const isCurrentRevision = Boolean(revision && revision.id === (selectedScenario?.currentRevisionId ?? currentRevision?.id));
    const contractsUnavailable = components === undefined || Boolean(componentsError) || (revision?.nodes ?? []).some(node => !(components ?? []).some(component => component.releases?.some(release => release.id === node.data.releaseId)));
    const editable = !contractsUnavailable && Boolean(user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && isCurrentRevision && revision?.state === 'draft');
    const canLaunch = Boolean((user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id) || user.role === 'environment_owner');
    const canLaunchRevision = Boolean(!contractsUnavailable && canLaunch && revision && (isCurrentRevision || revision.state === 'released'));
    const canAbandonDraft = Boolean(editable && selectedScenario?.revisions?.some((item) => item.id !== revision?.id && (item.state === 'released' || item.state === 'deprecated')));
    const canRequestScenarioDelete = Boolean(selectedScenario?.ownerId === user.id
        && selectedScenario.revisions?.length
        && selectedScenario.revisions.every((item) => item.state === 'draft'));
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
        if (!action || !selectedScenario || !revision)
            return;
        if (action === 'test' && canLaunchRevision)
            setTestOpen(true);
        if (action === 'publish' && revision.state === 'test_passed' && selectedScenario.ownerId === user.id)
            void previewPublish();
        if (action === 'inspect')
            setView('table');
        const next = new URLSearchParams(searchParams);
        next.delete('action');
        setSearchParams(next, { replace: true });
    }, [canLaunchRevision, revision, searchParams, selectedScenario, setSearchParams, user.id]);
    useEffect(() => {
        if (graphRevisionRef.current === revision?.id && graphDirty)
            return;
        graphRevisionRef.current = revision?.id;
        setLoadedGraphDigest(revision?.revisionDigest ?? '');
        const nextConstraints = Object.fromEntries(Object.entries(revision?.environmentConstraints ?? {}).map(([key, value]) => [key, adaptationValues(value)]));
        setConstraints(nextConstraints);
        setSavedGraphSignature(scenarioGraphSignature({ nodes: revision?.nodes ?? [], edges: revision?.edges ?? [], environmentConstraints: nextConstraints }));
        setNodes((revision?.nodes ?? []).map((node) => ({ ...node, type: 'component' })) as FlowNode[]);
        setEdges((revision?.edges ?? []) as FlowEdge[]);
        setSelectedNodeId(undefined);
        setReplacementIssues([]);
        setValidation(undefined);
    }, [revision?.id, revision?.revisionDigest, graphReloadToken, setEdges, setNodes]);
    useEffect(() => setDeleteExplanation(undefined), [selectedScenario?.id]);
    const releaseMetadata = useMemo(() => new Map(components?.flatMap((component) => (component.releases ?? []).map((release) => [release.id, { component, release }] as const)) ?? []), [components]);
    const releasesByID = useMemo(() => new Map([...releaseMetadata].map(([releaseID, item]) => [releaseID, item.release])), [releaseMetadata]);
    const reconciledGraph = useMemo(() => reconcileScenarioGraph(nodes, edges, releasesByID), [edges, nodes, releasesByID]);
    useEffect(() => {
        if (!editable || releasesByID.size === 0)
            return;
        const sourceSignature = (items: FlowNode[]) => JSON.stringify(items.map((node) => [node.id, node.data.dependencySources ?? {}]));
        const edgeSignature = (items: Array<Pick<ScenarioEdge, 'id' | 'source' | 'target' | 'kind' | 'dependencyId'>>) => JSON.stringify(items.map((edge) => [edge.id, edge.source, edge.target, edge.kind, edge.dependencyId]));
        if (sourceSignature(nodes) !== sourceSignature(reconciledGraph.nodes as FlowNode[]))
            setNodes(reconciledGraph.nodes as FlowNode[]);
        if (edgeSignature(edges) !== edgeSignature(reconciledGraph.edges))
            setEdges(reconciledGraph.edges as FlowEdge[]);
    }, [editable, edges, nodes, reconciledGraph, releasesByID.size, setEdges, setNodes]);
    const orderNodes = useMemo(() => reconciledGraph.issues.some(issue => issue.code === 'contract_unavailable') ? [] : scenarioExecutionOrderNodes(reconciledGraph.nodes, reconciledGraph.edges), [reconciledGraph]);
    const orderBlocked = contractsUnavailable || orderNodes.length > 0;
    useEffect(() => {
        if (view !== 'graph') {
            setFlowInstance(undefined);
            return;
        }
        if (!flowInstance || !locateNodeId)
            return;
        void flowInstance.fitView({ nodes: [{ id: locateNodeId }], maxZoom: 1.2, duration: 250 });
        setLocateNodeId(undefined);
    }, [view, flowInstance, locateNodeId]);
    const displayNodes = useMemo(() => nodes.map((node) => {
        const metadata = releaseMetadata.get(node.data.releaseId);
        return {
            ...node,
            className: orderNodes.includes(node.id) ? 'scenario-node--order-pending' : undefined,
            selected: node.id === selectedNodeId,
            data: {
                ...node.data,
                componentId: node.data.componentId || metadata?.component.id || '',
                label: node.data.label || metadata?.component.name || node.id,
                version: node.data.version ?? metadata?.release.version,
                layer: node.data.layer ?? metadata?.component.layer,
            },
        };
    }), [nodes, releaseMetadata, orderNodes, selectedNodeId]);
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
    const availableNodeActions: Array<ScenarioNodeData['action']> = ['install'];
    function addComponent(component: Component, release: ComponentRelease) {
        if (!editable)
            return;
        const defaultAction = 'install' as const;
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
        if (!editable || !connection.source || !connection.target)
            return;
        const source = nodes.find((node) => node.id === connection.source);
        const target = nodes.find((node) => node.id === connection.target);
        const targetRelease = target ? releasesByID.get(target.data.releaseId) : undefined;
        if (!source || !target || !targetRelease)
            return;
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
                if (node.id !== targetID)
                    return node;
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
        if (!selectedNodeId || !editable)
            return;
        setNodes((items) => items.map((node) => node.id === selectedNodeId ? { ...node, data: { ...node.data, ...patch } } : node));
    }
    const adaptationReleases = nodes.flatMap(node => { const release = releasesByID.get(node.data.releaseId); return release ? [{ name: node.data.label, release }] : []; });
    const adaptationIssues = scenarioAdaptationIssues(constraints, adaptationReleases, true, adaptationLabel);
    const commonRanges = commonAdaptationRanges(adaptationReleases);
    function adaptationLabel(key: string, value?: string) { const category = platformOptionCategories.find(c => c.key === key); return value ? category?.options.find(o => o.value === value)?.label ?? value : category?.label ?? key; }
    function environmentIssues(environment: Environment) {
        const facts = environment.currentRevision?.facts ?? {};
        return [...environmentAdaptationIssues(selectedScenario?.name ?? '场景', constraints, facts, adaptationLabel), ...adaptationReleases.flatMap(({ name, release }) => environmentAdaptationIssues(name, release.environmentConstraints ?? {}, facts, adaptationLabel))];
    }
    async function save() {
        if (!revision)
            return;
        const submittedEditor = { ...currentEditor.current };
        const conflicts = scenarioAdaptationIssues(constraints, adaptationReleases, false, adaptationLabel);
        if (conflicts.length) {
            notify('error', '适配标签不匹配', conflicts.slice(0, 3).join('；') + (conflicts.length > 3 ? `；另有 ${conflicts.length - 3} 项，请查看适配标签区。` : ''));
            return;
        }
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
            const saved = await api.saveGraph(revision.id, {
                nodes: normalized.nodes.map(({ id, position, data }) => ({ id, type: 'component' as const, position, data })),
                edges: normalized.edges,
                environmentConstraints: constraints,
                expectedDigest: loadedGraphDigest || undefined,
            });
            if (currentEditor.current.generation === submittedEditor.generation) {
                setLoadedGraphDigest(saved.revisionDigest ?? '');
                setSavedGraphSignature(scenarioGraphSignature(saved));
                if (currentEditor.current.signature === submittedEditor.signature) {
                    setNodes(saved.nodes as FlowNode[]);
                    setEdges(saved.edges as FlowEdge[]);
                    setConstraints(Object.fromEntries(Object.entries(saved.environmentConstraints ?? {}).map(([key, value]) => [key, adaptationValues(value)])));
                }
            }
            notify('success', '场景图已保存', '图或参数变更会使之前的测试结果失效。');
            signalRefresh('scenarios');
        }
        catch (reason) {
            notify('error', '保存失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function validate() {
        if (!revision)
            return;
        setBusy('validate');
        try {
            const result = await api.validateScenario(revision.id);
            setValidation(result);
            notify(result.valid ? 'success' : 'error', result.valid ? 'DAG 校验通过' : 'DAG 校验未通过', result.errors?.join('；'));
        }
        catch (reason) {
            notify('error', '校验失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    const [jobPlan, setJobPlan] = useState<ComponentTestPlan>();
    const [jobMedia, setJobMedia] = useState<Record<string, string>>({});
    const [exportReady, setExportReady] = useState(false);
    const jobChoices = Object.entries(jobMedia).map(([requirementId, mode]) => ({ requirementId, mode }));
    useEffect(() => { setJobPlan(undefined); setJobMedia({}); setExportReady(false); }, [testEnvironment, revision?.id]);
    async function previewJob() {
        if (!revision || !testEnvironment)
            return;
        setBusy('job-preview');
        try {
            const plan = await api.previewScenarioJob(revision.id, testEnvironment, jobChoices);
            setJobPlan(plan);
            setExportReady(plan.deliveryRequirements.every(item => !!jobMedia[item.id]));
        }
        catch (reason) {
            notify('error', '计划预览失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function exportJob() {
        if (!revision || !jobPlan)
            return;
        setBusy('job-export');
        try {
            const blob = await api.exportScenarioJob(revision.id, testEnvironment, jobPlan.planDigest, jobChoices);
            const href = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = href;
            link.download = `scenario-job-${revision.id}.tar.gz`;
            link.click();
            URL.revokeObjectURL(href);
            notify('success', '作业包已导出', '下载不会创建或启动 Run。');
        }
        catch (reason) {
            notify('error', '导出失败', displayError(reason));
            setJobPlan(undefined);
        }
        finally {
            setBusy(undefined);
        }
    }
    async function previewPublish() {
        if (orderBlocked || !revision)
            return;
        setBusy('publish-preview');
        setOperationExplanation(undefined);
        try {
            setCandidateSet(await api.candidateReleaseSet(revision.id));
        }
        catch (reason) {
            setOperationExplanation(actionableExplanation(reason));
            notify('error', '候选发布集检查失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function publish() {
        if (orderBlocked || !revision)
            return;
        setBusy('publish');
        setOperationExplanation(undefined);
        try {
            await api.publishScenario(revision.id);
            notify('success', '候选发布集已原子发布', `场景版本与 ${candidateSet?.releases.length ?? 0} 个候选组件版本已一次提交。`);
            setCandidateSet(undefined);
            signalRefresh(['scenarios', 'components', 'workbench']);
        }
        catch (reason) {
            setOperationExplanation(actionableExplanation(reason));
            notify('error', '发布失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    function importTemplate(text: string) {
        try {
            const parsed = parseScenarioTemplate(text);
            validateScenarioTemplateReferences(parsed, components ?? []);
            const importedScope = parseConstraintSelection(parsed.environmentConstraints, environmentConstraintDimensions(platformOptionCategories));
            const normalize = (scope: ConstraintSelection) => JSON.stringify(Object.entries(scope).filter(([, values]) => values.length).sort(([a], [b]) => a.localeCompare(b)).map(([key, values]) => [key, [...values].sort()]));
            if (normalize(importedScope) !== normalize(constraints))
                throw new Error('模板适配范围与当前分支不同，请新增分支后导入。');
            setNodes(parsed.nodes.map((node) => {
                const release = releaseMetadata.get(node.data.releaseId)?.release;
                return { ...node, data: { ...node.data, hostGroup: inheritedHostGroup(release, node.data.action) } };
            }));
            setEdges(parsed.edges as FlowEdge[]);
            setImportOpen(false);
            setValidation(undefined);
            notify('success', '场景模板已载入', `${parsed.nodes.length} 个节点 · ${parsed.edges.length} 条边；请检查后保存草稿。`);
        }
        catch (reason) {
            notify('error', '模板导入失败', displayError(reason));
        }
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
        }
        catch (reason) {
            notify('error', '导出 JSON 失败', displayError(reason));
        }
    }
    async function cloneRevision() {
        if (!selectedScenario || !revision)
            return;
        setBusy('clone');
        try {
            setClonePlan(await api.previewScenarioClone(selectedScenario.id, revision.id));
        }
        catch (reason) {
            notify('error', '新增版本预览失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function createVersion() {
        if (!selectedScenario || !clonePlan)
            return;
        setBusy('clone');
        try {
            const created = await api.cloneScenarioRevision(selectedScenario.id, clonePlan.sourceRevisionId, clonePlan.planDigest, clonePlan.sourceRunId);
            setSearchParams({ selected: selectedScenario.id, revision: created.id }, { replace: true });
            setClonePlan(undefined);
            setSection('target');
            notify('success', '新版本已创建', '来源版本和成功正式 Run 已锁定；请编辑目标集群并完成安装、升级测试。');
            signalRefresh('scenarios');
        }
        catch (reason) {
            notify('error', '新增版本失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function reopenRevision() {
        if (!revision?.revisionDigest)
            return;
        setBusy('edit');
        try {
            await api.reopenScenarioRevision(revision.id, revision.revisionDigest);
            notify('success', '版本已恢复编辑', '安装和升级测试证据已失效，需要重新测试。');
            signalRefresh('scenarios');
        }
        catch (reason) {
            notify('error', '继续编辑失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function replaceRelease(releaseId: string) {
        const target = releasesByID.get(releaseId);
        if (!selectedNode || !target || !editable)
            return;
        try {
            const dependencyChanges = changedReleaseDependencyIssues(releasesByID.get(selectedNode.data.releaseId), target);
            if (dependencyChanges.length && !await confirm(`更换为 ${target.version} 将改变依赖或公开参数映射：\n${dependencyChanges.join('\n')}\n\n确认按目标版本合同重新匹配依赖来源？缺少上游或存在多个来源时，需要继续补齐。`))
                return;
            const data = replaceScenarioNodeRelease(selectedNode, target);
            setReplacementIssues([...changedReleaseParameterIssues(selectedNode, target), ...dependencyChanges]);
            updateSelected(data);
            setValidation(undefined);
            notify('info', '目标组件版本已更换', '原参数已保留；依赖按确认后的目标合同匹配，请处理失效项并保存目标集群。');
        }
        catch (reason) {
            notify('error', '更换组件版本失败', displayError(reason));
        }
    }
    async function abandonDraft() {
        if (!revision || !await confirm(`确认放弃版本 ${revision.revision} 草稿？\n系统将恢复到最近的不可变版本，当前草稿会保留在历史记录中。`))
            return;
        setBusy('abandon');
        try {
            const scenario = await api.abandonScenarioRevision(revision.id);
            const restoredRevisionId = scenario.currentRevision?.id;
            setSearchParams({
                selected: selectedScenario?.id ?? scenario.id,
                ...(restoredRevisionId ? { revision: restoredRevisionId } : {}),
            }, { replace: true });
            notify('success', '草稿已放弃', `已恢复到版本 ${scenario.currentRevision?.revision ?? '—'}。`);
            signalRefresh('scenarios');
        }
        catch (reason) {
            notify('error', '放弃草稿失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function deprecateRevision() {
        if (!revision || !await confirm(`确认废弃版本 ${revision.revision}？`))
            return;
        setBusy('deprecate');
        try {
            await api.deprecateScenario(revision.id);
            notify('success', '场景版本已废弃');
            signalRefresh('scenarios');
        }
        catch (reason) {
            notify('error', '废弃失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    async function deleteScenario() {
        if (!selectedScenario)
            return;
        const revisionCount = selectedScenario.revisions?.length ?? 0;
        if (!await confirm(`确认永久删除场景“${selectedScenario.name}”？\n将删除整个场景及 ${revisionCount} 个未发布版本。\n\n仅从未发布且从未产生 Run 的场景允许删除；此操作不可恢复。`))
            return;
        setBusy('delete-scenario');
        setDeleteExplanation(undefined);
        try {
            await api.deleteScenario(selectedScenario.id);
            setSearchParams({}, { replace: true });
            notify('success', '场景已删除', `“${selectedScenario.name}”及其未发布版本已永久删除。`);
            signalRefresh(['scenarios', 'workbench']);
        }
        catch (reason) {
            setDeleteExplanation(actionableExplanation(reason));
            notify('error', '删除场景失败', displayError(reason));
        }
        finally {
            setBusy(undefined);
        }
    }
    return <div className="page page--scenario">
    <PageHeader eyebrow="Scenario composer" title="场景编排" description="集群 Owner 将精确组件版本编译为可测试、可发布的集群搭建 DAG。" actions={user.role === 'scenario_owner' && Boolean(scenarios?.length) ? <Button className="button button--primary" onClick={() => { setForkSource(''); setCreateOpen(true); }} htmlType={"button"} type="primary"><Plus size={16}/> 新建场景</Button> : undefined}/>
    <RefreshNotice loading={isRefreshing} error={scenarios ? error : undefined} onRetry={() => void reload()}/>
    {(loading && !scenarios) || (componentsLoading && components === undefined && !error) ? <LoadingBlock label="正在加载场景工作区…"/> : error && !scenarios ? <ErrorBlock message={error} onRetry={() => void reload()}/> : !scenarios?.length ? user.role === 'scenario_owner' ? <button type="button" className="panel scenario-start scenario-start--action" aria-labelledby="scenario-start-title" aria-describedby="scenario-start-description" onClick={() => { setForkSource(''); setCreateOpen(true); }}>
      <span className="empty-state">
        <span className="empty-state__icon"><Inbox size={24}/></span>
        <strong id="scenario-start-title">开始编排第一个场景</strong>
        <span id="scenario-start-description" className="scenario-start__description">创建场景后再选择适配范围、添加组件和配置验收。</span>
      </span>
    </button> : <section className="panel scenario-start"><EmptyState title="开始编排第一个场景" description="创建场景后再选择适配范围、添加组件和配置验收。"/></section> : <>
      {contractsUnavailable && <section className="panel scenario-contract-status" aria-label="组件合同待就绪"><h3>{componentsError ? '组件合同加载失败' : '组件合同暂不可用'}</h3><p>已保存的连线和参数保持完整；合同齐备前暂停拓扑与参数编辑、测试和发布。</p><div className="scenario-contract-status__items">{(revision?.nodes ?? []).filter(node => !(components ?? []).some(component => component.releases?.some(release => release.id === node.data.releaseId))).map(node => <p key={node.id}>{node.data.label} · {node.data.contractAvailability === 'unshared' ? '组件尚未共享' : node.data.contractAvailability === 'missing' ? '组件版本不存在' : '未返回此版本合同'} · 负责人：{node.data.componentOwnerName || '待核对'}</p>)}</div><Button className="button button--quiet" onClick={() => { void reloadComponents(); void reload(); }} htmlType={"button"} type="default">重新检查</Button></section>}
      <div className="scenario-toolbar panel">
        <Field layout="horizontal" label={"当前场景"}><Select value={selectedScenario?.id ?? ''} onChange={(selectedValue) => setSearchParams({ selected: selectedValue })} popupMatchSelectWidth={true}>{scenarios?.map((scenario) => <Select.Option key={scenario.id} value={scenario.id}>{scenario.name}</Select.Option>)}</Select></Field>
				{revision && <Field layout="horizontal" label={"版本"}><Select aria-label="版本" value={revision.id} onChange={(selectedValue) => {
                    if (selectedScenario)
                        setSearchParams({ selected: selectedScenario.id, revision: selectedValue });
                }} popupMatchSelectWidth={true}>{selectedScenario?.revisions?.map((item) => <Select.Option key={item.id} value={item.id}>r{item.revision} · {item.state === 'draft' ? '草稿' : item.state === 'testing' ? '测试中' : item.state === 'test_passed' ? '测试通过' : item.state === 'released' ? '已发布' : item.state === 'abandoned' ? '已放弃' : '已废弃'}{item.id === (selectedScenario.currentRevisionId ?? currentRevision?.id) ? ' · 当前' : ''}</Select.Option>)}</Select></Field>}
        {revision && <div className="scenario-revision"><span>{isCurrentRevision ? '当前版本' : '历史版本'}</span><StatusPill status={revision.state}/></div>}
        {revision && <BranchScope scope={revision.environmentConstraints}/>}
        <div className="scenario-toolbar__actions">
          <Button className="button button--quiet" disabled={!revision || busy === 'validate'} onClick={() => void validate()} htmlType={"button"} type="default"><CheckCircle2 size={16}/> 校验</Button>


          {editable && <Button className="button button--secondary" disabled={busy === 'save'} onClick={() => void save()} htmlType={"button"} type="default"><Save size={16}/> 保存草稿</Button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'released' && !selectedScenario.revisions?.some((item) => item.id !== revision.id && ['draft', 'testing', 'test_passed'].includes(item.state)) && <Button className="button button--secondary" disabled={busy === 'clone'} onClick={() => void cloneRevision()} htmlType={"button"} type="default"><Plus size={16}/> 新增版本</Button>}
          {user.role === 'scenario_owner' && revision?.state === 'released' && <Button className="button button--secondary" onClick={() => { setForkSource(revision.id); setCreateOpen(true); }} htmlType={"button"} type="default"><Plus size={16}/> 新增分支</Button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && isCurrentRevision && revision?.state === 'test_passed' && <Button className="button button--secondary" disabled={busy === 'edit'} onClick={() => void reopenRevision()} htmlType={"button"} type="default">继续编辑</Button>}


          <ActionMenu items={[(editable) && {key:'action-0',label:<><Upload size={16}/>导入模板</>,onClick:() => setImportOpen(true)},(revision) && {key:'action-1',label:<><Download size={16}/>导出 JSON</>,onClick:exportTemplate},(canAbandonDraft) && {key:'action-2',label:<><Undo2 size={16}/>放弃草稿</>,onClick:() => void abandonDraft(),disabled:busy === 'abandon',danger:true},(canRequestScenarioDelete) && {key:'action-3',label:<><Trash2 size={16}/>删除场景</>,onClick:() => void deleteScenario(),disabled:busy === 'delete-scenario',danger:true},(user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'released') && {key:'action-4',label:<>废弃</>,onClick:() => void deprecateRevision(),disabled:busy === 'deprecate',danger:true}]} />
          {canLaunchRevision && <Button className="button button--secondary" disabled={orderBlocked || graphDirty} onClick={() => setTestOpen(true)} htmlType={"button"} type="default"><Beaker size={16}/> {revision?.state === 'released' ? '环境运行' : '环境测试'}</Button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && isCurrentRevision && revision?.state === 'test_passed' && <Button className="button button--primary" disabled={orderBlocked || graphDirty || busy === 'publish-preview'} onClick={() => void previewPublish()} htmlType={"button"} type="primary"><Rocket size={16}/> 预览候选集并发布</Button>}
        </div>
      </div>
      {selectedScenario?.forkedFromScenarioId && <p className="scenario-origin panel">分支来源：<a href={`/scenarios?selected=${encodeURIComponent(selectedScenario.forkedFromScenarioId)}&revision=${encodeURIComponent(selectedScenario.forkedFromRevisionId ?? '')}`}>查看来源场景及已发布版本</a> · 当前场景独立维护</p>}
      {revision && <><ScenarioEvidence revision={revision}/><Tabs className="scenario-section-tabs" aria-label="场景版本工作区" activeKey={section} onChange={key => setSection(key as typeof section)} items={[{key:'target',label:'目标集群'},{key:'upgrade',label:'升级作业'},{key:'acceptance',label:`业务验收${revision.acceptanceJobs?.length ? ` · ${revision.acceptanceJobs.length}` : ' · 待录入'}`}]} /></>}
      {graphDirty && <p className="inline-warning" role="status">目标集群有未保存的修改；请先保存草稿，再编辑业务验收或预览执行计划。</p>}
      {graphDirty && loadedGraphDigest && revision?.revisionDigest !== loadedGraphDigest && <div className="inline-warning" role="alert"><span>当前版本已被其他操作更新，保存会被阻止，请重新载入后合并修改。</span><Button className="button button--quiet" onClick={async () => {
                    if (await confirm('重新载入会放弃当前未保存的目标集群修改，确认重新载入？')) {
                        setSavedGraphSignature(graphSignature);
                        setGraphReloadToken(token => token + 1);
                    }
                }} htmlType={"button"} type="default">重新载入目标集群</Button></div>}
      <StatusExplanationPanel item={revisionWorkItem}/>
      {!contractsUnavailable && orderNodes.length > 0 && <div className="scenario-order-warning" role="alert">
        <strong>执行顺序尚未确定，请补充手工顺序线</strong>
        <p>以下节点均可作为下一步，需由场景 Owner 决定先后。顺序明确前，不能完整测试、发布或发起新运行；草稿仍可保存。</p>
        {revision?.state === 'released' && <p>请由场景 Owner 新增版本，补线并重新测试发布。</p>}
        <div>{orderNodes.map(id => {
                    const node = displayNodes.find(item => item.id === id)!;
                    return <Button className="button button--quiet" key={id} onClick={() => { setSelectedNodeId(id); setView('graph'); setLocateNodeId(id); }} htmlType={"button"} type="default">
            {node.data.label} · {node.data.version ?? '—'} · {node.data.action} · {hostGroupLabel(node.data.hostGroup)} · {id}
          </Button>;
                })}</div>
      </div>}
      <StatusExplanationPanel explanation={deleteExplanation} title="场景删除被阻断"/>
      {revision && <div hidden={section !== 'acceptance'}><ScenarioAcceptanceEditor key={revision.id} revisionId={revision.id} editable={editable && !graphDirty} nodes={revision.nodes} components={components ?? []} environments={environments ?? []} onSaved={() => signalRefresh('scenarios')}/></div>}
      {revision && <div hidden={section !== 'upgrade'}><div className="panel scenario-upgrade-panel">{revision.sourceRevisionId ? <ScenarioExecutionPanel revision={revision} environments={environments ?? []} editable={editable} canLaunch={canLaunchRevision && !graphDirty} blockedReason={contractsUnavailable ? '请先等待组件共享并加载完整合同。' : graphDirty ? '请先保存目标集群修改。' : undefined} initialMode="upgrade" environmentIssues={environmentIssues} onSaved={() => signalRefresh('scenarios')}/> : <EmptyState title="这是场景首版" description="完成完整安装测试及业务验收后即可发布。从本场景已发布且正式运行成功的版本新增版本时，将生成升级作业。"/>}</div></div>}
      <div hidden={section !== 'target'}>
        {selectedScenario ? <><div className="scenario-workspace-tools"><Button onClick={() => setPaletteCollapsed(value => !value)} aria-expanded={!paletteCollapsed}>{paletteCollapsed ? '展开组件库' : '收起组件库'}</Button><Button onClick={() => setInspectorCollapsed(value => !value)} aria-expanded={!inspectorCollapsed}>{inspectorCollapsed ? '展开节点配置' : '收起节点配置'}</Button></div><div className={`scenario-editor${paletteCollapsed ? ' scenario-editor--palette-collapsed' : ''}${inspectorCollapsed ? ' scenario-editor--inspector-collapsed' : ''}`}>
        <aside className="scenario-palette panel" hidden={paletteCollapsed}>
          <header><h3>组件版本</h3><p>{editable ? '点击加入画布' : '当前为只读视图'}</p></header>
          <div className="palette-list">{COMPONENT_LAYERS.map((layer) => {
                    const available = components?.filter((component) => component.layer === layer.value).flatMap((component) => (component.releases ?? []).filter((release) => release.state === 'released' || release.candidate).map((release) => ({ component, release }))) ?? [];
                    return <section className="palette-layer" key={layer.value}><div className="palette-layer__header"><span>{layer.code}</span><strong>{layer.label}</strong></div>{available.length ? available.map(({ component, release }) => {
                            const used = nodes.filter((node) => node.data.releaseId === release.id).length;
                            const conflicts = scenarioAdaptationIssues(constraints, [...adaptationReleases, { name: component.name, release }], false, adaptationLabel);
                            return <button key={release.id} disabled={!editable || conflicts.length > 0} title={conflicts.join('；')} onClick={() => addComponent(component, release)}><span><Boxes size={16}/></span><div><strong>{component.name}</strong><small>{release.lineName} · {release.version}{release.candidate ? ' · 候选' : ''}{used ? ` · 已使用 ${used} 次` : ''}</small></div><Plus size={15}/></button>;
                        }) : <div className="palette-layer__empty">本层暂无已发布或候选组件</div>}</section>;
                })}</div>
          <div className="palette-hint"><GitCommitHorizontal size={17}/><p>Release 依赖线由系统自动维护；手工连线只增加本场景的执行顺序。</p></div>
        </aside>
        <section className="flow-canvas panel" aria-label="场景 DAG 画布">
          <div className="scenario-view-toggle"><button className={view === 'graph' ? 'active' : ''} onClick={() => setView('graph')}><Network size={14}/> DAG</button><button className={view === 'table' ? 'active' : ''} onClick={() => setView('table')}><Table2 size={14}/> 节点表</button><button className={view === 'parameters' ? 'active' : ''} onClick={() => setView('parameters')}><Settings2 size={14}/> 参数总览</button></div>
          {nodes.length && view === 'graph' ? <ReactFlow onInit={setFlowInstance} nodes={displayNodes} edges={displayEdges} nodeTypes={nodeTypes} onNodesChange={editable ? onNodesChange : undefined} onEdgesChange={editable ? handleEdgesChange : undefined} onConnect={connect} onNodeClick={(_, node) => setSelectedNodeId(node.id)} nodesDraggable={editable} nodesConnectable={editable} elementsSelectable fitView deleteKeyCode={editable ? ['Backspace', 'Delete'] : null}>
            <Background gap={22} size={1} color="#d8deeb"/><MiniMap pannable zoomable nodeColor="#6075e8"/><Controls showInteractive={false}/>
          </ReactFlow> : nodes.length && view === 'table' ? <div className="scenario-node-table"><div><strong>节点</strong><strong>版本</strong><strong>动作</strong><strong>主机组</strong><strong>前置/后置</strong></div>{displayNodes.map((node) => <button key={node.id} className={selectedNodeId === node.id ? 'active' : ''} onClick={() => setSelectedNodeId(node.id)}><span>{node.data.label}</span><span>{node.data.version}</span><span>{node.data.action}</span><span>{<HostGroupName value={node.data.hostGroup}/>}</span><span>{displayEdges.filter((edge) => edge.target === node.id).length} / {displayEdges.filter((edge) => edge.source === node.id).length}</span></button>)}</div> : nodes.length && view === 'parameters' ? contractsUnavailable ? <EmptyState title="等待完整组件合同" description="已保存参数与来源绑定保持原样，共享完成后重新校验。"/> : <ScenarioParameterOverviewPanel nodes={displayNodes} releases={releaseMetadata} editable={editable} onChange={(nodeId, parameterValues) => setNodes((items) => items.map((node) => node.id === nodeId ? { ...node, data: { ...node.data, parameterValues } } : node))} onLocate={(nodeId) => { setSelectedNodeId(nodeId); setView('graph'); }}/> : <EmptyState title="场景画布为空" description={editable ? '从左侧添加已发布或候选组件版本。' : '这个场景还没有组件节点。'}/>}
          {nodes.length && view === 'graph' ? <div className="scenario-edge-legend"><span><i className="dependency"/><LockKeyhole size={12}/> 自动依赖</span><span><i className="sequence"/> 手工顺序</span></div> : null}
          {dependencyIssues.length ? <div className="scenario-dependency-issues" role="status"><strong>{dependencyIssues.length} 项依赖待处理</strong>{dependencyIssues.map((issue) => <span key={`${issue.nodeId}-${issue.dependencyId}`}>{displayNodes.find((node) => node.id === issue.nodeId)?.data.label ?? issue.nodeId}：{issue.message}</span>)}</div> : null}
          {validation && <div className={`validation-result${validation.valid ? ' validation-result--ok' : ''}`}><strong>{validation.valid ? '校验通过' : `${validation.errors.length} 个问题`}</strong>{validation.errors.map((item) => <span key={item}>{item}</span>)}</div>}
        </section>
        <aside className="node-inspector panel" hidden={inspectorCollapsed}>
          <section className="scenario-settings" aria-label="适配检查">
            <details ref={settingsRef} open={settingsOpen} onToggle={event => setSettingsOpen(event.currentTarget.open)}>
              <summary><strong>适配检查</strong><span>{adaptationIssues.length ? `${adaptationIssues.length} 项待处理` : '范围已固定'}</span></summary>
              <div className="scenario-settings__editor"><p>本分支适配范围已固定。调整组件以满足范围，或新增分支。</p>
              {Object.entries(commonRanges).filter(([key]) => !constraints[key]?.length).map(([key, values]) => <p key={key}>{adaptationLabel(key)} · 组件共同支持：{values.map(v => adaptationLabel(key, v)).join('、') || '无共同范围'}；请在新分支中确定所需范围。</p>)}
              {adaptationIssues.length > 0 && <div className="form-validation" id="scenario-adaptation-issues">{adaptationIssues.map(issue => <p key={issue}>{issue}</p>)}</div>}
              </div>
            </details>
            {!settingsOpen && <div className="scenario-settings__summary">{Object.entries(constraints).filter(([, values]) => values.length > 0).map(([key, values]) => <p key={key}><strong>{adaptationLabel(key)}</strong><span>{values.map(v => adaptationLabel(key, v)).join('、')}</span></p>)}{!Object.values(constraints).some(values => values.length) && <p>不限制适配范围</p>}</div>}
            {!settingsOpen && adaptationIssues.length > 0 && <button className="scenario-settings__issues" onClick={() => { setSettingsOpen(true); requestAnimationFrame(() => settingsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })); }}>{adaptationIssues[0]}{adaptationIssues.length > 1 ? `（共 ${adaptationIssues.length} 项）` : ''} · 展开处理</button>}
          </section>
          <header><Settings2 size={17}/><div><h3>节点配置</h3><p>参数与目标主机组</p></div></header>
          {selectedNode ? <div className="inspector-form"><Field label={"显示名称"}><Input value={selectedNode.data.label} disabled={!editable} onChange={(event) => updateSelected({ label: event.target.value })}/></Field><Field label={"组件分层"}><Input value={selectedNodeComponent ? `${componentLayer(selectedNodeComponent.layer).code} · ${componentLayer(selectedNodeComponent.layer).label}` : '—'} disabled/></Field><Field label={<>{editable ? '更换组件版本' : '精确版本'}</>}><Select aria-label="更换组件版本" value={selectedNode.data.releaseId} disabled={!editable} onChange={(selectedValue) => replaceRelease(selectedValue)} popupMatchSelectWidth={true}>{selectedNodeComponent?.releases?.filter(item => item.state === 'released' || item.candidate || item.id === selectedNode.data.releaseId).map(item => <Select.Option key={item.id} value={item.id}>{item.version} · {item.state === 'released' ? '已发布' : '候选'}</Select.Option>)}</Select></Field>{replacementIssues.map(issue => <p className="inline-warning" key={issue}>{issue}</p>)}<Field label={"目标集群动作"}><Select value={selectedNode.data.action ?? availableNodeActions[0]} disabled onChange={(selectedValue) => { const action = selectedValue as ScenarioNodeData['action']; updateSelected({ action, hostGroup: inheritedHostGroup(selectedRelease, action) }); }} popupMatchSelectWidth={true}>{availableNodeActions.map((action) => <Select.Option key={action} value={action}>{action}</Select.Option>)}</Select></Field><Field label={"主机组（继承组件 Action）"}><Input title={selectedNode.data.hostGroup} value={hostGroupLabel(selectedNode.data.hostGroup ?? inheritedHostGroup(selectedRelease, selectedNode.data.action))} disabled/></Field>{nodeDependencies.length ? <div className="source-picker"><strong>版本依赖与配置来源</strong>{nodeDependencies.map((dependency) => {
                            const dependencyId = selectedRelease ? scenarioDependencyKey(selectedRelease, dependency) : dependency.releaseId;
                            const options = sourceOptionsByDependency[dependencyId] ?? [];
                            const selectedSource = selectedNode.data.dependencySources?.[dependencyId];
                            return <div key={dependencyId} className="source-picker__item">
              <p className="parameter-lineage">{dependency.componentName ?? dependency.componentId} · {dependency.version ?? dependency.releaseId}{dependency.purpose ? ` · ${dependency.purpose}` : ''}</p>
              {(dependency.parameterMappings ?? []).map((mapping) => <p key={`${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">{describeParameterMapping(dependency, mapping, components ?? [])}</p>)}
              {options.length <= 1
                                    ? <Field label={"来源节点"}><Input value={options[0] ? `${options[0].data.label} · ${options[0].data.version} · ${hostGroupLabel(options[0].data.hostGroup)} · 自动绑定` : '缺少匹配的上游节点'} disabled/></Field> : <Field label={"来源节点"} required><Select value={selectedSource ?? ''} disabled={!editable} onChange={(selectedValue) => chooseDependencySource(selectedNode.id, dependencyId, selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">请选择来源节点</Select.Option>{options.map((option) => <Select.Option key={option.id} value={option.id}>{option.data.label} · {option.data.version} · {hostGroupLabel(option.data.hostGroup)}</Select.Option>)}</Select></Field>}
            </div>;
                        })}</div> : null}{mappedTargets.size ? <p className="palette-hint">已由上游映射的参数不可再配置：{[...mappedTargets].join(', ')}</p> : null}{contractsUnavailable ? <p className="workspace-references">等待完整组件合同。已保存的参数值和来源绑定保持原样，暂不判断字段是否失效。</p> : <ScenarioParameterFields parameters={scenarioOwnedParameters(selectedRelease)} values={selectedNode.data.parameterValues ?? {}} editable={editable} onChange={(parameterValues) => updateSelected({ parameterValues })}/>}{editable && <Button className="button button--danger-soft" onClick={() => { setNodes((items) => items.filter((node) => node.id !== selectedNode.id)); setEdges((items) => items.filter((edge) => edge.source !== selectedNode.id && edge.target !== selectedNode.id)); setSelectedNodeId(undefined); }} htmlType={"button"} type="default" danger><Trash2 size={15}/> 删除节点</Button>}</div> : <EmptyState title="选择一个节点" description="查看版本、动作和节点参数。"/>}
        </aside>
      </div></> : <div className="panel"><EmptyState title="暂无场景" description="请先由集群 Owner 创建一个场景。"/></div>}</div>
    </>}
    {testOpen && revision && <Modal busy={Boolean(busy)} size="wide" title={revision.state === 'released' ? '运行已发布场景' : '场景完整测试'} description="先预览锁定的组件、参数、环境基线与验收作业，再提交执行。" onClose={() => setTestOpen(false)}><div className="modal-body"><ScenarioExecutionPanel revision={revision} environments={environments ?? []} editable={editable} canLaunch={canLaunchRevision && !graphDirty} blockedReason={graphDirty ? '请先保存目标集群修改。' : undefined} environmentIssues={environmentIssues} onSubmitted={() => setTestOpen(false)} onSaved={() => signalRefresh('scenarios')}/><details className="scenario-job-export"><summary>独立安装作业包</summary><Field label={"导出目标环境"}><Select value={testEnvironment} onChange={(selectedValue) => setTestEnvironment(selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">请选择</Select.Option>{environments?.map(environment => <Select.Option key={environment.id} value={environment.id} disabled={environmentIssues(environment).length > 0}>{environment.name}</Select.Option>)}</Select></Field>{jobPlan && <><JobPlanPreview plan={jobPlan}/>{jobPlan.deliveryRequirements.map(item => <Field key={item.id} label={<>导出介质来源：{item.name}</>}><Select value={jobMedia[item.id] ?? ''} onChange={(selectedValue) => { setJobMedia(current => ({ ...current, [item.id]: selectedValue })); setExportReady(false); }} popupMatchSelectWidth={true}><Select.Option value="">请选择后重新预览</Select.Option><Select.Option value="direct">直接使用锁定来源</Select.Option>{item.transferAvailable && <Select.Option value="transfer">平移到环境目标并校验</Select.Option>}</Select></Field>)}</>}<div className="scenario-inline-actions"><Button className="button button--secondary" disabled={orderBlocked || !testEnvironment || !!busy} onClick={() => void previewJob()} htmlType={"button"} type="default">预览导出作业</Button><Button className="button button--quiet" disabled={!jobPlan || !exportReady || !!busy} onClick={() => void exportJob()} htmlType={"button"} type="default">导出独立作业包</Button></div></details></div></Modal>}
    {createOpen && <ScenarioCreateModal scenarios={scenarios ?? []} initialSourceRevisionId={forkSource} onClose={() => setCreateOpen(false)} onDone={scenario => { setCreateOpen(false); setSection('target'); setSearchParams({ selected: scenario.id }); signalRefresh('scenarios'); }}/>}
    {clonePlan && <Modal busy={Boolean(busy)} title="新增版本预览" description="仅从本场景已发布且正式运行、业务验收均成功的版本创建。" onClose={() => setClonePlan(undefined)} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={() => setClonePlan(undefined)} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={busy === 'clone'} onClick={() => void createVersion()} htmlType={"button"} type="primary">确认新增版本</Button></footer>}><div className="modal-body"><p>r{clonePlan.sourceRevision} → r{clonePlan.nextRevision} 草稿</p><BranchScope scope={revision?.environmentConstraints} full/><p>新版本沿用当前分支的适配范围。</p><p>{clonePlan.nodeCount} 个节点 · {clonePlan.edgeCount} 条依赖与编排顺序</p><p>锁定来源正式 Run：{clonePlan.sourceRunId}</p><p>新版本需分别通过安装测试、升级测试及完整业务验收。</p></div></Modal>}
    {importOpen && <ScenarioTemplateModal onClose={() => setImportOpen(false)} onImport={importTemplate}/>}
    {candidateSet && <Modal busy={Boolean(busy)} title="候选发布集" description="以下 Draft 与场景版本将在同一事务中发布；任一项变化都会整体失败。" onClose={() => setCandidateSet(undefined)} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={() => { setCandidateSet(undefined); setOperationExplanation(undefined); }} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={orderBlocked || !candidateSet.ready || busy === 'publish'} onClick={() => void publish()} htmlType={"button"} type="primary"><Rocket size={16}/> {busy === 'publish' ? '原子发布中…' : '确认原子发布'}</Button></footer>}><div className="modal-body candidate-release-set">{candidateSet.releases.length ? candidateSet.releases.map((item) => <div key={item.releaseId}><strong>{item.componentName}</strong><span>{item.version}</span></div>) : <p>本场景只引用已发布组件；本次仅发布场景版本。</p>}{candidateSet.issues.map((issue) => <div className="inline-warning" key={`${issue.nodeId}-${issue.code}`}><span>{issue.nodeId ? `${issue.nodeId}：` : ''}{issue.message}</span></div>)}<StatusExplanationPanel explanation={operationExplanation} title="场景发布被阻断"/></div></Modal>}
  </div>;
}
function scenarioOwnedParameters(release?: ComponentRelease) {
    return (release?.parameters ?? []).filter((parameter) => parameter.modifiable && parameter.valueProvider === 'scenario_owner');
}
const parameterValueError = scenarioParameterError;
function scenarioParameterIssues(nodes: FlowNode[], releases: Map<string, {
    component: Component;
    release: ComponentRelease;
}>) {
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
function ScenarioParameterFields({ parameters, values, editable, onChange }: {
    parameters: ParameterDefinition[];
    values: Record<string, unknown>;
    editable: boolean;
    onChange: (values: Record<string, unknown>) => void;
}) {
    const allowed = new Set(parameters.map((parameter) => parameter.name));
    const staleKeys = Object.keys(values).filter((key) => !allowed.has(key));
    function setValue(name: string, value: unknown) {
        const next = { ...values };
        if (value === undefined)
            delete next[name];
        else
            next[name] = value;
        onChange(next);
    }
    if (!parameters.length && !staleKeys.length)
        return <EmptyState title="该节点没有集群 Owner 参数" description="组件固定值、环境字段和上游映射不会进入填写区。"/>;
    return <div className="scenario-parameter-fields">
    {parameters.map((parameter) => {
            const issue = parameterValueError(parameter, values[parameter.name]);
            const present = values[parameter.name] !== undefined;
            return <label key={parameter.name} className={issue ? 'field-invalid' : ''}><span>{parameter.name}{parameter.required ? '（必填）' : '（可选）'}</span><small>{parameter.description} · {parameter.type}</small><ParameterValueEditor parameter={parameter} value={values[parameter.name]} disabled={!editable} optional={!parameter.required} onChange={(value) => setValue(parameter.name, value)}/>{editable && !present && parameter.suggestedValue !== undefined ? <Button className="icon-text" onClick={() => setValue(parameter.name, parameter.suggestedValue)} htmlType={"button"} type="text">采用建议值 {String(parameter.suggestedValue)}</Button> : null}{issue ? <em>{issue}</em> : null}</label>;
        })}
    {staleKeys.map((key) => <div className="inline-warning" key={key}><span>失效字段 {key} 不再属于当前 Release 契约。</span>{editable ? <Button className="button button--quiet button--small" onClick={() => setValue(key, undefined)} htmlType={"button"} type="default" size="small">确认清理</Button> : null}</div>)}
  </div>;
}
function ScenarioParameterOverviewPanel({ nodes, releases, editable, onChange, onLocate }: {
    nodes: FlowNode[];
    releases: Map<string, {
        component: Component;
        release: ComponentRelease;
    }>;
    editable: boolean;
    onChange: (nodeId: string, values: Record<string, unknown>) => void;
    onLocate: (nodeId: string) => void;
}) {
    const groups = new Map<string, {
        component: Component;
        release: ComponentRelease;
        nodes: FlowNode[];
    }>();
    for (const node of nodes) {
        const metadata = releases.get(node.data.releaseId);
        if (!metadata)
            continue;
        const parameters = scenarioOwnedParameters(metadata.release);
        if (!parameters.length && !Object.keys(node.data.parameterValues ?? {}).length)
            continue;
        const group = groups.get(metadata.release.id) ?? { ...metadata, nodes: [] };
        group.nodes.push(node);
        groups.set(metadata.release.id, group);
    }
    const ordered = [...groups.values()].sort((left, right) => `${left.component.name}:${left.release.version}`.localeCompare(`${right.component.name}:${right.release.version}`));
    if (!ordered.length)
        return <EmptyState title="没有集群 Owner 参数" description="当前 DAG 的参数全部由组件、环境或上游映射提供。"/>;
    return <div className="scenario-parameter-overview">
    {ordered.map((group) => <section key={group.release.id} className="parameter-overview-release"><header><div><strong>{group.component.name}</strong><span>{group.release.version} · {group.release.id}</span></div></header>{group.nodes.map((node) => {
                const parameters = scenarioOwnedParameters(group.release);
                const values = node.data.parameterValues ?? {};
                const required = parameters.filter((parameter) => parameter.required);
                const completed = required.filter((parameter) => !parameterValueError(parameter, values[parameter.name])).length;
                return <article key={node.id}><div className="parameter-overview-node"><div><strong>{node.data.label}</strong><small>{node.data.action} · {<HostGroupName value={node.data.hostGroup}/>}</small></div><span>{completed}/{required.length} 已完成</span><Button className="icon-text" onClick={() => onLocate(node.id)} htmlType={"button"} type="text">定位节点</Button></div><ScenarioParameterFields parameters={parameters} values={values} editable={editable} onChange={(next) => onChange(node.id, next)}/></article>;
            })}</section>)}
  </div>;
}
function ScenarioTemplateModal({ onClose, onImport }: {
    onClose: () => void;
    onImport: (text: string) => void;
}) {
    const [text, setText] = useState('{\n  "nodes": [],\n  "edges": []\n}');
    return <Modal size="wide" title="导入场景模板" description="一次导入节点和边；载入后仍需人工检查并保存。" onClose={onClose} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" onClick={() => onImport(text)} htmlType={"button"} type="primary"><Upload size={16}/> 载入草稿</Button></footer>}><div className="modal-body"><Input.TextArea aria-label="场景模板 JSON" className="code-editor" rows={18} value={text} onChange={(event) => setText(event.target.value)} spellCheck={false}/></div></Modal>;
}
