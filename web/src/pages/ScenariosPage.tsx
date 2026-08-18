import { useEffect, useMemo, useState, type FormEvent } from 'react';
import {
  Background,
  Controls,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  addEdge,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import { Beaker, Boxes, CheckCircle2, GitCommitHorizontal, Network, Plus, Rocket, Save, Settings2, Trash2 } from 'lucide-react';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill } from '../components/Primitives';
import { describeParameterMapping, mappedParameterNames } from '../components/ParameterEditors';
import { parseRunInput, RunInputFields, uniqueRunInputs } from '../components/RunInputFields';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { COMPONENT_CATEGORY_LABELS, COMPONENT_LAYERS, componentLayer } from '../types/componentClassification';
import type { Component, Environment, Scenario, ScenarioEdge, ScenarioNodeData } from '../types/domain';

type FlowNode = Node<ScenarioNodeData>;

export function isNodeReachable(source: string, target: string, edges: Array<Pick<Edge, 'source' | 'target'>>) {
  const forward = new Map<string, string[]>();
  for (const edge of edges) forward.set(edge.source, [...(forward.get(edge.source) ?? []), edge.target]);
  const queue = [source];
  const seen = new Set<string>();
  while (queue.length) {
    const current = queue.shift()!;
    if (current === target) return true;
    if (seen.has(current)) continue;
    seen.add(current);
    queue.push(...(forward.get(current) ?? []));
  }
  return false;
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

export function ScenariosPage() {
  const { user, notify, signalRefresh } = useApp();
  const { data: scenarios, loading, error, isRefreshing, reload } = useApiData((signal) => api.scenarios(signal), [user.id], 'scenarios');
  const { data: components } = useApiData((signal) => api.components(signal), [user.id], 'components');
  const { data: environments } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [selectedScenarioId, setSelectedScenarioId] = useState<string>();
  const selectedScenario = useMemo(() => scenarios?.find((item) => item.id === selectedScenarioId) ?? scenarios?.[0], [scenarios, selectedScenarioId]);
  const revision = selectedScenario?.currentRevision;
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  const [validation, setValidation] = useState<{ valid: boolean; errors: string[] }>();
  const [testOpen, setTestOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [testEnvironment, setTestEnvironment] = useState('');
  const [runInputValues, setRunInputValues] = useState<Record<string, string>>({});
	const [executionPolicy, setExecutionPolicy] = useState('{}');
  const [busy, setBusy] = useState<string>();

  const editable = Boolean(user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'draft');
  const canLaunch = Boolean((user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id) || user.role === 'environment_owner');

  useEffect(() => {
    setNodes((revision?.nodes ?? []).map((node) => ({ ...node, type: 'component' })) as FlowNode[]);
    setEdges((revision?.edges ?? []) as Edge[]);
    setSelectedNodeId(undefined);
    setValidation(undefined);
    setRunInputValues({});
		setExecutionPolicy(JSON.stringify(revision?.executionPolicy ?? {}, null, 2));
  }, [revision?.id, setEdges, setNodes]);

  const releaseMetadata = useMemo(() => new Map(
    components?.flatMap((component) => (component.releases ?? []).map((release) => [release.id, { component, release }] as const)) ?? [],
  ), [components]);
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

  const selectedNode = nodes.find((node) => node.id === selectedNodeId);
  const selectedNodeMetadata = selectedNode ? releaseMetadata.get(selectedNode.data.releaseId) : undefined;
  const selectedNodeComponent = selectedNodeMetadata?.component ?? components?.find((component) => component.id === selectedNode?.data.componentId);
  const selectedRelease = selectedNodeMetadata?.release;
  const mappedTargets = mappedParameterNames(selectedRelease);
  const mappingDependencies = (selectedRelease?.dependencies ?? []).filter((dependency) => (dependency.parameterMappings ?? []).length > 0);
  const sourceOptionsByDependency = Object.fromEntries(mappingDependencies.map((dependency) => {
    const candidates = displayNodes.filter((node) => node.data.releaseId === dependency.releaseId && selectedNodeId && isNodeReachable(node.id, selectedNodeId, edges));
    return [dependency.id ?? dependency.releaseId, candidates];
  }));
  const availableNodeActions = useMemo(() => {
    const explicit = selectedRelease?.actions?.map((action) => action.type).filter(Boolean) ?? [];
    return [...new Set(explicit.length ? explicit : [selectedNode?.data.action ?? 'install'])];
  }, [selectedNode?.data.action, selectedRelease?.actions]);
  const declaredRunInputs = useMemo(() => uniqueRunInputs([
    ...(revision?.runInputs ?? []),
    ...nodes.flatMap((node) => node.data.runInputs ?? []),
  ]), [nodes, revision?.runInputs]);

  function addComponent(component: Component) {
    if (!editable || !component.latestRelease) return;
    const declaredActions = component.latestRelease.actions?.map((action) => action.type) ?? [];
    const defaultAction = declaredActions.includes('install') ? 'install' : declaredActions.includes('upgrade') ? 'upgrade' : declaredActions.includes('configure') ? 'configure' : declaredActions.includes('preflight') ? 'preflight' : declaredActions.includes('inspect') ? 'inspect' : declaredActions.includes('verify') ? 'verify' : declaredActions[0] ?? 'install';
    setNodes((items) => {
      const count = items.length;
      return [...items, {
        id: `node-${component.id}-${Date.now()}-${count}`,
        type: 'component',
        position: { x: 80 + (count % 3) * 260, y: 80 + Math.floor(count / 3) * 170 },
        data: { label: component.name, componentId: component.id, releaseId: component.latestRelease!.id, version: component.latestRelease!.version, action: defaultAction, hostGroup: 'all', layer: component.layer },
      }];
    });
  }

  function connect(connection: Connection) {
    if (editable) setEdges((items) => addEdge({ ...connection, id: `edge-${connection.source}-${connection.target}` }, items));
  }

  function updateSelected(patch: Partial<ScenarioNodeData>) {
    if (!selectedNodeId || !editable) return;
    setNodes((items) => items.map((node) => node.id === selectedNodeId ? { ...node, data: { ...node.data, ...patch } } : node));
  }

  async function save() {
    if (!revision) return;
    setBusy('save');
    try {
      const policy = JSON.parse(executionPolicy || '{}') as Record<string, unknown>;
      await api.saveGraph(revision.id, { nodes: nodes.map(({ id, type, position, data }) => ({ id, type, position, data })), edges: edges.map(({ id, source, target, label }) => ({ id, source, target, label: typeof label === 'string' ? label : undefined })) as ScenarioEdge[], executionPolicy: policy });
      notify('success', '场景图已保存', '图或参数变更会使之前的测试结果失效。'); signalRefresh('scenarios');
    } catch (reason) { notify('error', '保存失败', reason instanceof SyntaxError ? '执行策略必须是有效 JSON。' : displayError(reason)); } finally { setBusy(undefined); }
  }

  async function validate() {
    if (!revision) return; setBusy('validate');
    try { const result = await api.validateScenario(revision.id); setValidation(result); notify(result.valid ? 'success' : 'error', result.valid ? 'DAG 校验通过' : 'DAG 校验未通过', result.errors?.join('；')); }
    catch (reason) { notify('error', '校验失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function test() {
    if (!revision || !testEnvironment) return; setBusy('test');
    try {
      const runInput = parseRunInput(declaredRunInputs, runInputValues);
      if (revision.state === 'released') await api.runScenario(revision.id, testEnvironment, runInput);
      else await api.testScenario(revision.id, testEnvironment, runInput);
      notify('success', revision.state === 'released' ? '场景运行已提交' : '完整场景测试已提交', revision.state === 'released' ? '可以在运行中心查看执行进度。' : '场景状态将进入 Testing。'); setTestOpen(false); signalRefresh(['scenarios', 'runs', 'environments']);
    }
    catch (reason) { notify('error', '测试提交失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function publish() {
    if (!revision) return; setBusy('publish');
    try { await api.publishScenario(revision.id); notify('success', '场景已发布', 'Revision 已锁定为不可变版本。'); signalRefresh('scenarios'); }
    catch (reason) { notify('error', '发布失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function cloneRevision() {
    if (!selectedScenario) return; setBusy('clone');
    try { await api.cloneScenarioRevision(selectedScenario.id); notify('success', '新 Revision 已创建', '已从当前不可变版本克隆为 Draft。'); signalRefresh('scenarios'); }
    catch (reason) { notify('error', '创建 Revision 失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  async function deprecateRevision() {
    if (!revision || !window.confirm(`确认废弃 Revision ${revision.revision}？`)) return; setBusy('deprecate');
    try { await api.deprecateScenario(revision.id); notify('success', '场景 Revision 已废弃'); signalRefresh('scenarios'); }
    catch (reason) { notify('error', '废弃失败', displayError(reason)); } finally { setBusy(undefined); }
  }

  return <div className="page page--scenario">
    <PageHeader eyebrow="Scenario composer" title="场景编排" description="场景 Owner 将精确组件版本编译为可测试、可发布的集群搭建 DAG。" actions={user.role === 'scenario_owner' ? <button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建场景</button> : undefined} />
    <RefreshNotice loading={isRefreshing} error={scenarios ? error : undefined} onRetry={() => void reload()} />
    {loading && !scenarios ? <LoadingBlock label="正在加载场景图…" /> : error && !scenarios ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <>
      <div className="scenario-toolbar panel">
        <label><span>当前场景</span><select value={selectedScenario?.id ?? ''} onChange={(event) => setSelectedScenarioId(event.target.value)}>{scenarios?.map((scenario) => <option key={scenario.id} value={scenario.id}>{scenario.name}</option>)}</select></label>
        {revision && <div className="scenario-revision"><span>Revision {revision.revision}</span><StatusPill status={revision.state} /></div>}
        <div className="scenario-toolbar__actions">
          <button className="button button--quiet" disabled={!revision || busy === 'validate'} onClick={() => void validate()}><CheckCircle2 size={16} /> 校验</button>
          {editable && <button className="button button--secondary" disabled={busy === 'save'} onClick={() => void save()}><Save size={16} /> 保存草稿</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && (revision?.state === 'released' || revision?.state === 'deprecated') && <button className="button button--secondary" disabled={busy === 'clone'} onClick={() => void cloneRevision()}><Plus size={16} /> 新 Revision</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'released' && <button className="button button--danger-soft" disabled={busy === 'deprecate'} onClick={() => void deprecateRevision()}>废弃</button>}
          {canLaunch && <button className="button button--secondary" disabled={!revision} onClick={() => setTestOpen(true)}><Beaker size={16} /> {revision?.state === 'released' ? '环境运行' : '环境测试'}</button>}
          {user.role === 'scenario_owner' && selectedScenario?.ownerId === user.id && revision?.state === 'test_passed' && <button className="button button--primary" disabled={busy === 'publish'} onClick={() => void publish()}><Rocket size={16} /> 发布</button>}
        </div>
      </div>
      {selectedScenario ? <div className="scenario-editor">
        <aside className="scenario-palette panel">
          <header><h3>组件版本</h3><p>{editable ? '点击加入画布' : '当前为只读视图'}</p></header>
          <div className="palette-list">{COMPONENT_LAYERS.map((layer) => {
            const available = components?.filter((component) => component.layer === layer.value && component.latestRelease?.state === 'released') ?? [];
            return <section className="palette-layer" key={layer.value}><div className="palette-layer__header"><span>{layer.code}</span><strong>{layer.label}</strong></div>{available.length ? available.map((component) => {
              const used = nodes.filter((node) => node.data.componentId === component.id).length;
              return <button key={component.id} disabled={!editable} onClick={() => addComponent(component)}><span><Boxes size={16} /></span><div><strong>{component.name}</strong><small>{COMPONENT_CATEGORY_LABELS[component.category]} · {component.latestRelease?.version}{used ? ` · 已使用 ${used} 次` : ''}</small></div><Plus size={15} /></button>;
            }) : <div className="palette-layer__empty">本层暂无已发布组件</div>}</section>;
          })}</div>
          <div className="palette-hint"><GitCommitHorizontal size={17} /><p>分层只用于分类提示；连线才表示硬依赖和实际执行顺序。</p></div>
        </aside>
        <section className="flow-canvas panel" aria-label="场景 DAG 画布">
          {nodes.length ? <ReactFlow nodes={displayNodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={editable ? onNodesChange : undefined} onEdgesChange={editable ? onEdgesChange : undefined} onConnect={connect} onNodeClick={(_, node) => setSelectedNodeId(node.id)} nodesDraggable={editable} nodesConnectable={editable} elementsSelectable fitView deleteKeyCode={editable ? ['Backspace', 'Delete'] : null}>
            <Background gap={22} size={1} color="#d8deeb" /><MiniMap pannable zoomable nodeColor="#6075e8" /><Controls showInteractive={false} />
          </ReactFlow> : <EmptyState title="场景画布为空" description={editable ? '从左侧添加已发布的组件版本。' : '这个场景还没有组件节点。'} />}
          {validation && <div className={`validation-result${validation.valid ? ' validation-result--ok' : ''}`}><strong>{validation.valid ? '校验通过' : `${validation.errors.length} 个问题`}</strong>{validation.errors.map((item) => <span key={item}>{item}</span>)}</div>}
        </section>
        <aside className="node-inspector panel">
          <header><Settings2 size={17} /><div><h3>节点配置</h3><p>参数与目标主机组</p></div></header>
          <div className="inspector-form"><label><span>执行策略 JSON</span><textarea aria-label="执行策略 JSON" className="code-editor code-editor--small" value={executionPolicy} disabled={!editable} onChange={(event) => setExecutionPolicy(event.target.value)} /></label></div>
          {selectedNode ? <div className="inspector-form"><label><span>显示名称</span><input value={selectedNode.data.label} disabled={!editable} onChange={(event) => updateSelected({ label: event.target.value })} /></label><label><span>组件分层</span><input value={selectedNodeComponent ? `${componentLayer(selectedNodeComponent.layer).code} · ${componentLayer(selectedNodeComponent.layer).label}` : '—'} disabled /></label><label><span>精确版本</span><input value={selectedNode.data.version ?? ''} disabled /></label><label><span>生命周期动作</span><select value={selectedNode.data.action ?? availableNodeActions[0]} disabled={!editable} onChange={(event) => updateSelected({ action: event.target.value as ScenarioNodeData['action'] })}>{availableNodeActions.map((action) => <option key={action} value={action}>{action}</option>)}</select></label><label><span>主机组</span><input value={selectedNode.data.hostGroup ?? ''} disabled={!editable} onChange={(event) => updateSelected({ hostGroup: event.target.value })} /></label>{mappingDependencies.length ? <div className="source-picker"><strong>依赖参数来源</strong>{mappingDependencies.map((dependency) => {
            const options = sourceOptionsByDependency[dependency.id ?? dependency.releaseId] ?? [];
            const selectedSource = selectedNode.data.dependencySources?.[dependency.id ?? ''];
            return <div key={dependency.id ?? dependency.releaseId} className="source-picker__item">
              {(dependency.parameterMappings ?? []).map((mapping) => <p key={`${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">{describeParameterMapping(dependency, mapping, components ?? [])}</p>)}
              {options.length <= 1
                ? <label><span>来源节点</span><input value={options[0] ? `${options[0].data.label} · ${options[0].data.version} · ${options[0].data.hostGroup}` : '自动绑定'} disabled /></label>
                : <label><span>来源节点</span><select required value={selectedSource ?? ''} disabled={!editable} onChange={(event) => updateSelected({ dependencySources: { ...selectedNode.data.dependencySources, [dependency.id ?? '']: event.target.value } })}><option value="">请选择来源节点</option>{options.map((option) => <option key={option.id} value={option.id}>{option.data.label} · {option.data.version} · {option.data.hostGroup}</option>)}</select></label>}
            </div>;
          })}</div> : null}{mappedTargets.size ? <p className="palette-hint">已由上游映射的参数不可再配置：{[...mappedTargets].join(', ')}</p> : null}<label><span>节点参数 JSON</span><textarea key={`${selectedNode.id}-values`} className="code-editor code-editor--small" defaultValue={JSON.stringify(Object.fromEntries(Object.entries(selectedNode.data.values ?? {}).filter(([key]) => !mappedTargets.has(key))), null, 2)} disabled={!editable} onBlur={(event) => { try { updateSelected({ values: Object.fromEntries(Object.entries(JSON.parse(event.target.value) as Record<string, unknown>).filter(([key]) => !mappedTargets.has(key))) }); } catch { notify('error', '节点参数不是有效 JSON'); } }} /></label><label><span>环境参数绑定 JSON</span><textarea key={`${selectedNode.id}-bindings`} className="code-editor code-editor--small" defaultValue={JSON.stringify(Object.fromEntries(Object.entries(selectedNode.data.bindings ?? {}).filter(([key]) => !mappedTargets.has(key))), null, 2)} disabled={!editable} onBlur={(event) => { try { updateSelected({ bindings: Object.fromEntries(Object.entries(JSON.parse(event.target.value) as Record<string, string>).filter(([key]) => !mappedTargets.has(key))) }); } catch { notify('error', '参数绑定不是有效 JSON'); } }} /></label><label><span>运行时输入（逗号分隔）</span><input value={(selectedNode.data.runInputs ?? []).filter((item) => !mappedTargets.has(item)).join(', ')} disabled={!editable} onChange={(event) => updateSelected({ runInputs: event.target.value.split(',').map((item) => item.trim()).filter((item) => item && !mappedTargets.has(item)) })} /></label>{editable && <button className="button button--danger-soft" onClick={() => { setNodes((items) => items.filter((node) => node.id !== selectedNode.id)); setEdges((items) => items.filter((edge) => edge.source !== selectedNode.id && edge.target !== selectedNode.id)); setSelectedNodeId(undefined); }}><Trash2 size={15} /> 删除节点</button>}</div> : <EmptyState title="选择一个节点" description="查看版本、动作和参数绑定。" />}
        </aside>
      </div> : <div className="panel"><EmptyState title="暂无场景" description="请先由场景 Owner 创建一个场景。" /></div>}
    </>}
    {testOpen && <Modal title={revision?.state === 'released' ? '运行已发布场景' : '场景完整测试'} description="运行将锁定当前场景、组件、环境 revision、运行参数和 Playbook 摘要。" onClose={() => setTestOpen(false)}><div className="modal-body"><label><span>共享测试环境</span><select value={testEnvironment} onChange={(event) => setTestEnvironment(event.target.value)}><option value="">请选择</option>{environments?.map((environment: Environment) => <option key={environment.id} value={environment.id}>{environment.name} · {environment.status ?? 'ready'}</option>)}</select></label><RunInputFields names={declaredRunInputs} values={runInputValues} onChange={(name, value) => setRunInputValues((current) => ({ ...current, [name]: value }))} /></div><footer className="modal-actions"><button className="button button--quiet" onClick={() => setTestOpen(false)}>取消</button><button className="button button--primary" disabled={!testEnvironment || busy === 'test'} onClick={() => void test()}><Beaker size={16} /> {revision?.state === 'released' ? '开始运行' : '开始完整测试'}</button></footer></Modal>}
    {createOpen && <CreateScenarioModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('scenarios'); }} />}
  </div>;
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
