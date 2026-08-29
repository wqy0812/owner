import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { Activity, AlertTriangle, Archive, ArchiveRestore, Braces, CheckCircle2, CloudCog, Cpu, Download, GitBranch, GitCompare, HardDrive, History, KeyRound, LockKeyhole, Network, Plus, RotateCcw, Save, Server, Trash2, Upload, UserRound, Wifi } from 'lucide-react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { CatalogRepositoryStatus, CatalogRestorePlan, CredentialRef, Environment, EnvironmentExportDocument, EnvironmentHealthCheck, EnvironmentHost, EnvironmentImportPlan, EnvironmentLifecycle, EnvironmentRevision, EnvironmentRollbackPlan, WorkExplanation } from '../types/domain';

type Tab = 'inventory' | 'facts' | 'variables' | 'credentials';
type EnvironmentVariableRow = { name: string; value: string };

const SCHEDULING_LABELS: Record<string, string> = {
  idle: '调度空闲', queued: '队列等待', awaiting_approval: '等待审批', running: '执行中',
};

function stable(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stable).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.entries(value as Record<string, unknown>).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => `${JSON.stringify(key)}:${stable(item)}`).join(',')}}`;
  return JSON.stringify(value);
}

function schedulingLabel(environment: Environment): string {
  return SCHEDULING_LABELS[environment.schedulingStatus ?? 'idle'] ?? '调度未知';
}

function revisionActor(revision: EnvironmentRevision, users: Array<{ id: string; name: string }>): string {
  return users.find((item) => item.id === revision.createdBy)?.name ?? revision.createdBy ?? '系统初始化';
}

export function EnvironmentsPage() {
  const { user, users, notify, signalRefresh } = useApp();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: environments, loading, error, isRefreshing, reload } = useApiData((signal) => api.environments(signal, user.role === 'environment_owner'), [user.id, user.role], 'environments');
  const { data: runs } = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const { data: workbench } = useApiData((signal) => api.workbench(signal), [user.id], 'workbench');
  const selectedId = searchParams.get('selected') ?? '';
  const selected = useMemo(() => environments?.find((item) => item.id === selectedId) ?? environments?.[0], [environments, selectedId]);
  const environmentWorkItem = workbench?.items.find((item) => item.subject.type === 'environment' && item.subject.id === selected?.id);
  const [tab, setTab] = useState<Tab>('inventory');
  const [hosts, setHosts] = useState<EnvironmentHost[]>([]);
  const [factsText, setFactsText] = useState('{}');
  const [variables, setVariables] = useState<EnvironmentVariableRow[]>([]);
  const [credentials, setCredentials] = useState<CredentialRef[]>([]);
  const [busy, setBusy] = useState(false);
  const [healthBusy, setHealthBusy] = useState(false);
  const [health, setHealth] = useState<EnvironmentHealthCheck>();
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
    setFactsText(JSON.stringify(selected?.currentRevision?.facts ?? {}, null, 2));
    setVariables(Object.entries(selected?.currentRevision?.variables ?? {}).map(([name, value]) => ({ name, value })));
    setCredentials(selected?.currentRevision?.credentialRefs ?? []);
    setHealth(selected?.healthCheck);
  }, [selected?.currentRevision?.id, selected?.healthCheck?.id, selected?.id]);

  useEffect(() => {
    const requested = searchParams.get('tab');
    if (requested === 'inventory' || requested === 'facts' || requested === 'variables' || requested === 'credentials') setTab(requested);
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
  const variablesObject = Object.fromEntries(variables.map((item) => [item.name.trim(), item.value]));
  const dirtyByTab: Record<Tab, boolean> = {
    inventory: stable(hosts) !== stable(baseline?.hosts ?? []),
    facts: factsText !== JSON.stringify(baseline?.facts ?? {}, null, 2),
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
      return [`主机数量：${baseline.hosts.length} → ${hosts.length}`, ...(added.length ? [`新增：${added.join('、')}`] : []), ...(removed.length ? [`移除：${removed.join('、')}`] : []), ...(stable(hosts) !== stable(baseline.hosts) && !added.length && !removed.length ? ['主机地址、分组或连接参数发生修改'] : [])];
    }
    if (tab === 'facts') {
      try {
        const next = JSON.parse(factsText) as Record<string, unknown>;
        const keys = new Set([...Object.keys(baseline.facts), ...Object.keys(next)]);
        return [...keys].filter((key) => stable(baseline.facts[key]) !== stable(next[key])).map((key) => `环境事实 ${key}：${JSON.stringify(baseline.facts[key] ?? '（无）')} → ${JSON.stringify(next[key] ?? '（删除）')}`);
      } catch { return ['环境事实不是有效 JSON，无法生成差异']; }
    }
    if (tab === 'variables') {
      const keys = new Set([...Object.keys(baseline.variables), ...Object.keys(variablesObject)]);
      return [...keys].filter((key) => baseline.variables[key] !== variablesObject[key]).map((key) => `${key}：${baseline.variables[key] ?? '（无）'} → ${variablesObject[key] ?? '（删除）'}`);
    }
    const oldNames = baseline.credentialRefs.map((item) => item.name);
    const newNames = credentials.map((item) => item.name);
    return [`CredentialRef：${oldNames.length} → ${newNames.length}`, `名称：${newNames.join('、') || '（空）'}`];
  }, [baseline, factsText, hosts, tab, variablesObject, credentials]);

  function discardCurrent() {
    if (!baseline) return;
    if (tab === 'inventory') setHosts(baseline.hosts);
    if (tab === 'facts') setFactsText(JSON.stringify(baseline.facts, null, 2));
    if (tab === 'variables') setVariables(Object.entries(baseline.variables).map(([name, value]) => ({ name, value })));
    if (tab === 'credentials') setCredentials(baseline.credentialRefs);
  }

  async function saveCurrent(changeReason: string) {
    if (!selected || !editable || !dirty) return;
    setBusy(true);
    try {
      if (tab === 'inventory') await api.updateInventory(selected.id, hosts, changeReason);
      if (tab === 'facts') await api.updateFacts(selected.id, JSON.parse(factsText) as Record<string, unknown>, changeReason);
      if (tab === 'variables') {
        const names = variables.map((item) => item.name.trim());
        if (names.some((name) => !/^[A-Z_][A-Z0-9_]*$/.test(name))) throw new Error('环境变量名必须是大写标识符。');
        if (new Set(names).size !== names.length) throw new Error('环境变量名不能重复。');
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
    try {
      const result = await api.checkEnvironmentHealth(selected.id);
      setHealth(result);
      notify(result.status === 'healthy' ? 'success' : 'error', result.status === 'healthy' ? '环境检查通过' : '环境检查发现异常', `${result.results.filter((item) => item.reachable).length}/${result.results.length} 个端点可达。`);
      signalRefresh(['environments', 'workbench']);
    } catch (reason) { notify('error', '环境检查失败', displayError(reason)); } finally { setHealthBusy(false); }
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

  async function previewClusterRollback() {
    if (!selected || !editable) return;
    setRollbackOpen(true);
    setRollbackPlan(undefined);
    setRollbackError(undefined);
    setRollbackExplanation(undefined);
    setRollbackBusy('preview');
    try {
      setRollbackPlan(await api.previewEnvironmentRollback(selected.id));
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
      const run = await api.startEnvironmentRollback(selected.id, { expectedPlanDigest: rollbackPlan.planDigest, confirmEnvironmentName });
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

  function updateHost(index: number, patch: Partial<EnvironmentHost>) {
    setHosts((items) => items.map((host, i) => i === index ? { ...host, ...patch } : host));
  }

  const environmentRuns = runs?.filter((run) => run.environmentId === selected?.id).slice(0, 4) ?? [];
  const facts = selected?.currentRevision?.facts ?? {};
  const healthStale = Boolean(health && health.environmentRevisionId !== selected?.currentRevision?.id);

  return <div className="page">
    <PageHeader eyebrow="Execution environments" title="环境管理" description="分别查看调度占用与真实连通性；配置变更以可追溯 Revision 保存。" actions={user.role === 'environment_owner' ? <><button className="button button--quiet" onClick={() => setImportOpen(true)}><Upload size={16} /> 导入环境</button><button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建环境</button></> : undefined} />
    {user.role === 'environment_owner' && <CatalogRepositoryPanel />}
    <RefreshNotice loading={isRefreshing} error={environments ? error : undefined} onRetry={() => void reload()} />
    {loading && !environments ? <LoadingBlock label="正在读取共享环境…" /> : error && !environments ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="catalog-layout">
      <aside className="catalog-list panel">
        <div className="catalog-list__header"><strong>共享环境</strong><span>{environments?.length ?? 0}</span></div>
        {environments?.map((environment) => <button key={environment.id} className={`catalog-item${selected?.id === environment.id ? ' active' : ''}`} onClick={() => selectEnvironment(environment.id)}><span className="catalog-item__icon catalog-item__icon--cyan"><CloudCog size={18} /></span><span><strong>{environment.name}</strong><small>r{environment.currentRevision?.revision ?? 1} · {environment.currentRevision?.hosts?.length ?? 0} 台主机</small></span><StatusPill status={environment.archivedAt ? 'offline' : environment.schedulingStatus ?? 'idle'}>{environment.archivedAt ? '已归档' : schedulingLabel(environment)}</StatusPill></button>)}
      </aside>
      {selected ? <section className="detail-stack">
        <article className="panel environment-hero">
          <div><span className="environment-icon"><CloudCog size={25} /></span><div><div className="eyebrow">Environment revision {selected.currentRevision?.revision ?? 1}</div><h2>{selected.name}</h2><p>{selected.description ?? '用于平台组件与场景测试的共享环境'}</p></div></div>
          <div className="environment-hero__actions"><div className="environment-owner"><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}<StatusPill status={selected.archivedAt ? 'offline' : selected.schedulingStatus ?? 'idle'}>{selected.archivedAt ? '已归档' : schedulingLabel(selected)}</StatusPill></div>{ownsSelected && <button className={selected.archivedAt ? 'button button--secondary' : 'button button--danger-soft'} disabled={anyDirty || lifecycleBusy !== undefined} onClick={() => void openLifecycle()}>{selected.archivedAt ? <ArchiveRestore size={15} /> : <Archive size={15} />} {selected.archivedAt ? '恢复环境' : '移除环境'}</button>}{editable && selected.currentRevision && <><button className="button button--quiet" onClick={() => void exportRevision(selected.currentRevision!, false)}><Download size={15} /> 安全导出</button><button className="button button--quiet" onClick={() => void exportRevision(selected.currentRevision!, true)}><KeyRound size={15} /> 导出含引用</button><button className="button button--danger" disabled={(selected.schedulingStatus ?? 'idle') !== 'idle' || anyDirty || rollbackBusy !== undefined} title={(selected.schedulingStatus ?? 'idle') !== 'idle' ? '请先处理当前活动 Run' : anyDirty ? '请先保存或放弃环境配置更改' : undefined} onClick={() => void previewClusterRollback()}><RotateCcw size={15} /> 一键回滚至干净状态</button></>}</div>
        </article>
        {selected.archivedAt && <div className="inline-warning"><Archive size={17} /><span>该环境已归档，仅保留配置和历史证据；不能创建 Revision、健康检查、构建或 Run。需要再次使用时先恢复环境。</span></div>}
        <StatusExplanationPanel item={environmentWorkItem} />
        <section className="fact-grid">
          <article><Cpu size={18} /><span>架构</span><strong>{String(facts.architecture ?? 'amd64')}</strong></article>
          <article><HardDrive size={18} /><span>操作系统</span><strong>{String(facts.operatingSystem ?? 'Kylin')}</strong></article>
          <article><HardDrive size={18} /><span>系统版本</span><strong>{String(facts.operatingSystemVersion ?? '—')}</strong></article>
          <article><Server size={18} /><span>Docker</span><strong>{String(facts.dockerVersion ?? '—')}</strong></article>
          <article><Network size={18} /><span>网络栈</span><strong>{String(facts.ipFamily ?? 'IPv4')}</strong></article>
          <article><Server size={18} /><span>主机</span><strong>{hosts.length}</strong></article>
        </section>

        <article className="panel health-panel" id="environment-health">
          <header className="panel__header"><div><span className={`panel__icon ${health?.status === 'degraded' ? 'panel__icon--rose' : 'panel__icon--cyan'}`}><Activity size={18} /></span><div><h2>环境连通性</h2><p>TCP 只读检查主机 SSH、IMAGE_REGISTRY 和 FILE_STATION，不执行安装。</p></div></div>{editable && <button className="button button--secondary" disabled={healthBusy} onClick={() => void checkHealth()}><Wifi size={15} /> {healthBusy ? '检查中…' : '立即检查'}</button>}</header>
          {!health ? <EmptyState title="尚未检查" description="“调度空闲”不代表主机或依赖端点真实可达。" /> : <div className="health-results">
            <div className={`health-summary health-summary--${health.status}`}>{health.status === 'healthy' ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}<div><strong>{health.status === 'healthy' ? '全部端点可达' : '存在不可达端点'}</strong><small>{formatTime(health.checkedAt)} · {health.results.filter((item) => item.reachable).length}/{health.results.length} 通过{healthStale ? ' · 检查基于旧 Revision，请重新检查' : ''}</small></div></div>
            {health.results.map((item) => <div className="health-result" key={`${item.kind}-${item.name}`}><span className={item.reachable ? 'health-dot health-dot--ok' : 'health-dot health-dot--failed'} /><div><strong>{item.name}</strong><small>{item.address}</small></div><span>{item.reachable ? `${item.latencyMs} ms` : item.error ?? '不可达'}</span></div>)}
          </div>}
        </article>

        <article className="panel environment-editor">
          <div className="tabs" role="tablist">{([
            ['inventory', 'Inventory', <Server size={16} />], ['facts', '环境事实', <Cpu size={16} />], ['variables', '环境变量', <Braces size={16} />], ['credentials', '凭据引用', <KeyRound size={16} />],
          ] as const).map(([value, label, icon]) => <button key={value} role="tab" aria-selected={tab === value} className={tab === value ? 'active' : ''} onClick={() => setTab(value)}>{icon}{label}{dirtyByTab[value] && <span className="dirty-dot" aria-label="有未保存更改" />}</button>)}</div>
          {tab === 'inventory' && <div className="editor-section"><div className="section-title"><div><h3>主机与分组</h3><p>保存后创建新的 Environment Revision。</p></div>{editable && <button className="button button--quiet" onClick={() => setHosts((items) => [...items, { name: '', address: '', groups: ['all'], port: 22, user: 'root' }])}><Plus size={15} /> 添加主机</button>}</div><div className="host-table"><div className="host-table__head"><span>主机名</span><span>地址</span><span>主机组</span><span>SSH 用户 / 端口</span><span /></div>{hosts.map((host, index) => <div className="host-row" key={`${host.name}-${index}`}><input aria-label={`主机 ${index + 1} 名称`} value={host.name} disabled={!editable} onChange={(event) => updateHost(index, { name: event.target.value })} /><input aria-label={`主机 ${index + 1} 地址`} value={host.address} disabled={!editable} onChange={(event) => updateHost(index, { address: event.target.value })} /><input aria-label={`主机 ${index + 1} 分组`} value={host.groups.join(', ')} disabled={!editable} onChange={(event) => updateHost(index, { groups: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} /><div className="split-input"><input aria-label={`主机 ${index + 1} SSH 用户`} value={host.user ?? ''} disabled={!editable} onChange={(event) => updateHost(index, { user: event.target.value })} /><input aria-label={`主机 ${index + 1} SSH 端口`} type="number" value={host.port ?? 22} disabled={!editable} onChange={(event) => updateHost(index, { port: Number(event.target.value) })} /></div>{editable && <button className="icon-button icon-button--danger" aria-label={`移除主机 ${host.name || index + 1}，保存后生效`} onClick={() => setHosts((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div>{!hosts.length && <EmptyState title="没有主机" description={editable ? '添加一台测试主机。' : '环境 Owner 尚未配置 Inventory。'} />}</div>}
          {tab === 'facts' && <div className="editor-section"><div className="section-title"><div><h3>环境事实</h3><p>架构、操作系统和网络栈会参与组件兼容性预检。</p></div></div><textarea aria-label="环境事实 JSON" className="code-editor" value={factsText} disabled={!editable} onChange={(event) => setFactsText(event.target.value)} spellCheck={false} /></div>}
          {tab === 'variables' && <div className="editor-section"><div className="section-title"><div><h3>组件作业环境变量</h3><p>IMAGE_REGISTRY 指定镜像仓库，FILE_STATION 指定组件介质站，均填写 host:port。</p></div>{editable && <button className="button button--quiet" onClick={() => setVariables((items) => [...items, { name: '', value: '' }])}><Plus size={15} /> 添加变量</button>}</div><div className="environment-variable-list">{variables.map((variable, index) => <div key={index} id={variable.name ? `environment-variable-${variable.name}` : undefined}><span className="variable-icon"><Braces size={17} /></span><input aria-label="环境变量名" value={variable.name} disabled={!editable} placeholder="IMAGE_REGISTRY" onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value.toUpperCase() } : item))} /><input aria-label={`环境变量 ${variable.name || index} 的值`} value={variable.value} disabled={!editable} placeholder={variable.name === 'IMAGE_REGISTRY' ? '192.168.88.54:5000' : variable.name === 'FILE_STATION' ? '192.168.88.57:8080' : '非敏感字符串值'} onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" aria-label={`移除环境变量 ${variable.name || index}，保存后生效`} onClick={() => setVariables((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div>{!variables.length && <EmptyState title="尚未配置环境变量" description={editable ? '至少添加 IMAGE_REGISTRY 和 FILE_STATION，分别用于镜像和组件介质。' : '环境 Owner 尚未配置环境变量。'} />}</div>}
          {tab === 'credentials' && <div className="editor-section"><div className="section-title"><div><h3>CredentialRef</h3><p>数据库和 API 只保存引用，其他角色只看到脱敏值。</p></div>{editable && <button className="button button--quiet" onClick={() => setCredentials((items) => [...items, { name: '', type: 'envVarRef', reference: '' }])}><Plus size={15} /> 添加引用</button>}</div><div className="credential-list">{credentials.map((credential, index) => <div key={`${credential.name}-${index}`}><span className="credential-icon"><LockKeyhole size={17} /></span><input aria-label="凭据名称" value={credential.name} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} /><select aria-label={`凭据 ${credential.name || index} 类型`} value={credential.type} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, type: event.target.value as CredentialRef['type'] } : item))}><option value="envVarRef">envVarRef</option><option value="sshKeyPath">sshKeyPath</option></select><input aria-label={`凭据 ${credential.name || index} 引用`} value={editable ? credential.reference ?? '' : credential.maskedReference ?? '••••••••'} disabled={!editable} placeholder={credential.type === 'envVarRef' ? 'ANSIBLE_SSH_KEY' : '/path/to/key'} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, reference: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" aria-label={`移除凭据 ${credential.name || index}，保存后生效`} onClick={() => setCredentials((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div></div>}
          {editable && <footer className="editor-footer"><span className={dirty ? 'editor-dirty' : ''}><LockKeyhole size={14} /> {dirty ? '当前页有未保存更改' : '当前页与已保存 Revision 一致'}</span><div>{dirty && <button className="button button--quiet" onClick={discardCurrent}>放弃本页更改</button>}<button className="button button--primary" disabled={busy || !dirty} onClick={() => setSaveOpen(true)}><Save size={16} /> 保存新 Revision</button></div></footer>}
        </article>

        <article className="panel revision-history"><header className="panel__header"><div><span className="panel__icon"><History size={18} /></span><div><h2>Revision 历史</h2><p>恢复旧配置时始终创建新 Revision，不覆盖历史。</p></div></div></header><div className="revision-list">{(selected.revisions ?? (selected.currentRevision ? [selected.currentRevision] : [])).map((revision) => <div key={revision.id}><span className="revision-number">r{revision.revision}</span><div><strong>{revision.changeReason || (revision.revision === 1 ? '创建环境' : '未填写变更原因')}</strong><small>{revisionActor(revision, users)} · {formatTime(revision.createdAt)}</small></div>{editable && <button className="button button--quiet" onClick={() => void exportRevision(revision, false)}><Download size={14} /> 导出</button>}{revision.id === selected.currentRevision?.id ? <StatusPill status="active">当前</StatusPill> : editable && <button className="button button--quiet" disabled={anyDirty} onClick={() => setRestoreRevision(revision)}><RotateCcw size={14} /> 基于此恢复</button>}</div>)}</div></article>

        <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><LockKeyhole size={18} /></span><div><h2>最近环境运行</h2><p>同一环境一次只允许一个活动 Run</p></div></div></header>{environmentRuns.length ? <div className="simple-table">{environmentRuns.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`}><div><strong>{run.name ?? run.scenarioName ?? run.componentName}</strong><small>{formatTime(run.createdAt)}</small></div><StatusPill status={run.status} /></Link>)}</div> : <EmptyState title="暂无运行记录" />}</article>
      </section> : <section className="panel"><EmptyState title="没有可见环境" /></section>}
    </div>}
    {createOpen && <CreateEnvironmentModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('environments'); }} />}
    {importOpen && <EnvironmentImportModal environments={(environments ?? []).filter((environment) => !environment.archivedAt)} selected={selected?.archivedAt ? undefined : selected} onClose={() => setImportOpen(false)} onDone={(environment) => { setImportOpen(false); signalRefresh(['environments', 'workbench']); setSearchParams({ selected: environment.id }); }} />}
    {saveOpen && selected && <ChangeReasonModal title="保存为新 Revision" description={`r${selected.currentRevision?.revision ?? 0} → r${(selected.currentRevision?.revision ?? 0) + 1}`} busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 仍锁定旧 Revision。` : undefined} diffLines={diffLines} onClose={() => setSaveOpen(false)} onConfirm={(reason) => void saveCurrent(reason)} />}
    {restoreRevision && selected && <ChangeReasonModal title={`基于 r${restoreRevision.revision} 恢复`} description="将复制该历史快照并创建新的当前 Revision。" busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 不会被修改。` : undefined} diffLines={[`目标快照：r${restoreRevision.revision}`, `主机 ${restoreRevision.hosts.length} 台 · 环境变量 ${Object.keys(restoreRevision.variables).length} 个 · CredentialRef ${restoreRevision.credentialRefs.length} 个`]} onClose={() => setRestoreRevision(undefined)} onConfirm={(reason) => void restore(reason)} />}
    {rollbackOpen && selected && <ClusterRollbackModal environment={selected} plan={rollbackPlan} error={rollbackError} explanation={rollbackExplanation} busy={rollbackBusy} onRetry={() => void previewClusterRollback()} onClose={() => { if (!rollbackBusy) { setRollbackOpen(false); setRollbackPlan(undefined); setRollbackError(undefined); setRollbackExplanation(undefined); } }} onConfirm={(confirmation) => void submitClusterRollback(confirmation)} />}
    {lifecycleOpen && selected && <EnvironmentLifecycleModal environment={selected} lifecycle={lifecycle} error={lifecycleError} explanation={lifecycleExplanation} busy={lifecycleBusy} onRetry={() => void openLifecycle()} onClose={() => { if (!lifecycleBusy) setLifecycleOpen(false); }} onConfirm={(action) => void applyLifecycle(action)} />}
  </div>;
}

function CatalogRepositoryPanel() {
  const { notify, signalRefresh } = useApp();
  const { data: repository, loading, error, isRefreshing, reload } = useApiData<CatalogRepositoryStatus>((signal) => api.catalogRepository(signal), [], 'catalog-repository');
  const [repositoryMode, setRepositoryMode] = useState<'create' | 'connect'>();
  const [restoreOpen, setRestoreOpen] = useState(false);

  function updated(status: CatalogRepositoryStatus, message: string) {
    notify('success', message, `${status.path} · ${status.branch}`);
    setRepositoryMode(undefined);
    signalRefresh(['catalog-repository', 'workbench']);
    void reload();
  }

  return <article className="panel" id="catalog-repository">
    <header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>发布目录灾备</h2><p>发布后自动复制到本地私有 Git 仓库；恢复只允许组件和场景均为空的数据库。</p></div></div><div className="environment-hero__actions"><button className="button button--quiet" disabled={!repository} onClick={() => setRepositoryMode('connect')}>接入已有仓库</button><button className="button button--secondary" disabled={!repository} onClick={() => setRepositoryMode('create')}><Plus size={15} /> 创建私有仓库</button></div></header>
    <RefreshNotice loading={Boolean(isRefreshing)} error={repository ? error : undefined} onRetry={reload} />
    {loading && !repository ? <LoadingBlock label="正在读取私有仓库…" /> : error && !repository ? <ErrorBlock message={error} onRetry={reload} /> : repository ? <div className="revision-preview">
      <div className="revision-diff"><GitBranch size={18} /><div><strong>{repository.configured ? '已接入私有仓库' : '尚未配置私有仓库'}</strong><p>{repository.configured ? repository.path : `允许根目录：${repository.allowedRoot}`}</p>{repository.configured && <p>分支：{repository.branch} · 恢复点：{repository.recoveryPoints.length} 个 · 六小时任务：{repository.timerEnabled ? '已启用' : '已停止'}</p>}</div></div>
      {!repository.configured && <div className="inline-warning"><AlertTriangle size={16} /><span>未通过前台选择仓库，六小时定时备份保持停止。</span></div>}
      {repository.configured && repository.behind && <div className="inline-warning"><AlertTriangle size={16} /><span>恢复点落后：当前发布代次 {repository.currentGeneration}，已备份代次 {repository.backedUpGeneration}。</span></div>}
      {repository.lastError && <div className="inline-warning"><AlertTriangle size={16} /><span>最近备份异常：{repository.lastError}</span></div>}
      {repository.configured && <div className="modal-actions"><button className="button button--danger" disabled={!repository.recoveryPoints.length} onClick={() => setRestoreOpen(true)}><RotateCcw size={15} /> 从 Git 恢复空库</button></div>}
    </div> : null}
    {repositoryMode && repository && <CatalogRepositoryModal mode={repositoryMode} allowedRoot={repository.allowedRoot} onClose={() => setRepositoryMode(undefined)} onDone={updated} />}
    {restoreOpen && repository && <CatalogRestoreModal repository={repository} onClose={() => setRestoreOpen(false)} onDone={() => { setRestoreOpen(false); notify('success', '发布目录已从 Git 恢复', '组件、场景和 Playbook 已写入空数据库，并已触发恢复后快照。'); signalRefresh(['catalog-repository', 'components', 'scenarios', 'workbench']); void reload(); }} />}
  </article>;
}

function CatalogRepositoryModal({ mode, allowedRoot, onClose, onDone }: { mode: 'create' | 'connect'; allowedRoot: string; onClose: () => void; onDone: (status: CatalogRepositoryStatus, message: string) => void }) {
  const { notify } = useApp();
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true);
    try {
      const status = mode === 'create' ? await api.createCatalogRepository(path.trim()) : await api.connectCatalogRepository(path.trim());
      onDone(status, mode === 'create' ? '私有仓库已创建' : '已有仓库已接入');
    } catch (reason) { notify('error', mode === 'create' ? '创建私有仓库失败' : '接入私有仓库失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  return <Modal title={mode === 'create' ? '创建私有 Catalog 仓库' : '接入已有 Catalog 仓库'} description={`路径必须位于 ${allowedRoot} 内；相对路径会基于该目录解析。`} onClose={onClose}>
    <form onSubmit={(event) => void submit(event)}><div className="modal-body"><label><span>服务器仓库路径</span><input aria-label="服务器仓库路径" value={path} onChange={(event) => setPath(event.target.value)} placeholder={mode === 'create' ? 'catalog-production.git' : '/data/private-catalog-repositories/catalog-production.git'} autoFocus required /><small>{mode === 'create' ? '目标必须不存在；平台将创建权限为 0700 的 bare Git 仓库。' : `已有仓库必须包含 catalog 分支，且路径位于 ${allowedRoot}。`}</small></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" disabled={busy} onClick={onClose}>取消</button><button className="button button--primary" disabled={busy || !path.trim()}>{busy ? '处理中…' : mode === 'create' ? '创建并接入' : '验证并接入'}</button></footer></form>
  </Modal>;
}

function CatalogRestoreModal({ repository, onClose, onDone }: { repository: CatalogRepositoryStatus; onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [ref, setRef] = useState(repository.recoveryPoints[0]?.ref ?? '');
  const [plan, setPlan] = useState<CatalogRestorePlan>();
  const [confirmation, setConfirmation] = useState('');
  const [busy, setBusy] = useState<'preview' | 'restore'>();
  async function preview() {
    setBusy('preview'); setPlan(undefined); setConfirmation('');
    try { setPlan(await api.previewCatalogRestore(ref)); }
    catch (reason) { notify('error', '恢复预检失败', displayError(reason)); }
    finally { setBusy(undefined); }
  }
  async function restoreCatalog() {
    if (!plan) return; setBusy('restore');
    try { await api.restoreCatalog({ ref, expectedPlanDigest: plan.planDigest, confirmation }); onDone(); }
    catch (reason) { setPlan(undefined); notify('error', 'Git 恢复失败', displayError(reason)); }
    finally { setBusy(undefined); }
  }
  return <Modal size="wide" title="从 Git 恢复发布目录" description="仅在当前数据库没有任何组件和场景时允许恢复；操作不会恢复 Run、环境、审批或审计历史。" onClose={onClose}>
    <div className="modal-body cluster-rollback-preview"><div className="warning-callout"><AlertTriangle size={19} /><div><strong>冲突时整批失败</strong><p>组件或场景非空、ID/slug 冲突、用户身份属性冲突、Playbook 路径内容不同，都会拒绝恢复且不留下部分数据。</p></div></div>
      <label><span>Git 恢复点</span><select aria-label="Git 恢复点" value={ref} onChange={(event) => { setRef(event.target.value); setPlan(undefined); setConfirmation(''); }}>{repository.recoveryPoints.map((point) => <option key={point.ref} value={point.ref}>{formatTime(point.createdAt)} · {point.ref}</option>)}</select></label>
      {plan && <><section className="cluster-rollback-summary"><div><span>组件</span><strong>{plan.counts.components ?? 0}</strong></div><div><span>Release</span><strong>{plan.counts.component_releases ?? 0}</strong></div><div><span>场景</span><strong>{plan.counts.scenarios ?? 0}</strong></div><div><span>Playbook</span><strong>{plan.playbookCount}</strong></div></section><div className="revision-diff"><GitCompare size={18} /><div><strong>恢复计划已锁定</strong><p>Commit：{plan.gitCommit.slice(0, 16)}…</p><p>Catalog：{plan.catalogSha256.slice(0, 16)}… · Schema：{plan.schemaContract}</p></div></div><label><span>输入“恢复发布目录”以确认</span><input aria-label="确认恢复发布目录" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="off" /><small>恢复前会重新核对空库状态和计划摘要。</small></label></>}
    </div><footer className="modal-actions"><button className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>取消</button><button className="button button--quiet" disabled={Boolean(busy) || !ref} onClick={() => void preview()}>{busy === 'preview' ? '预检中…' : '预览恢复'}</button><button className="button button--danger" disabled={Boolean(busy) || !plan || confirmation !== '恢复发布目录'} onClick={() => void restoreCatalog()}>{busy === 'restore' ? '恢复中…' : '确认恢复空库'}</button></footer>
  </Modal>;
}

function EnvironmentLifecycleModal({ environment, lifecycle, error, explanation, busy, onRetry, onClose, onConfirm }: { environment: Environment; lifecycle?: EnvironmentLifecycle; error?: string; explanation?: WorkExplanation; busy?: 'load' | 'delete' | 'archive' | 'unarchive'; onRetry: () => void; onClose: () => void; onConfirm: (action: 'delete' | 'archive' | 'unarchive') => void }) {
  const [confirmation, setConfirmation] = useState('');
  const confirmed = confirmation === environment.name;
  const action = lifecycle?.archived ? 'unarchive' : lifecycle?.canDelete ? 'delete' : 'archive';
  const blocked = !lifecycle || (!lifecycle.archived && !lifecycle.canDelete && !lifecycle.canArchive);
  const title = lifecycle?.archived ? '恢复归档环境' : lifecycle?.canDelete ? '永久删除环境' : '归档环境';
  const buttonLabel = action === 'delete' ? '永久删除环境' : action === 'archive' ? '确认归档环境' : '恢复环境';
  return <Modal title={title} description={`目标环境：${environment.name}`} onClose={onClose}>
    <div className="modal-body cluster-rollback-preview">
      {busy === 'load' ? <LoadingBlock label="正在核对 Run、构建记录和安装基线…" /> : error && !lifecycle ? <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="环境生命周期操作被阻断" /></> : lifecycle ? <>
        <section className="cluster-rollback-summary"><div><span>Revision</span><strong>{lifecycle.revisionCount}</strong></div><div><span>历史 Run</span><strong>{lifecycle.runCount}</strong></div><div><span>镜像构建</span><strong>{lifecycle.imageBuildCount}</strong></div><div><span>安装基线</span><strong>{lifecycle.installationCount}</strong></div></section>
        {lifecycle.archived ? <div className="warning-callout"><ArchiveRestore size={19} /><div><strong>恢复后可再次执行任务</strong><p>环境会重新出现在组件构建、组件验证和场景运行的目标选择中；历史记录不会改变。</p></div></div> : lifecycle.canDelete ? <div className="warning-callout"><Trash2 size={19} /><div><strong>这是不可恢复的永久删除</strong><p>仅因为该环境从未产生 Run、镜像构建或安装基线才允许删除。Environment Revision 和健康检查会删除，审计记录保留。</p></div></div> : lifecycle.canArchive ? <div className="warning-callout"><Archive size={19} /><div><strong>已有历史证据，只能归档</strong><p>归档不会删除 Run、构建和 Revision；环境将退出所有新任务选择，之后可以恢复。</p></div></div> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>当前不能移除环境</strong><p>{lifecycle.activeRunCount ? `仍有 ${lifecycle.activeRunCount} 个活动 Run；请先等待结束或取消。` : lifecycle.activeImageBuildCount ? `仍有 ${lifecycle.activeImageBuildCount} 个活动镜像构建；请先等待结束。` : `仍有 ${lifecycle.installationCount} 个安装基线；请先一键回滚至干净状态。`}</p></div></div>}
        {error && <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="环境生命周期操作被阻断" /></>}
        {!blocked && <label><span>输入环境名称以确认</span><input aria-label="确认环境名称" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder={environment.name} autoComplete="off" /><small>必须完整输入：{environment.name}</small></label>}
      </> : null}
    </div>
    <footer className="modal-actions"><button className="button button--quiet" disabled={busy !== undefined} onClick={onClose}>取消</button><button className={action === 'delete' ? 'button button--danger' : 'button button--primary'} disabled={blocked || !confirmed || busy !== undefined} onClick={() => onConfirm(action)}>{busy && busy !== 'load' ? '处理中…' : buttonLabel}</button></footer>
  </Modal>;
}

function ClusterRollbackModal({ environment, plan, error, explanation, busy, onRetry, onClose, onConfirm }: { environment: Environment; plan?: EnvironmentRollbackPlan; error?: string; explanation?: WorkExplanation; busy?: 'preview' | 'submit'; onRetry: () => void; onClose: () => void; onConfirm: (confirmation: string) => void }) {
  const [confirmation, setConfirmation] = useState('');
  const confirmed = confirmation === environment.name;
  return <Modal title="一键回滚整个集群" description={`目标环境：${environment.name}。计划只允许恢复为安装前的干净状态。`} onClose={onClose}>
    <div className="modal-body cluster-rollback-preview">
      <div className="warning-callout"><AlertTriangle size={19} /><div><strong>这是整集群破坏性操作</strong><p>平台将依据当前安装清单，按来源 Run 时间倒序、每个来源内部安装步骤逆序执行 rollback。任一基线不完整都会拒绝生成计划。</p></div></div>
      {busy === 'preview' ? <LoadingBlock label="正在校验安装来源、备份基线和 Playbook 指纹…" /> : error ? <><ErrorBlock message={error} onRetry={onRetry} /><StatusExplanationPanel explanation={explanation} title="回滚操作被阻断" /></> : plan ? <>
        <section className="cluster-rollback-summary"><div><span>来源 Run</span><strong>{plan.sources.length} 个</strong></div><div><span>组件</span><strong>{plan.componentCount}</strong></div><div><span>回滚节点</span><strong>{plan.nodeCount}</strong></div><div><span>Environment Revision</span><strong>{plan.environmentRevisionId}</strong></div></section>
        <section className="cluster-rollback-sources" aria-label="安装基线来源">{plan.sources.map((source) => <Link key={source.runId} to={`/runs?selected=${source.runId}`}><span>{source.kind}</span><strong>{source.runId}</strong><small>{source.componentCount} 个组件</small></Link>)}</section>
        <section className="test-plan-preview" aria-label="整集群回滚计划"><header><div><strong>逆序执行计划</strong><small>摘要 {plan.planDigest.slice(0, 16)}…</small></div><StatusPill status="awaiting_approval">提交后待审批</StatusPill></header><div>{plan.steps.map((step) => <article key={`${step.order}-${step.componentId}-${step.limit}`}><span>{step.order}</span><div><strong>{step.componentName} · rollback</strong><p>{step.releaseVersion} · 目标 {step.limit || 'all'}</p><small>基线 Run {step.backupInstallRunId} · {step.backupRef}</small></div></article>)}</div></section>
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
      {plan && <section className="revision-preview"><div className="revision-diff"><GitCompare size={18} /><div><strong>导入预览 · 将创建 r{plan.nextRevision}</strong>{plan.changes.map((line) => <p key={line}>{line}</p>)}<p>{plan.hostCount} 台主机 · {plan.variableCount} 个变量 · {plan.credentialRefCount} 个 CredentialRef</p></div></div>{plan.warnings.map((warning) => <div className="inline-warning" key={warning}><AlertTriangle size={16} /><span>{warning}</span></div>)}</section>}
    </div>
    <footer className="modal-actions"><button className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>取消</button><button className="button button--quiet" disabled={Boolean(busy) || !text.trim() || !reason.trim() || (targetKind === 'new' ? !name.trim() : !targetEnvironmentId)} onClick={() => void preview()}>{busy === 'preview' ? '预检中…' : '预览差异'}</button><button className="button button--primary" disabled={Boolean(busy) || !plan} onClick={() => void submit()}>{busy === 'submit' ? '导入中…' : '确认创建 Revision'}</button></footer>
  </Modal>;
}

function CreateEnvironmentModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.createEnvironment({
        name: String(form.get('name')), description: String(form.get('description')),
        facts: { architecture: String(form.get('architecture')), operatingSystem: String(form.get('operatingSystem')), operatingSystemVersion: String(form.get('operatingSystemVersion')), dockerVersion: String(form.get('dockerVersion')), ipFamily: String(form.get('ipFamily')) },
      });
      notify('success', '环境已创建', '请继续配置 Inventory 和 CredentialRef。'); onDone();
    } catch (reason) { notify('error', '创建环境失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建共享环境" description="环境由当前 Environment Owner 管理，其他 Owner 可用它运行测试。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>环境名称</span><input name="name" required placeholder="集群测试环境" /></label><label><span>架构</span><select name="architecture" defaultValue="amd64" required><option value="amd64">amd64</option><option value="arm64">arm64</option></select></label><label><span>操作系统</span><select name="operatingSystem" defaultValue="Ubuntu" required><option value="Ubuntu">Ubuntu</option><option value="Kylin">Kylin</option><option value="SUSE">SUSE</option></select></label><label><span>操作系统版本</span><input name="operatingSystemVersion" defaultValue="18.04" required /></label><label><span>Docker 版本</span><input name="dockerVersion" defaultValue="20.10.21" required /></label><label><span>网络栈</span><select name="ipFamily" defaultValue="IPv4" required><option value="IPv4">IPv4</option><option value="IPv6">IPv6</option></select></label><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建环境'}</button></footer></form></Modal>;
}
