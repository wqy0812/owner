import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { Activity, AlertTriangle, Braces, CheckCircle2, CloudCog, Cpu, GitCompare, HardDrive, History, KeyRound, LockKeyhole, Network, Plus, RotateCcw, Save, Server, Trash2, UserRound, Wifi } from 'lucide-react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { actionableExplanation, api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { CredentialRef, Environment, EnvironmentHealthCheck, EnvironmentHost, EnvironmentRevision, EnvironmentRollbackPlan, WorkExplanation } from '../types/domain';

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
  const { data: environments, loading, error, isRefreshing, reload } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
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
  const [saveOpen, setSaveOpen] = useState(false);
  const [restoreRevision, setRestoreRevision] = useState<EnvironmentRevision>();
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackPlan, setRollbackPlan] = useState<EnvironmentRollbackPlan>();
  const [rollbackError, setRollbackError] = useState<string>();
  const [rollbackExplanation, setRollbackExplanation] = useState<WorkExplanation>();
  const [rollbackBusy, setRollbackBusy] = useState<'preview' | 'submit'>();
  const handledDeepLink = useRef<string>();
  const editable = user.role === 'environment_owner' && selected?.ownerId === user.id;

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
    <PageHeader eyebrow="Execution environments" title="环境管理" description="分别查看调度占用与真实连通性；配置变更以可追溯 Revision 保存。" actions={user.role === 'environment_owner' ? <button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建环境</button> : undefined} />
    <RefreshNotice loading={isRefreshing} error={environments ? error : undefined} onRetry={() => void reload()} />
    {loading && !environments ? <LoadingBlock label="正在读取共享环境…" /> : error && !environments ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="catalog-layout">
      <aside className="catalog-list panel">
        <div className="catalog-list__header"><strong>共享环境</strong><span>{environments?.length ?? 0}</span></div>
        {environments?.map((environment) => <button key={environment.id} className={`catalog-item${selected?.id === environment.id ? ' active' : ''}`} onClick={() => selectEnvironment(environment.id)}><span className="catalog-item__icon catalog-item__icon--cyan"><CloudCog size={18} /></span><span><strong>{environment.name}</strong><small>r{environment.currentRevision?.revision ?? 1} · {environment.currentRevision?.hosts?.length ?? 0} 台主机</small></span><StatusPill status={environment.schedulingStatus ?? 'idle'}>{schedulingLabel(environment)}</StatusPill></button>)}
      </aside>
      {selected ? <section className="detail-stack">
        <article className="panel environment-hero">
          <div><span className="environment-icon"><CloudCog size={25} /></span><div><div className="eyebrow">Environment revision {selected.currentRevision?.revision ?? 1}</div><h2>{selected.name}</h2><p>{selected.description ?? '用于平台组件与场景测试的共享环境'}</p></div></div>
          <div className="environment-hero__actions"><div className="environment-owner"><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}<StatusPill status={selected.schedulingStatus ?? 'idle'}>{schedulingLabel(selected)}</StatusPill></div>{editable && <button className="button button--danger" disabled={(selected.schedulingStatus ?? 'idle') !== 'idle' || anyDirty || rollbackBusy !== undefined} title={(selected.schedulingStatus ?? 'idle') !== 'idle' ? '请先处理当前活动 Run' : anyDirty ? '请先保存或放弃环境配置更改' : undefined} onClick={() => void previewClusterRollback()}><RotateCcw size={15} /> 一键回滚至干净状态</button>}</div>
        </article>
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

        <article className="panel revision-history"><header className="panel__header"><div><span className="panel__icon"><History size={18} /></span><div><h2>Revision 历史</h2><p>恢复旧配置时始终创建新 Revision，不覆盖历史。</p></div></div></header><div className="revision-list">{(selected.revisions ?? (selected.currentRevision ? [selected.currentRevision] : [])).map((revision) => <div key={revision.id}><span className="revision-number">r{revision.revision}</span><div><strong>{revision.changeReason || (revision.revision === 1 ? '创建环境' : '未填写变更原因')}</strong><small>{revisionActor(revision, users)} · {formatTime(revision.createdAt)}</small></div>{revision.id === selected.currentRevision?.id ? <StatusPill status="active">当前</StatusPill> : editable && <button className="button button--quiet" disabled={anyDirty} onClick={() => setRestoreRevision(revision)}><RotateCcw size={14} /> 基于此恢复</button>}</div>)}</div></article>

        <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><LockKeyhole size={18} /></span><div><h2>最近环境运行</h2><p>同一环境一次只允许一个活动 Run</p></div></div></header>{environmentRuns.length ? <div className="simple-table">{environmentRuns.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`}><div><strong>{run.name ?? run.scenarioName ?? run.componentName}</strong><small>{formatTime(run.createdAt)}</small></div><StatusPill status={run.status} /></Link>)}</div> : <EmptyState title="暂无运行记录" />}</article>
      </section> : <section className="panel"><EmptyState title="没有可见环境" /></section>}
    </div>}
    {createOpen && <CreateEnvironmentModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('environments'); }} />}
    {saveOpen && selected && <ChangeReasonModal title="保存为新 Revision" description={`r${selected.currentRevision?.revision ?? 0} → r${(selected.currentRevision?.revision ?? 0) + 1}`} busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 仍锁定旧 Revision。` : undefined} diffLines={diffLines} onClose={() => setSaveOpen(false)} onConfirm={(reason) => void saveCurrent(reason)} />}
    {restoreRevision && selected && <ChangeReasonModal title={`基于 r${restoreRevision.revision} 恢复`} description="将复制该历史快照并创建新的当前 Revision。" busy={busy} warning={selected.schedulingStatus !== 'idle' ? `当前环境处于“${schedulingLabel(selected)}”，活动 Run 不会被修改。` : undefined} diffLines={[`目标快照：r${restoreRevision.revision}`, `主机 ${restoreRevision.hosts.length} 台 · 环境变量 ${Object.keys(restoreRevision.variables).length} 个 · CredentialRef ${restoreRevision.credentialRefs.length} 个`]} onClose={() => setRestoreRevision(undefined)} onConfirm={(reason) => void restore(reason)} />}
    {rollbackOpen && selected && <ClusterRollbackModal environment={selected} plan={rollbackPlan} error={rollbackError} explanation={rollbackExplanation} busy={rollbackBusy} onRetry={() => void previewClusterRollback()} onClose={() => { if (!rollbackBusy) { setRollbackOpen(false); setRollbackPlan(undefined); setRollbackError(undefined); setRollbackExplanation(undefined); } }} onConfirm={(confirmation) => void submitClusterRollback(confirmation)} />}
  </div>;
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
