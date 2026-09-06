import { activeWorkbench, activeRun } from '../hooks/activeWork';
import {EnvironmentFactFields} from '../components/EnvironmentFactFields';
import { JobPlanPreview } from '../components/JobPlanPreview';
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { Activity, AlertTriangle, Archive, ArchiveRestore, Braces, CheckCircle2, CloudCog, Cpu, Download, GitCompare, HardDrive, History, KeyRound, LockKeyhole, Network, Plus, RotateCcw, Save, Server, Trash2, Upload, UserRound, Wifi } from 'lucide-react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { ParameterValueEditor } from '../components/ParameterEditors';
import { EnvironmentInventoryEditor } from '../components/EnvironmentInventoryEditor';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { CredentialRef, Environment, EnvironmentExportDocument, EnvironmentHealthCheck, EnvironmentHost, EnvironmentImportPlan, EnvironmentLifecycle, EnvironmentRevision, EnvironmentRollbackPlan, EnvironmentSSHCheck, WorkExplanation } from '../types/domain';
import { activeEnvironmentConstraintDimensions } from '../types/environmentConstraints';

type Tab = 'inventory' | 'facts' | 'parameters' | 'variables' | 'credentials';
type EnvironmentVariableRow = { name: string; value: string };

const SCHEDULING_LABELS: Record<string, string> = {
  idle: '调度空闲', queued: '队列等待', awaiting_approval: '等待审批', running: '执行中',
};

function stable(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stable).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.entries(value as Record<string, unknown>).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => `${JSON.stringify(key)}:${stable(item)}`).join(',')}}`;
  return JSON.stringify(value);
}

type CredentialSource = Awaited<ReturnType<typeof api.environmentCredentialSources>>[number];
function CredentialSourceList({sources,name,loading}:{sources?:CredentialSource[];name:string;loading:boolean}) {
  if (!sources) return loading ? <small>正在读取声明来源…</small> : null;
  const matching = sources.filter(source => source.name === name);
  const render = (items: CredentialSource[]) => items.map(source => <div className="configuration-source" key={[source.kind,source.releaseId,source.revisionId,source.actionId,source.name].join(':')}><span>{source.kind === 'platform_ssh' ? '平台用途' : source.kind === 'component_action' ? '组件声明' : '业务验收声明'}</span>{source.kind === 'platform_ssh' ? <span>{source.description}</span> : <Link to={source.componentId ? `/components?selected=${encodeURIComponent(source.componentId)}&release=${encodeURIComponent(source.releaseId ?? '')}` : `/scenarios?selected=${encodeURIComponent(source.scenarioId ?? '')}&revision=${encodeURIComponent(source.revisionId ?? '')}`}>{source.componentId ? `${source.componentName} · ${source.lineName} · ${source.version}` : `${source.scenarioName} · r${source.revision}`} · {source.actionName}</Link>}</div>);
  return <div className="credential-source-list">{matching.length ? <><small>{matching.length} 个可见声明来源</small>{render(matching.slice(0,3))}{matching.length > 3 && <details><summary>其余 {matching.length-3} 个来源</summary>{render(matching.slice(3))}</details>}</> : <small>暂无可见声明来源；不能据此判断是否可删除。</small>}</div>;
}

function schedulingLabel(environment: Environment): string {
  return SCHEDULING_LABELS[environment.schedulingStatus ?? 'idle'] ?? '调度未知';
}

function revisionActor(revision: EnvironmentRevision, users: Array<{ id: string; name: string }>): string {
  return users.find((item) => item.id === revision.createdBy)?.name ?? revision.createdBy ?? '系统初始化';
}


export function EnvironmentsPage() {
  const { user, users, notify, signalRefresh, platformOptionCategories } = useApp();
  const hostGroupOptions = platformOptionCategories.find((category) => category.kind === 'host_group')?.options ?? [];
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: environments, loading, error, isRefreshing, reload } = useApiData((signal) => api.environments(signal, user.role === 'environment_owner' || user.role === 'platform_admin'), [user.id, user.role], 'environments');
  const { data: parameterFields } = useApiData((signal) => api.environmentParameterFields(signal), [user.id], 'environment-parameter-fields');
  const { data: variableDefinitions } = useApiData((signal) => api.environmentVariableDefinitions(signal), [user.id], 'environment-variable-definitions');
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench', activeWorkbench);
  const selectedId = searchParams.get('selected') ?? '';
  const selected = useMemo(() => environments?.find((item) => item.id === selectedId) ?? environments?.[0], [environments, selectedId]);
  const credentialSources = useApiData((signal) => selected ? api.environmentCredentialSources(selected.id, selected.currentRevision?.id ?? '', signal) : Promise.resolve([]), [user.id, selected?.id, selected?.currentRevision?.id], ['components', 'scenarios', 'environments']);
  const recentRunQuery = useApiData((signal) => selected ? api.runs({ environmentId: selected.id, pageSize: 4 }, signal) : Promise.resolve(undefined), [user.id, selected?.id], 'runs', page => Boolean(page?.items.some(activeRun)));
  const environmentWorkItem = workbench?.items.find((item) => item.subject.type === 'environment' && item.subject.id === selected?.id);
  const [tab, setTab] = useState<Tab>('inventory');
  const [hosts, setHosts] = useState<EnvironmentHost[]>([]);
  const [facts, setFacts] = useState<Record<string, unknown>>({});
  const [parameters, setParameters] = useState<Record<string, unknown>>({});
  const [variables, setVariables] = useState<EnvironmentVariableRow[]>([]);
  const [credentials, setCredentials] = useState<CredentialRef[]>([]);
  const [busy, setBusy] = useState(false);
  const [healthBusy, setHealthBusy] = useState(false);
  const [health, setHealth] = useState<EnvironmentHealthCheck>();
  const [sshCheck, setSSHCheck] = useState<EnvironmentSSHCheck>();
  const [connectivityError, setConnectivityError] = useState<string>();
  const [createOpen, setCreateOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [restoreRevision, setRestoreRevision] = useState<EnvironmentRevision>();
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackPlan, setRollbackPlan] = useState<EnvironmentRollbackPlan>();
  const [rollbackError, setRollbackError] = useState<string>();
  const [rollbackExplanation, setRollbackExplanation] = useState<WorkExplanation>();
  const [rollbackBusy, setRollbackBusy] = useState<'preview' | 'submit'>();
  const [lifecycleOpen, setLifecycleOpen] = useState(false);
  const [lifecycle, setLifecycle] = useState<EnvironmentLifecycle>();
  const [lifecycleBusy, setLifecycleBusy] = useState<'load' | 'delete' | 'archive' | 'unarchive'>();
  const [lifecycleError, setLifecycleError] = useState<string>();
  const [lifecycleExplanation, setLifecycleExplanation] = useState<WorkExplanation>();
  const handledDeepLink = useRef<string>();
  const ownsSelected = user.role === 'environment_owner' && selected?.ownerId === user.id;
  const editable = ownsSelected && !selected?.archivedAt;

  useEffect(() => {
    setHosts(selected?.currentRevision?.hosts ?? []);
    setFacts(selected?.currentRevision?.facts ?? {});
    setParameters(selected?.currentRevision?.parameters ?? {});
    setVariables(Object.entries(selected?.currentRevision?.variables ?? {}).map(([name, value]) => ({ name, value })));
    setCredentials(selected?.currentRevision?.credentialRefs ?? []);
    setHealth(selected?.healthCheck);
    setSSHCheck(selected?.sshCheck);
    setConnectivityError(undefined);
  }, [selected?.currentRevision?.id, selected?.healthCheck?.id, selected?.sshCheck?.id, selected?.id]);

  useEffect(() => {
    const requested = searchParams.get('tab');
    if (requested === 'inventory' || requested === 'facts' || requested === 'parameters' || requested === 'variables' || requested === 'credentials') setTab(requested);
    const focus = searchParams.get('focus');
    if (focus) requestAnimationFrame(() => document.getElementById(focus === 'health' ? 'environment-health' : `environment-variable-${focus}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }));
    const action = searchParams.get('action');
    if (action !== 'rollback') handledDeepLink.current = undefined;
    if (action === 'rollback' && selected && editable) {
      const key = `${selected.id}:rollback`;
      if (handledDeepLink.current !== key) {
        handledDeepLink.current = key;
        void previewClusterRollback();
        const next = new URLSearchParams(searchParams);
        next.delete('action');
        setSearchParams(next, { replace: true });
      }
    }
  }, [editable, searchParams, selected?.id, setSearchParams]);

  const baseline = selected?.currentRevision;
  const effectiveParameters = parameters;
  const staleParameterKeys = parameterFields === undefined ? [] : Object.keys(effectiveParameters).filter((key) => !parameterFields.some((field) => field.valueKey === key));
  const variablesObject = Object.fromEntries(variables.map((item) => [item.name.trim(), item.value]));
  const dirtyByTab: Record<Tab, boolean> = {
    inventory: stable(hosts) !== stable(baseline?.hosts ?? []),
    facts: stable(facts) !== stable(baseline?.facts ?? {}),
    parameters: stable(effectiveParameters) !== stable(baseline?.parameters ?? {}),
    variables: stable(variablesObject) !== stable(baseline?.variables ?? {}),
    credentials: stable(credentials.map(({ name, type, reference }) => ({ name, type, reference }))) !== stable((baseline?.credentialRefs ?? []).map(({ name, type, reference }) => ({ name, type, reference }))),
  };
  const dirty = dirtyByTab[tab];
  const anyDirty = Object.values(dirtyByTab).some(Boolean);

  useEffect(() => {
    if (!anyDirty) return;
    const guard = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
    window.addEventListener('beforeunload', guard);
    return () => window.removeEventListener('beforeunload', guard);
  }, [anyDirty]);

  const diffLines = useMemo(() => {
    if (!baseline) return [];
    if (tab === 'inventory') {
      const oldNames = new Set(baseline.hosts.map((item) => item.name));
      const newNames = new Set(hosts.map((item) => item.name));
      const added = [...newNames].filter((name) => !oldNames.has(name));
      const removed = [...oldNames].filter((name) => !newNames.has(name));
      const describeGroups = (groups: string[]) => groups.map((value) => hostGroupOptions.find((option) => option.value === value)?.label ?? value).join('、') || '未分组';
      const changes = hosts.flatMap((host) => {
        const previous = baseline.hosts.find((item) => item.name === host.name);
        if (!previous) return [];
        return [
          ...(stable(previous.groups) !== stable(host.groups) ? [`${host.name} 主机组：${describeGroups(previous.groups)} → ${describeGroups(host.groups)}`] : []),
          ...(previous.address !== host.address || previous.user !== host.user || previous.port !== host.port ? [`${host.name} 地址或 SSH 连接信息发生修改`] : []),
        ];
      });
      return [`主机数量：${baseline.hosts.length} → ${hosts.length}`, ...(added.length ? [`新增：${added.join('、')}`] : []), ...(removed.length ? [`移除：${removed.join('、')}`] : []), ...changes];
    }
    if (tab === 'facts') {
      try {
        const keys = new Set([...Object.keys(baseline.facts), ...Object.keys(facts)]);
        return [...keys].filter((key) => stable(baseline.facts[key]) !== stable(facts[key])).map((key) => `适配标签 · 实际环境 ${key}：${JSON.stringify(baseline.facts[key] ?? '（无）')} → ${JSON.stringify(facts[key] ?? '（删除）')}`);
      } catch { return ['适配标签 · 实际环境无法生成差异']; }
    }
    if (tab === 'variables') {
      const keys = new Set([...Object.keys(baseline.variables), ...Object.keys(variablesObject)]);
      return [...keys].filter((key) => baseline.variables[key] !== variablesObject[key]).map((key) => `${key}：${baseline.variables[key] ?? '（无）'} → ${variablesObject[key] ?? '（删除）'}`);
    }
    if (tab === 'parameters') {
      const fields = new Map((parameterFields ?? []).map((field) => [field.valueKey, field.label]));
      const keys = new Set([...Object.keys(baseline.parameters), ...Object.keys(effectiveParameters)]);
      return [...keys].filter((key) => stable(baseline.parameters[key]) !== stable(effectiveParameters[key])).map((key) => `${fields.get(key) ?? key}：${JSON.stringify(baseline.parameters[key] ?? '（无）')} → ${JSON.stringify(effectiveParameters[key] ?? '（未填写）')}`);
    }
    const oldNames = baseline.credentialRefs.map((item) => item.name);
    const newNames = credentials.map((item) => item.name);
    return [`CredentialRef：${oldNames.length} → ${newNames.length}`, `名称：${newNames.join('、') || '（空）'}`];
  }, [baseline, facts, hosts, tab, variablesObject, credentials, effectiveParameters, parameterFields, hostGroupOptions]);

  function discardCurrent() {
    if (!baseline) return;
    if (tab === 'inventory') setHosts(baseline.hosts);
    if (tab === 'facts') setFacts(baseline.facts);
    if (tab === 'parameters') setParameters(baseline.parameters);
    if (tab === 'variables') setVariables(Object.entries(baseline.variables).map(([name, value]) => ({ name, value })));
    if (tab === 'credentials') setCredentials(baseline.credentialRefs);
  }

  async function saveCurrent(changeReason: string) {
    if (!selected || !editable || !dirty) return;
    setBusy(true);
    try {
      if (tab === 'inventory') await api.updateInventory(selected.id, hosts, changeReason);
      if (tab === 'facts') await api.updateFacts(selected.id, facts, changeReason);
      if (tab === 'parameters') {
        await api.updateEnvironmentParameters(selected.id, effectiveParameters, changeReason);
      }
      if (tab === 'variables') {
        const names = variables.map((item) => item.name.trim());
        if (names.some((name) => !/^[A-Z_][A-Z0-9_]*$/.test(name))) throw new Error('环境变量名必须是大写标识符。');
        if (new Set(names).size !== names.length) throw new Error('环境变量名不能重复。');
        const governed = new Set((variableDefinitions ?? []).map((item) => item.name));
        if (names.some((name) => !governed.has(name))) throw new Error('环境变量必须从平台 Owner 维护的字段中选择。');
        await api.updateVariables(selected.id, variablesObject, changeReason);
      }
      if (tab === 'credentials') await api.updateCredentialRefs(selected.id, credentials.map(({ name, type, reference }) => ({ name, type, reference })), changeReason);
      setSaveOpen(false);
      notify('success', '环境 Revision 已更新', '差异和变更原因已记录，后续运行会锁定新快照。');
      signalRefresh(['environments', 'workbench']);
    } catch (reason) {
      notify('error', '环境保存失败', reason instanceof SyntaxError ? '内容必须是有效 JSON。' : displayError(reason));
    } finally { setBusy(false); }
  }

  async function checkHealth() {
    if (!selected || !editable) return;
    setHealthBusy(true);
    setConnectivityError(undefined);
    try {
      const result = await api.checkEnvironmentConnectivity(selected.id);
      setHealth(result.tcpCheck);
      setSSHCheck(result.sshCheck);
      const passedTCP = result.tcpCheck.results.filter((item) => item.reachable).length;
      const passedSSH = result.sshCheck.results.filter((item) => item.status === 'passed' || item.status === 'skipped').length;
      const healthy = result.tcpCheck.status === 'healthy' && result.sshCheck.status === 'healthy';
      notify(healthy ? 'success' : 'error', healthy ? '环境检查通过' : '环境检查发现异常', `TCP ${passedTCP}/${result.tcpCheck.results.length}；SSH ${passedSSH}/${result.sshCheck.results.length}。`);
      signalRefresh(['environments', 'workbench']);
    } catch (reason) {
      const message = displayError(reason);
      setConnectivityError(message);
      notify('error', '环境检查失败', message);
    } finally { setHealthBusy(false); }
  }

  async function restore(reason: string) {
    if (!selected || !restoreRevision) return;
    setBusy(true);
    try {
      await api.restoreEnvironmentRevision(selected.id, restoreRevision.id, reason);
      notify('success', `已基于 r${restoreRevision.revision} 创建新 Revision`, '历史没有被覆盖，后续 Run 将锁定恢复后的新快照。');
      setRestoreRevision(undefined);
      signalRefresh(['environments', 'workbench']);
    } catch (errorReason) { notify('error', 'Revision 恢复失败', displayError(errorReason)); } finally { setBusy(false); }
  }

  async function exportRevision(revision: EnvironmentRevision, includeCredentialReferences: boolean) {
    if (!selected) return;
    if (includeCredentialReferences && !window.confirm('敏感导出会包含 CredentialRef 的引用字符串，但不会包含真实 Secret。确认下载并自行妥善保管？')) return;
    try {
      const blob = await api.exportEnvironmentRevision(selected.id, revision.id, includeCredentialReferences);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url; anchor.download = `${selected.name}-r${revision.revision}${includeCredentialReferences ? '-credential-refs' : ''}.json`; anchor.click();
      URL.revokeObjectURL(url);
      notify('success', includeCredentialReferences ? '已导出含凭据引用的环境文件' : '已安全导出环境文件', '导出内容不包含 Secret 实际值、运行记录或历史证据。');
    } catch (reason) { notify('error', '环境导出失败', displayError(reason)); }
  }

  async function previewClusterRollback(nodes?: string[]) {
    if (!selected || !editable) return;
    setRollbackOpen(true);
    setRollbackPlan(undefined);
    setRollbackError(undefined);
    setRollbackExplanation(undefined);
    setRollbackBusy('preview');
    try {
      setRollbackPlan(await api.previewEnvironmentRollback(selected.id, nodes));
    } catch (reason) {
      setRollbackError(displayError(reason));
      setRollbackExplanation(actionableExplanation(reason));
    } finally { setRollbackBusy(undefined); }
  }

  async function submitClusterRollback(confirmEnvironmentName: string) {
    if (!selected || !rollbackPlan) return;
    setRollbackBusy('submit');
    setRollbackError(undefined);
    setRollbackExplanation(undefined);
    try {
      const run = await api.startEnvironmentRollback(selected.id, { expectedPlanDigest: rollbackPlan.planDigest, confirmEnvironmentName, nodes: rollbackPlan.nodes });
      notify('success', '整集群回滚 Run 已创建', '当前处于待审批状态；请在运行中心复核风险后批准。');
      setRollbackOpen(false);
      setRollbackPlan(undefined);
      signalRefresh(['runs', 'environments', 'workbench']);
      navigate(`/runs?selected=${run.id}`);
    } catch (reason) {
      setRollbackError(displayError(reason));
      setRollbackExplanation(actionableExplanation(reason));
      setRollbackPlan(undefined);
    } finally { setRollbackBusy(undefined); }
  }

  async function openLifecycle() {
    if (!selected || !ownsSelected || anyDirty) return;
    setLifecycleOpen(true);
    setLifecycle(undefined);
    setLifecycleError(undefined);
    setLifecycleExplanation(undefined);
    setLifecycleBusy('load');
    try {
      setLifecycle(await api.environmentLifecycle(selected.id));
    } catch (reason) {
      setLifecycleError(displayError(reason));
      setLifecycleExplanation(actionableExplanation(reason));
    } finally { setLifecycleBusy(undefined); }
  }

  async function applyLifecycle(action: 'delete' | 'archive' | 'unarchive') {
    if (!selected) return;
    setLifecycleBusy(action);
    setLifecycleError(undefined);
    setLifecycleExplanation(undefined);
    try {
      if (action === 'delete') {
        await api.deleteEnvironment(selected.id);
        notify('success', '环境已删除', '从未使用的环境及其 Revision、健康检查已永久删除，审计记录已保留。');
        setSearchParams({});
      } else if (action === 'archive') {
        await api.archiveEnvironment(selected.id);
        notify('success', '环境已归档', '历史 Run、Revision 和构建证据仍可追溯；该环境不再参与新任务选择。');
      } else {
        await api.unarchiveEnvironment(selected.id);
        notify('success', '环境已恢复', '该环境重新出现在组件构建和场景运行的目标选择中。');
      }
      setLifecycleOpen(false);
      setLifecycle(undefined);
      signalRefresh(['environments', 'workbench']);
    } catch (reason) {
      setLifecycleError(displayError(reason));
      setLifecycleExplanation(actionableExplanation(reason));
      try { setLifecycle(await api.environmentLifecycle(selected.id)); } catch { /* Retain the actionable mutation error. */ }
    } finally { setLifecycleBusy(undefined); }
  }

  function selectEnvironment(id: string) {
    if (anyDirty) {
      notify('info', '存在未保存更改', '请先保存或放弃当前编辑，再切换环境。');
      return;
    }
    setSearchParams({ selected: id });
  }

  const environmentRuns = recentRunQuery.data?.items ?? [];
  const currentFacts = selected?.currentRevision?.facts ?? {};
  const healthStale = Boolean(health && health.environmentRevisionId !== selected?.currentRevision?.id);
  const sshCheckStale = Boolean(sshCheck && sshCheck.environmentRevisionId !== selected?.currentRevision?.id);


  return <div className="page">
    <PageHeader eyebrow="Execution environments" title="环境管理" description="分别查看调度占用与真实连通性；配置变更以可追溯 Revision 保存。" actions={user.role === 'environment_owner' ? <><button className="button button--quiet" onClick={() => setImportOpen(true)}><Upload size={16} /> 导入环境</button><button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建环境</button></> : undefined} />
    <RefreshNotice loading={isRefreshing} error={environments ? error : undefined} onRetry={() => void reload()} />
    {loading && !environments ? <LoadingBlock label="正在读取共享环境…" /> : error && !environments ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="catalog-layout">
      <aside className="catalog-list panel">
        <div className="catalog-list__header"><strong>共享环境</strong><span>{environments?.length ?? 0}</span></div>
        {environments?.map((environment) => <button key={environment.id} className={`catalog-item${selected?.id === environment.id ? ' active' : ''}`} onClick={() => selectEnvironment(environment.id)}><span className="catalog-item__icon catalog-item__icon--cyan"><CloudCog size={18} /></span><span><strong>{environment.name}</strong><small>r{environment.currentRevision?.revision ?? 1} · {environment.currentRevision?.hosts?.length ?? 0} 台主机</small></span><StatusPill status={environment.archivedAt ? 'offline' : environment.schedulingStatus ?? 'idle'}>{environment.archivedAt ? '已归档' : schedulingLabel(environment)}</StatusPill></button>)}
      </aside>
      {selected ? <section className="detail-stack">
        <article className="panel environment-hero">
          <div className="environment-hero__summary"><span className="environment-icon"><CloudCog size={25} /></span><div><div className="eyebrow">Environment revision {selected.currentRevision?.revision ?? 1}</div><h2>{selected.name}</h2><p>{selected.description ?? '用于平台组件与场景测试的共享环境'}</p></div></div>
          <div className="environment-owner"><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}<StatusPill status={selected.archivedAt ? 'offline' : selected.schedulingStatus ?? 'idle'}>{selected.archivedAt ? '已归档' : schedulingLabel(selected)}</StatusPill></div>
          {(ownsSelected || (editable && selected.currentRevision)) && <div className="environment-hero__toolbar" role="group" aria-label="环境操作">
            {editable && selected.currentRevision && <><button className="button button--quiet" onClick={() => void exportRevision(selected.currentRevision!, false)}><Download size={15} /> 安全导出</button><button className="button button--quiet" onClick={() => void exportRevision(selected.currentRevision!, true)}><KeyRound size={15} /> 导出含引用</button></>}
            {ownsSelected && <button className={selected.archivedAt ? 'button button--secondary' : 'button button--danger-soft'} disabled={anyDirty || lifecycleBusy !== undefined} onClick={() => void openLifecycle()}>{selected.archivedAt ? <ArchiveRestore size={15} /> : <Archive size={15} />} {selected.archivedAt ? '恢复环境' : '移除环境'}</button>}
            {editable && selected.currentRevision && <button className="button button--danger" disabled={(selected.schedulingStatus ?? 'idle') !== 'idle' || anyDirty || rollbackBusy !== undefined} title={(selected.schedulingStatus ?? 'idle') !== 'idle' ? '请先处理当前活动 Run' : anyDirty ? '请先保存或放弃环境配置更改' : undefined} onClick={() => void previewClusterRollback()}><RotateCcw size={15} /> 手动回滚组件</button>}
          </div>}
        </article>
        {selected.archivedAt && <div className="inline-warning"><Archive size={17} /><span>该环境已归档，仅保留配置和历史证据；不能创建 Revision、健康检查、构建或 Run。需要再次使用时先恢复环境。</span></div>}
        <StatusExplanationPanel item={environmentWorkItem} />
        <section className="fact-grid">
          <article><Cpu size={18} /><span>架构</span><strong>{String(currentFacts.architecture ?? '—')}</strong></article>
          <article><HardDrive size={18} /><span>操作系统</span><strong>{String(currentFacts.operatingSystem ?? '—')}</strong></article>
          <article><HardDrive size={18} /><span>系统版本</span><strong>{String(currentFacts.operatingSystemVersion ?? '—')}</strong></article>
          <article><Network size={18} /><span>网络栈</span><strong>{String(currentFacts.ipFamily ?? '—')}</strong></article>
          <article><Server size={18} /><span>主机</span><strong>{hosts.length}</strong></article>
        </section>

        <article className="panel health-panel" id="environment-health">
          <header className="panel__header"><div><span className={`panel__icon ${health?.status === 'degraded' || sshCheck?.status === 'degraded' ? 'panel__icon--rose' : 'panel__icon--cyan'}`}><Activity size={18} /></span><div><h2>环境连通性</h2><p>一次完成 TCP 端点探测和 Go SSH 检查；检查只读，不执行安装。</p></div></div>{editable && <button className="button button--secondary" disabled={healthBusy} onClick={() => void checkHealth()}><Wifi size={15} /> {healthBusy ? '正在检查 TCP 与 SSH…' : '立即检查'}</button>}</header>
          {connectivityError && <ErrorBlock message={connectivityError} onRetry={() => void checkHealth()} />}
          {!health && !sshCheck ? <EmptyState title="尚未检查" description="“调度空闲”不代表端口可达、SSH 可认证或远端会话可执行。" /> : <div className="connectivity-results">
            <section className="health-results" aria-label="TCP 端点检查结果">
              <div className="connectivity-section-title"><strong>TCP 端点检查</strong><small>SSH 端口、IMAGE_REGISTRY、FILE_STATION</small></div>
              {!health ? <EmptyState title="TCP 尚未检查" /> : <>
                <div className={`health-summary health-summary--${health.status}`}>{health.status === 'healthy' ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}<div><strong>{health.status === 'healthy' ? '全部 TCP 端点可达' : '存在 TCP 不可达端点'}</strong><small>{formatTime(health.checkedAt)} · {health.results.filter((item) => item.reachable).length}/{health.results.length} 通过{healthStale ? ' · 基于旧 Revision，请重新检查' : ''}</small></div></div>
                {health.results.map((item) => <div className="health-result" key={`${item.kind}-${item.name}`}><span className={item.reachable ? 'health-dot health-dot--ok' : 'health-dot health-dot--failed'} /><div><strong>{item.name}</strong><small>{item.address}</small></div><span className={item.reachable ? '' : 'health-result__error'}>{item.reachable ? `${item.latencyMs} ms` : item.error ?? 'TCP 不可达'}</span></div>)}
              </>}
            </section>
            <section className="health-results" aria-label="SSH 检查结果">
              <div className="connectivity-section-title"><strong>SSH 检查</strong><small>主机指纹、身份认证、远端 true 命令</small></div>
              {!sshCheck ? <EmptyState title="SSH 尚未检查" /> : <>
                <div className={`health-summary health-summary--${sshCheck.status}`}>{sshCheck.status === 'healthy' ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}<div><strong>{sshCheck.status === 'healthy' ? 'SSH 检查通过' : 'SSH 检查存在异常'}</strong><small>{formatTime(sshCheck.checkedAt)} · {sshCheck.results.filter((item) => item.status === 'passed' || item.status === 'skipped').length}/{sshCheck.results.length} 通过 · {sshCheck.durationMs} ms{sshCheckStale ? ' · 基于旧 Revision，请重新检查' : ''}</small></div></div>
                {sshCheck.results.map((item) => {
                  const passed = item.status === 'passed' || item.status === 'skipped';
                  return <div className="health-result" key={`${item.kind}-${item.name}`}><span className={passed ? 'health-dot health-dot--ok' : 'health-dot health-dot--failed'} /><div><strong>{item.name}</strong><small>{item.user ? `${item.user}@` : ''}{item.address}</small></div><span className={passed ? '' : 'health-result__error'}>{passed ? item.status === 'skipped' ? item.message : 'SSH 认证与 true 执行通过' : <>{item.message ?? 'SSH 检查失败'}{item.errorCode && <small>{item.errorCode}</small>}</>}</span></div>;
                })}
              </>}
            </section>
          </div>}
        </article>

        <article className="panel environment-editor">
          <div className="tabs" role="tablist">{([
            ['inventory', 'Inventory', <Server size={16} />], ['facts', '适配标签 · 实际环境', <Cpu size={16} />], ['parameters', '组件环境参数', <CloudCog size={16} />], ['variables', '环境变量', <Braces size={16} />], ['credentials', '凭据引用', <KeyRound size={16} />],
          ] as const).map(([value, label, icon]) => <button key={value} role="tab" aria-selected={tab === value} className={tab === value ? 'active' : ''} onClick={() => setTab(value)}>{icon}{label}{dirtyByTab[value] && <span className="dirty-dot" aria-label="有未保存更改" />}</button>)}</div>
          {tab === 'inventory' && <EnvironmentInventoryEditor key={`${selected.id}:${selected.currentRevision?.id}`} hosts={hosts} options={hostGroupOptions} editable={Boolean(editable)} onChange={setHosts} />}
          {tab === 'facts' && <div className="editor-section"><div className="section-title"><div><h3>适配标签 · 实际环境</h3><p>每个分类选择一个实际值；子分类须与所选父分类对应。</p></div></div><EnvironmentFactFields categories={platformOptionCategories} facts={facts} editable={editable} onChange={setFacts} /></div>}
          {tab === 'parameters' && <div className="editor-section"><div className="section-title"><div><h3>组件环境参数</h3><p>字段由组件版本定义，环境 Owner 填写实际值或采用组件建议值。保存后记录到环境版本。</p></div></div>
            <div className="scenario-parameter-fields">{(parameterFields ?? []).map((field) => {
              const value = effectiveParameters[field.valueKey];
              const required = field.required;
              return <label key={field.valueKey} className={required && (value === undefined || value === null || (typeof value === 'string' && !value.trim())) ? 'field-invalid' : ''}>
                <span>{field.label}{required ? '（必填）' : '（可选）'}</span>
                <small>{field.description}</small><div className="configuration-source"><span>定义来源</span>{field.bindings.map(binding => binding.canViewContract ? <a key={binding.releaseId} href={`/components?selected=${encodeURIComponent(binding.componentId)}&release=${encodeURIComponent(binding.releaseId)}&focus=parameters`}>{binding.componentName} · {binding.lineName} · {binding.version} · {binding.parameterName}</a> : <span key={binding.releaseId}>{binding.componentName} · {binding.lineName} · {binding.version}（合同未共享）</span>)}</div><small>当前值：{selected.name} · r{selected.currentRevision?.revision}</small>
                <ParameterValueEditor parameter={{ name: field.label, type: field.type, enum: field.enum }} value={value ?? undefined} disabled={!editable} optional={!required} onChange={(nextValue) => setParameters((current) => { const next = { ...current }; if (nextValue === undefined) delete next[field.valueKey]; else next[field.valueKey] = nextValue ?? null; return next; })} />
                {editable && value === undefined && field.suggestedValue !== undefined ? <button type="button" className="icon-text" onClick={() => setParameters((current) => ({ ...current, [field.valueKey]: field.suggestedValue }))}>采用建议值 {String(field.suggestedValue)}</button> : null}
              </label>;
            })}{!(parameterFields ?? []).length ? <EmptyState title="没有环境 Owner 参数" description="当前组件契约没有分配环境字段。" /> : null}</div>
            {staleParameterKeys.length > 0 && <section className="stale-parameter-notice" aria-label="失效环境参数">
              <header><span className="stale-parameter-notice__icon"><AlertTriangle size={18} /></span><div><div className="stale-parameter-notice__title"><h3>已从组件契约移除的参数</h3><em>{staleParameterKeys.length} 项待清理</em></div><p>当前契约不再引用这些值。移除全部项目后保存为新 Revision，历史版本仍会完整保留。</p></div></header>
              <div className="stale-parameter-list">{staleParameterKeys.map((key) => <article key={key} className="stale-parameter-item"><div><strong>{key.split(':').at(-1)}</strong><small>当前环境保留值</small></div><code>{JSON.stringify(effectiveParameters[key])}</code>{editable && <button type="button" className="button button--danger-soft" aria-label={`移除失效参数 ${key}`} onClick={() => setParameters((current) => { const next = { ...current }; delete next[key]; return next; })}><Trash2 size={14} /> 移除</button>}</article>)}</div>
            </section>}
          </div>}
          {tab === 'variables' && <div className="editor-section"><div className="section-title"><div><h3>组件作业环境变量</h3><p>变量键由平台 Owner 治理，环境 Owner 只选择字段并填写非敏感值。</p></div>{editable && <button className="button button--quiet" disabled={(variableDefinitions ?? []).every((definition) => variables.some((item) => item.name === definition.name))} onClick={() => setVariables((items) => [...items, { name: '', value: '' }])}><Plus size={15} /> 添加变量</button>}</div><div className="environment-variable-list">{variables.map((variable, index) => <div key={index} id={variable.name ? `environment-variable-${variable.name}` : undefined}><span className="variable-icon"><Braces size={17} /></span><select aria-label="环境变量名" value={variable.name} disabled={!editable} onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value } : item))}><option value="">请选择平台字段</option>{variable.name && !(variableDefinitions ?? []).some((definition) => definition.name === variable.name) ? <option value={variable.name}>{variable.name} · 历史字段</option> : null}{(variableDefinitions ?? []).filter((definition) => definition.name === variable.name || !variables.some((item) => item.name === definition.name)).map((definition) => <option key={definition.id} value={definition.name}>{definition.label} · {definition.name}</option>)}</select><input aria-label={`环境变量 ${variable.name || index} 的值`} value={variable.value} disabled={!editable} placeholder="非敏感字符串值" onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" aria-label={`移除环境变量 ${variable.name || index}，保存后生效`} onClick={() => setVariables((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div>{!variables.length && <EmptyState title="尚未配置环境变量" description={editable ? '从平台字段目录选择变量。' : '环境 Owner 尚未配置环境变量。'} />}</div>}
          {tab === 'credentials' && <div className="editor-section"><div className="section-title"><div><h3>CredentialRef</h3><p>查看凭据的声明来源，填写后端变量名或密钥文件路径。声明来源不表示已在本环境运行。</p></div>{editable && <button className="button button--quiet" onClick={() => setCredentials((items) => [...items, { name: '', type: 'envVarRef', reference: '' }])}><Plus size={15} /> 添加引用</button>}</div>{credentialSources.error && <ErrorBlock message={credentialSources.error} onRetry={() => void credentialSources.reload()} />}<div className="credential-list">{credentials.map((credential, index) => <div className="credential-entry" key={index}><div className="credential-fields"><span className="credential-icon"><LockKeyhole size={17} /></span><input aria-label="凭据名称" value={credential.name} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} /><select aria-label={`凭据 ${credential.name || index} 类型`} value={credential.type} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, type: event.target.value as CredentialRef['type'] } : item))}><option value="envVarRef">后端环境变量引用</option><option value="sshKeyPath">后端密钥文件路径</option></select><input aria-label={`凭据 ${credential.name || index} 引用`} value={editable ? credential.reference ?? '' : credential.maskedReference ?? '••••••••'} disabled={!editable} placeholder={credential.type === 'envVarRef' ? 'SECRET_ENV_VAR' : '/path/to/key'} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, reference: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" aria-label={`移除凭据 ${credential.name || index}，保存后生效`} onClick={() => setCredentials((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div><small>当前值：{selected.name} · r{selected.currentRevision?.revision} · {(editable ? credential.reference : credential.configured) ? '已填写引用' : '未填写引用'}</small><CredentialSourceList sources={credentialSources.data} name={credential.name} loading={credentialSources.loading}/></div>)}</div></div>}
          {editable && <footer className="editor-footer"><span className={dirty ? 'editor-dirty' : ''}><LockKeyhole size={14} /> {dirty ? '当前页有未保存更改' : '当前页与已保存 Revision 一致'}</span><div>{dirty && <button className="button button--quiet" onClick={discardCurrent}>放弃本页更改</button>}<button className="button button--primary" disabled={busy || !dirty || (tab === 'parameters' && (parameterFields === undefined || staleParameterKeys.length > 0))} onClick={() => setSaveOpen(true)}><Save size={16} /> 保存新 Revision</button></div></footer>}
        </article>

        <article className="panel revision-history"><header className="panel__header"><div><span className="panel__icon"><History size={18} /></span><div><h2>Revision 历史</h2><p>恢复旧配置时始终创建新 Revision，不覆盖历史。</p></div></div></header><div className="revision-list">{(selected.revisions ?? (selected.currentRevision ? [selected.currentRevision] : [])).map((revision) => <div key={revision.id}><span className="revision-number">r{revision.revision}</span><div><strong>{revision.changeReason || (revision.revision === 1 ? '创建环境' : '未填写变更原因')}</strong><small>{revisionActor(revision, users)} · {formatTime(revision.createdAt)}</small></div><div className="revision-actions">{revision.id === selected.currentRevision?.id ? <StatusPill status="active">当前</StatusPill> : editable && <button className="button button--quiet" disabled={anyDirty} onClick={() => setRestoreRevision(revision)}><RotateCcw size={14} /> 基于此恢复</button>}{editable && <button className="button button--quiet" onClick={() => void exportRevision(revision, false)}><Download size={14} /> 导出</button>}</div></div>)}</div></article>

        <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><LockKeyhole size={18} /></span><div><h2>最近环境运行</h2><p>同一环境一次只允许一个活动 Run</p></div></div></header><RefreshNotice loading={recentRunQuery.isRefreshing} error={recentRunQuery.error} onRetry={() => void recentRunQuery.reload()} />{environmentRuns.length ? <div className="simple-table">{environmentRuns.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`}><div><strong>{run.name}</strong><small>{formatTime(run.createdAt)}</small></div><StatusPill status={run.status} /></Link>)}</div> : recentRunQuery.loading ? <LoadingBlock label="正在读取最近运行…" /> : !recentRunQuery.error ? <EmptyState title="暂无运行记录" /> : null}</article>
      </section> : <section className="panel"><EmptyState title="没有可见环境" /></section>}
    </div>}
    {createOpen && <CreateEnvironmentModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('environments'); }} />}
    {importOpen && <EnvironmentImportModal environments={(environments ?? []).filter((environment) => !environment.archivedAt)} selected={selected?.archivedAt ? undefined : selected} onClose={() => setImportOpen(false)} onDone={(environment) => { setImportOpen(false); signalRefresh(['environments', 'workbench']); setSearchParams({ selected: environment.id }); }} />}
    {saveOpen && selected && <ChangeReasonModal title="保存为新 Revision" description={`r${selected.currentRevision?.revision ?? 0} → r${(selected.currentRevision?.revision ?? 0) + 1}`} busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 仍锁定旧 Revision。` : undefined} diffLines={diffLines} onClose={() => setSaveOpen(false)} onConfirm={(reason) => void saveCurrent(reason)} />}
    {restoreRevision && selected && <ChangeReasonModal title={`基于 r${restoreRevision.revision} 恢复`} description="将复制该历史快照并创建新的当前 Revision。" busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 不会被修改。` : undefined} diffLines={[`目标快照：r${restoreRevision.revision}`, `主机 ${restoreRevision.hosts.length} 台 · 环境变量 ${Object.keys(restoreRevision.variables).length} 个 · CredentialRef ${restoreRevision.credentialRefs.length} 个`]} onClose={() => setRestoreRevision(undefined)} onConfirm={(reason) => void restore(reason)} />}
    {rollbackOpen && selected && <ClusterRollbackModal environment={selected} plan={rollbackPlan} error={rollbackError} explanation={rollbackExplanation} busy={rollbackBusy} onScope={(nodes) => void previewClusterRollback(nodes)} onRetry={() => void previewClusterRollback()} onClose={() => { if (!rollbackBusy) { setRollbackOpen(false); setRollbackPlan(undefined); setRollbackError(undefined); setRollbackExplanation(undefined); } }} onConfirm={(confirmation) => void submitClusterRollback(confirmation)} />}
    {lifecycleOpen && selected && <EnvironmentLifecycleModal environment={selected} lifecycle={lifecycle} error={lifecycleError} explanation={lifecycleExplanation} busy={lifecycleBusy} onRetry={() => void openLifecycle()} onClose={() => { if (!lifecycleBusy) setLifecycleOpen(false); }} onConfirm={(action) => void applyLifecycle(action)} />}
  </div>;
}

function EnvironmentLifecycleModal({ environment, lifecycle, error, explanation, busy, onRetry, onClose, onConfirm }: { environment: Environment; lifecycle?: EnvironmentLifecycle; error?: string; explanation?: WorkExplanation; busy?: 'load' | 'delete' | 'archive' | 'unarchive'; onRetry: () => void; onClose: () => void; onConfirm: (action: 'delete' | 'archive' | 'unarchive') => void }) {
  const [confirmation, setConfirmation] = useState('');
  const confirmed = confirmation === environment.name;
  const action = lifecycle?.archived ? 'unarchive' : lifecycle?.canDelete ? 'delete' : 'archive';
  const blocked = !lifecycle || (!lifecycle.archived && !lifecycle.canDelete && !lifecycle.canArchive);
  const title = lifecycle?.archived ? '恢复归档环境' : lifecycle?.canDelete ? '永久删除环境' : '归档环境';
  const buttonLabel = action === 'delete' ? '永久删除环境' : action === 'archive' ? '确认归档环境' : '恢复环境';
  return <Modal size="wide" title={title} description={`目标环境：${environment.name}`} onClose={onClose}>
    <div className="modal-body cluster-rollback-preview">
      {busy === 'load' ? <LoadingBlock label="正在核对 Run、构建记录和安装基线…" /> : error && !lifecycle ? <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="环境生命周期操作被阻断" /></> : lifecycle ? <>
        <section className="cluster-rollback-summary"><div><span>Revision</span><strong>{lifecycle.revisionCount}</strong></div><div><span>历史 Run</span><strong>{lifecycle.runCount}</strong></div><div><span>镜像构建</span><strong>{lifecycle.imageBuildCount}</strong></div><div><span>安装基线</span><strong>{lifecycle.installationCount}</strong></div></section>
        {lifecycle.archived ? <div className="warning-callout"><ArchiveRestore size={19} /><div><strong>恢复后可再次执行任务</strong><p>环境会重新出现在组件构建、组件验证和场景运行的目标选择中；历史记录不会改变。</p></div></div> : lifecycle.canDelete ? <div className="warning-callout"><Trash2 size={19} /><div><strong>这是不可恢复的永久删除</strong><p>仅因为该环境从未产生 Run、镜像构建或安装基线才允许删除。Environment Revision 和健康检查会删除，审计记录保留。</p></div></div> : lifecycle.canArchive ? <div className="warning-callout"><Archive size={19} /><div><strong>已有历史证据，只能归档</strong><p>归档不会删除 Run、构建和 Revision；环境将退出所有新任务选择，之后可以恢复。</p></div></div> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>当前不能移除环境</strong><p>{lifecycle.activeRunCount ? `仍有 ${lifecycle.activeRunCount} 个活动 Run；请先等待结束或取消。` : lifecycle.activeImageBuildCount ? `仍有 ${lifecycle.activeImageBuildCount} 个活动镜像构建；请先等待结束。` : `仍有 ${lifecycle.installationCount} 个安装基线；请先手动回滚组件。`}</p></div></div>}
        {error && <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="环境生命周期操作被阻断" /></>}
        {!blocked && <label><span>输入环境名称以确认</span><input aria-label="确认环境名称" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder={environment.name} autoComplete="off" /><small>必须完整输入：{environment.name}</small></label>}
      </> : null}
    </div>
    <footer className="modal-actions"><button className="button button--quiet" disabled={busy !== undefined} onClick={onClose}>取消</button><button className={action === 'delete' ? 'button button--danger' : 'button button--primary'} disabled={blocked || !confirmed || busy !== undefined} onClick={() => onConfirm(action)}>{busy && busy !== 'load' ? '处理中…' : buttonLabel}</button></footer>
  </Modal>;
}

function ClusterRollbackModal({ environment, plan, error, explanation, busy, onScope, onRetry, onClose, onConfirm }: { environment: Environment; plan?: EnvironmentRollbackPlan; error?: string; explanation?: WorkExplanation; busy?: 'preview' | 'submit'; onScope: (nodes?: string[]) => void; onRetry: () => void; onClose: () => void; onConfirm: (confirmation: string) => void }) {
  const [scopeCount, setScopeCount] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const confirmed = confirmation === environment.name;
  return <Modal size="wide" title="手动回滚组件" description={`目标环境：${environment.name}。按备份基线恢复安装前状态或指定版本。`} onClose={onClose}>
    <div className="modal-body cluster-rollback-preview">
      <div className="warning-callout"><AlertTriangle size={19} /><div><strong>回滚会修改目标环境</strong><p>平台将依据已验证安装及未完成操作记录，按依赖逆序执行回滚和回滚后检查；组件已绑定的回滚前检查也会执行。备份基线不完整时拒绝生成计划。</p></div></div>
      {busy === 'preview' ? <LoadingBlock label="正在校验安装来源、备份基线和 Playbook 指纹…" /> : error ? <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="回滚操作被阻断" /></> : plan ? <>
        <section className="cluster-rollback-summary"><div><span>来源 Run</span><strong>{plan.sources.length} 个</strong></div><div><span>组件</span><strong>{plan.componentCount}</strong></div><div><span>回滚节点</span><strong>{plan.nodeCount}</strong></div><div><span>Environment Revision</span><strong>{plan.environmentRevisionId}</strong></div></section>
        <section className="cluster-rollback-sources" aria-label="安装基线来源">{plan.sources.map((source) => <Link key={source.runId} to={`/runs?selected=${source.runId}`}><span>{source.kind}</span><strong>{source.runId}</strong><small>{source.componentCount} 个组件</small></Link>)}</section>
        <label><span>回滚范围</span><select aria-label="回滚节点范围" value={scopeCount} onChange={(event) => setScopeCount(event.target.value)}><option value="">全部待恢复节点</option>{(plan.nodes ?? []).map((_, index) => <option key={index} value={index + 1}>逆序计划的前 {index + 1} 个节点</option>)}</select><small>范围包含所选组件的下游节点，以维持依赖关系。</small></label>
        <div className="form-actions"><button className="button button--quiet" disabled={busy !== undefined} onClick={() => { onScope(scopeCount ? plan.nodes.slice(0, Number(scopeCount)) : undefined); setScopeCount(''); }}>预览所选范围</button></div>
        <JobPlanPreview plan={plan} />
        <label><span>输入环境名称以确认</span><input aria-label="确认回滚环境名称" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder={environment.name} autoComplete="off" /><small>必须完整输入：{environment.name}</small></label>
      </> : null}
    </div>
    <footer className="modal-actions"><button className="button button--quiet" disabled={busy !== undefined} onClick={onClose}>取消</button><button className="button button--danger" disabled={!plan || !confirmed || busy !== undefined} onClick={() => onConfirm(confirmation)}>{busy === 'submit' ? '正在锁定计划…' : '创建回滚 Run（待审批）'}</button></footer>
  </Modal>;
}

function ChangeReasonModal({ title, description, warning, diffLines, busy, onClose, onConfirm }: { title: string; description: string; warning?: string; diffLines: string[]; busy: boolean; onClose: () => void; onConfirm: (reason: string) => void }) {
  const [reason, setReason] = useState('');
  return <Modal title={title} description={description} onClose={onClose}><div className="modal-body revision-preview"><div className="revision-diff"><GitCompare size={18} /><div><strong>变更预览</strong>{diffLines.map((line) => <p key={line}>{line}</p>)}</div></div>{warning && <div className="inline-warning"><AlertTriangle size={17} /><span>{warning}</span></div>}<label><span>变更原因</span><textarea rows={3} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="例如：调整 node-6 地址并补充 FILE_STATION" autoFocus /></label></div><footer className="modal-actions"><button className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy || !reason.trim()} onClick={() => onConfirm(reason.trim())}>{busy ? '处理中…' : '确认创建 Revision'}</button></footer></Modal>;
}

export function environmentDocumentContainsCredentialReferences(document?: EnvironmentExportDocument) {
	return document?.snapshot?.credentialRefs?.some((reference) => typeof reference.reference === 'string' && reference.reference.trim() !== '') ?? false;
}

function EnvironmentImportModal({ environments, selected, onClose, onDone }: { environments: Environment[]; selected?: Environment; onClose: () => void; onDone: (environment: Environment) => void }) {
  const { notify } = useApp();
  const [text, setText] = useState('');
  const [targetKind, setTargetKind] = useState<'new' | 'existing'>(selected ? 'existing' : 'new');
  const [targetEnvironmentId, setTargetEnvironmentId] = useState(selected?.id ?? '');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [reason, setReason] = useState('');
  const [confirmReferences, setConfirmReferences] = useState(false);
  const [plan, setPlan] = useState<EnvironmentImportPlan>();
  const [document, setDocument] = useState<EnvironmentExportDocument>();
  const [busy, setBusy] = useState<'preview' | 'submit'>();

  function request(parsed: EnvironmentExportDocument) {
    return {
      document: parsed,
      target: targetKind === 'new' ? { kind: 'new' as const, name: name.trim(), description: description.trim() } : { kind: 'existing' as const, environmentId: targetEnvironmentId },
      changeReason: reason.trim(),
      confirmCredentialReferences: confirmReferences,
    };
  }

  async function preview() {
    setBusy('preview'); setPlan(undefined);
    try {
      const parsed = JSON.parse(text) as EnvironmentExportDocument;
      if (parsed.formatVersion !== 'clusterforge-environment/v1') throw new Error('仅支持 clusterforge-environment/v1 文件。');
      const result = await api.previewEnvironmentImport(request(parsed));
      setDocument(parsed); setPlan(result);
    } catch (reasonValue) { notify('error', '环境导入预检失败', reasonValue instanceof SyntaxError ? '导入内容不是有效 JSON。' : displayError(reasonValue)); }
    finally { setBusy(undefined); }
  }

  async function submit() {
    if (!plan || !document) return;
    setBusy('submit');
    try {
      const environment = await api.importEnvironment({ ...request(document), expectedPlanDigest: plan.planDigest });
      notify('success', targetKind === 'new' ? '已从文件创建环境' : `已创建 Environment Revision r${plan.nextRevision}`, '运行、健康记录、安装证据和审计历史没有被导入。');
      onDone(environment);
    } catch (reasonValue) { setPlan(undefined); notify('error', '环境导入失败', displayError(reasonValue)); }
    finally { setBusy(undefined); }
  }

	let containsReferences = false;
	try {
		const candidate = document ?? (text.trim() ? JSON.parse(text) as EnvironmentExportDocument : undefined);
		containsReferences = environmentDocumentContainsCredentialReferences(candidate);
	} catch { /* Preview reports malformed JSON. */ }

  return <Modal size="wide" title="导入环境 Revision" description="导入只复用环境配置；不会复制 Run、健康检查、安装记录、备份或审计历史。" onClose={onClose}>
    <div className="modal-body">
	      <label><span>环境导出 JSON</span><input type="file" accept="application/json,.json" onChange={(event) => { const file = event.target.files?.[0]; if (file) void file.text().then((value) => { setText(value); setPlan(undefined); setDocument(undefined); setConfirmReferences(false); }); }} /><textarea className="code-editor" rows={12} value={text} onChange={(event) => { setText(event.target.value); setPlan(undefined); setDocument(undefined); setConfirmReferences(false); }} placeholder="粘贴 clusterforge-environment/v1 文件；缺少的 CredentialRef reference 可直接在此映射或删除。" /></label>
      <label><span>导入目标</span><select value={targetKind} onChange={(event) => { setTargetKind(event.target.value as 'new' | 'existing'); setPlan(undefined); }}><option value="new">创建新环境及 r1</option><option value="existing">既有环境创建新 Revision</option></select></label>
      {targetKind === 'new' ? <div className="form-grid"><label><span>新环境名称</span><input value={name} onChange={(event) => { setName(event.target.value); setPlan(undefined); }} /></label><label><span>说明</span><input value={description} onChange={(event) => { setDescription(event.target.value); setPlan(undefined); }} /></label></div> : <label><span>目标环境</span><select value={targetEnvironmentId} onChange={(event) => { setTargetEnvironmentId(event.target.value); setPlan(undefined); }}><option value="">请选择</option>{environments.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? 1}</option>)}</select></label>}
      <label><span>变更原因</span><textarea rows={2} value={reason} onChange={(event) => { setReason(event.target.value); setPlan(undefined); }} placeholder="说明本次导入用途" /></label>
      {containsReferences ? <label className="checkbox-field"><input type="checkbox" checked={confirmReferences} onChange={(event) => { setConfirmReferences(event.target.checked); setPlan(undefined); }} /><span>我确认复用文件中的 CredentialRef 引用；文件不包含 Secret 实际值</span></label> : null}
      {plan && <section className="revision-preview"><div className="revision-diff"><GitCompare size={18} /><div><strong>导入预览 · 将创建 r{plan.nextRevision}</strong>{plan.changes.map((line) => <p key={line}>{line}</p>)}<p>{plan.hostCount} 台主机 · {plan.variableCount} 个变量 · {plan.parameterCount} 个参数 · {plan.credentialRefCount} 个 CredentialRef</p></div></div>{plan.warnings.map((warning) => <div className="inline-warning" key={warning}><AlertTriangle size={16} /><span>{warning}</span></div>)}</section>}
    </div>
    <footer className="modal-actions"><button className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>取消</button><button className="button button--quiet" disabled={Boolean(busy) || !text.trim() || !reason.trim() || (targetKind === 'new' ? !name.trim() : !targetEnvironmentId)} onClick={() => void preview()}>{busy === 'preview' ? '预检中…' : '预览差异'}</button><button className="button button--primary" disabled={Boolean(busy) || !plan} onClick={() => void submit()}>{busy === 'submit' ? '导入中…' : '确认创建 Revision'}</button></footer>
  </Modal>;
}

function CreateEnvironmentModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify, platformOptionCategories } = useApp();
  const factDimensions = activeEnvironmentConstraintDimensions(platformOptionCategories);
  const [facts, setFacts] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.createEnvironment({
        name: String(form.get('name')), description: String(form.get('description')),
        facts,
      });
      notify('success', '环境已创建', '请继续配置 Inventory 和 CredentialRef。'); onDone();
    } catch (reason) { notify('error', '创建环境失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建共享环境" description="适配标签 · 实际环境来自平台统一目录，每个分类选择一个实际值。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>环境名称</span><input name="name" required placeholder="集群测试环境" /></label><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><EnvironmentFactFields categories={platformOptionCategories} facts={facts} editable onChange={setFacts} /><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy || factDimensions.some((dimension) => platformOptionCategories.find((category) => category.id === dimension.id)?.environmentRequired && !facts[dimension.key])}>{busy ? '创建中…' : '创建环境'}</button></footer></form></Modal>;
}
