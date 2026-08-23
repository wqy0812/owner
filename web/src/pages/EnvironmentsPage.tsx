import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { Braces, CloudCog, Cpu, HardDrive, KeyRound, LockKeyhole, Network, Plus, Save, Server, Trash2, UserRound } from 'lucide-react';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { CredentialRef, EnvironmentHost } from '../types/domain';

type Tab = 'inventory' | 'facts' | 'variables' | 'credentials';
type EnvironmentVariableRow = { name: string; value: string };

export function EnvironmentsPage() {
  const { user, notify, signalRefresh } = useApp();
  const { data: environments, loading, error, isRefreshing, reload } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const { data: runs } = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const [selectedId, setSelectedId] = useState<string>();
  const selected = useMemo(() => environments?.find((item) => item.id === selectedId) ?? environments?.[0], [environments, selectedId]);
  const [tab, setTab] = useState<Tab>('inventory');
  const [hosts, setHosts] = useState<EnvironmentHost[]>([]);
  const [factsText, setFactsText] = useState('{}');
  const [variables, setVariables] = useState<EnvironmentVariableRow[]>([]);
  const [credentials, setCredentials] = useState<CredentialRef[]>([]);
  const [busy, setBusy] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const editable = user.role === 'environment_owner' && selected?.ownerId === user.id;

  useEffect(() => {
    setHosts(selected?.currentRevision?.hosts ?? []);
    setFactsText(JSON.stringify(selected?.currentRevision?.facts ?? {}, null, 2));
    setVariables(Object.entries(selected?.currentRevision?.variables ?? {}).map(([name, value]) => ({ name, value })));
    setCredentials(selected?.currentRevision?.credentialRefs ?? []);
  }, [selected?.currentRevision?.id, selected?.id]);

  async function saveCurrent() {
    if (!selected || !editable) return;
    setBusy(true);
    try {
      if (tab === 'inventory') await api.updateInventory(selected.id, hosts);
      if (tab === 'facts') await api.updateFacts(selected.id, JSON.parse(factsText) as Record<string, unknown>);
      if (tab === 'variables') {
        const names = variables.map((item) => item.name.trim());
        if (names.some((name) => !/^[A-Z_][A-Z0-9_]*$/.test(name))) throw new Error('环境变量名必须是大写标识符。');
        if (new Set(names).size !== names.length) throw new Error('环境变量名不能重复。');
        await api.updateVariables(selected.id, Object.fromEntries(variables.map((item) => [item.name.trim(), item.value])));
      }
      if (tab === 'credentials') await api.updateCredentialRefs(selected.id, credentials.map(({ name, type, reference }) => ({ name, type, reference })));
      notify('success', '环境 Revision 已更新', '后续运行会锁定新的环境快照。'); signalRefresh('environments');
    } catch (reason) {
      notify('error', '环境保存失败', reason instanceof SyntaxError ? '内容必须是有效 JSON。' : displayError(reason));
    } finally { setBusy(false); }
  }

  function updateHost(index: number, patch: Partial<EnvironmentHost>) {
    setHosts((items) => items.map((host, i) => i === index ? { ...host, ...patch } : host));
  }

  const environmentRuns = runs?.filter((run) => run.environmentId === selected?.id).slice(0, 4) ?? [];
  const facts = selected?.currentRevision?.facts ?? {};

  return <div className="page">
    <PageHeader eyebrow="Execution environments" title="环境管理" description="环境 Owner 管理 Inventory、环境事实、IMAGE_REGISTRY、FILE_STATION 和安全凭据引用；其他 Owner 可选择环境运行测试。" actions={user.role === 'environment_owner' ? <button className="button button--primary" onClick={() => setCreateOpen(true)}><Plus size={16} /> 新建环境</button> : undefined} />
    <RefreshNotice loading={isRefreshing} error={environments ? error : undefined} onRetry={() => void reload()} />
    {loading && !environments ? <LoadingBlock label="正在读取共享环境…" /> : error && !environments ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="catalog-layout">
      <aside className="catalog-list panel">
        <div className="catalog-list__header"><strong>共享环境</strong><span>{environments?.length ?? 0}</span></div>
        {environments?.map((environment) => <button key={environment.id} className={`catalog-item${selected?.id === environment.id ? ' active' : ''}`} onClick={() => setSelectedId(environment.id)}><span className="catalog-item__icon catalog-item__icon--cyan"><CloudCog size={18} /></span><span><strong>{environment.name}</strong><small>r{environment.currentRevision?.revision ?? 1} · {environment.currentRevision?.hosts?.length ?? 0} 台主机</small></span><StatusPill status={environment.status ?? 'ready'} /></button>)}
      </aside>
      {selected ? <section className="detail-stack">
        <article className="panel environment-hero">
          <div><span className="environment-icon"><CloudCog size={25} /></span><div><div className="eyebrow">Environment revision {selected.currentRevision?.revision ?? 1}</div><h2>{selected.name}</h2><p>{selected.description ?? '用于平台组件与场景测试的共享环境'}</p></div></div>
          <div className="environment-owner"><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}<StatusPill status={selected.status ?? 'ready'} /></div>
        </article>
        <section className="fact-grid">
          <article><Cpu size={18} /><span>架构</span><strong>{String(facts.arch ?? facts.architecture ?? 'amd64')}</strong></article>
          <article><HardDrive size={18} /><span>操作系统</span><strong>{String(facts.os ?? facts.distribution ?? 'Kylin V10')}</strong></article>
          <article><Network size={18} /><span>网络栈</span><strong>{String(facts.network ?? facts.ipFamily ?? 'IPv4')}</strong></article>
          <article><Server size={18} /><span>主机</span><strong>{hosts.length}</strong></article>
        </section>
        <article className="panel environment-editor">
          <div className="tabs" role="tablist"><button className={tab === 'inventory' ? 'active' : ''} onClick={() => setTab('inventory')}><Server size={16} /> Inventory</button><button className={tab === 'facts' ? 'active' : ''} onClick={() => setTab('facts')}><Cpu size={16} /> 环境事实</button><button className={tab === 'variables' ? 'active' : ''} onClick={() => setTab('variables')}><Braces size={16} /> 环境变量</button><button className={tab === 'credentials' ? 'active' : ''} onClick={() => setTab('credentials')}><KeyRound size={16} /> 凭据引用</button></div>
          {tab === 'inventory' && <div className="editor-section"><div className="section-title"><div><h3>主机与分组</h3><p>保存后创建新的 Environment Revision。</p></div>{editable && <button className="button button--quiet" onClick={() => setHosts((items) => [...items, { name: '', address: '', groups: ['all'], port: 22, user: 'root' }])}><Plus size={15} /> 添加主机</button>}</div><div className="host-table"><div className="host-table__head"><span>主机名</span><span>地址</span><span>主机组</span><span>SSH 用户 / 端口</span><span /></div>{hosts.map((host, index) => <div className="host-row" key={`${host.name}-${index}`}><input value={host.name} disabled={!editable} onChange={(event) => updateHost(index, { name: event.target.value })} /><input value={host.address} disabled={!editable} onChange={(event) => updateHost(index, { address: event.target.value })} /><input value={host.groups.join(', ')} disabled={!editable} onChange={(event) => updateHost(index, { groups: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} /><div className="split-input"><input value={host.user ?? ''} disabled={!editable} onChange={(event) => updateHost(index, { user: event.target.value })} /><input type="number" value={host.port ?? 22} disabled={!editable} onChange={(event) => updateHost(index, { port: Number(event.target.value) })} /></div>{editable && <button className="icon-button icon-button--danger" aria-label="删除主机" onClick={() => setHosts((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div>{!hosts.length && <EmptyState title="没有主机" description={editable ? '添加一台测试主机。' : '环境 Owner 尚未配置 Inventory。'} />}</div>}
          {tab === 'facts' && <div className="editor-section"><div className="section-title"><div><h3>环境事实</h3><p>架构、操作系统和网络栈会参与组件兼容性预检。</p></div></div><textarea className="code-editor" value={factsText} disabled={!editable} onChange={(event) => setFactsText(event.target.value)} spellCheck={false} /></div>}
          {tab === 'variables' && <div className="editor-section"><div className="section-title"><div><h3>组件作业环境变量</h3><p>IMAGE_REGISTRY 指定镜像仓库，FILE_STATION 指定组件介质站，均填写 host:port。</p></div>{editable && <button className="button button--quiet" onClick={() => setVariables((items) => [...items, { name: '', value: '' }])}><Plus size={15} /> 添加变量</button>}</div><div className="environment-variable-list">{variables.map((variable, index) => <div key={index}><span className="variable-icon"><Braces size={17} /></span><input aria-label="环境变量名" value={variable.name} disabled={!editable} placeholder="IMAGE_REGISTRY" onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value.toUpperCase() } : item))} /><input aria-label={`环境变量 ${variable.name || index} 的值`} value={variable.value} disabled={!editable} placeholder={variable.name === 'IMAGE_REGISTRY' ? '192.168.88.54:5000' : variable.name === 'FILE_STATION' ? '192.168.88.57:8080' : '非敏感字符串值'} onChange={(event) => setVariables((items) => items.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" aria-label={`删除环境变量 ${variable.name || index}`} onClick={() => setVariables((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div>{!variables.length && <EmptyState title="尚未配置环境变量" description={editable ? '至少添加 IMAGE_REGISTRY 和 FILE_STATION，分别用于镜像和组件介质。' : '环境 Owner 尚未配置环境变量。'} />}</div>}
          {tab === 'credentials' && <div className="editor-section"><div className="section-title"><div><h3>CredentialRef</h3><p>数据库和 API 只保存引用，其他角色只看到脱敏值。</p></div>{editable && <button className="button button--quiet" onClick={() => setCredentials((items) => [...items, { name: '', type: 'envVarRef', reference: '' }])}><Plus size={15} /> 添加引用</button>}</div><div className="credential-list">{credentials.map((credential, index) => <div key={`${credential.name}-${index}`}><span className="credential-icon"><LockKeyhole size={17} /></span><input aria-label="凭据名称" value={credential.name} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} /><select value={credential.type} disabled={!editable} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, type: event.target.value as CredentialRef['type'] } : item))}><option value="envVarRef">envVarRef</option><option value="sshKeyPath">sshKeyPath</option></select><input value={editable ? credential.reference ?? '' : credential.maskedReference ?? '••••••••'} disabled={!editable} placeholder={credential.type === 'envVarRef' ? 'ANSIBLE_SSH_KEY' : '/path/to/key'} onChange={(event) => setCredentials((items) => items.map((item, i) => i === index ? { ...item, reference: event.target.value } : item))} />{editable && <button className="icon-button icon-button--danger" onClick={() => setCredentials((items) => items.filter((_, i) => i !== index))}><Trash2 size={15} /></button>}</div>)}</div></div>}
          {editable && <footer className="editor-footer"><span><LockKeyhole size={14} /> 保存期间不会读取凭据实际内容</span><button className="button button--primary" disabled={busy} onClick={() => void saveCurrent()}><Save size={16} /> {busy ? '保存中…' : '保存新 Revision'}</button></footer>}
        </article>
        <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><LockKeyhole size={18} /></span><div><h2>最近环境运行</h2><p>同一环境一次只允许一个活动 Run</p></div></div></header>{environmentRuns.length ? <div className="simple-table">{environmentRuns.map((run) => <div key={run.id}><div><strong>{run.name ?? run.scenarioName ?? run.componentName}</strong><small>{formatTime(run.createdAt)}</small></div><StatusPill status={run.status} /></div>)}</div> : <EmptyState title="暂无运行记录" />}</article>
      </section> : <section className="panel"><EmptyState title="没有可见环境" /></section>}
    </div>}
    {createOpen && <CreateEnvironmentModal onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); signalRefresh('environments'); }} />}
  </div>;
}

function CreateEnvironmentModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); const form = new FormData(event.currentTarget);
    try {
      await api.createEnvironment({
        name: String(form.get('name')), description: String(form.get('description')),
        facts: { architecture: String(form.get('architecture')), os: String(form.get('os')), network: String(form.get('network')) },
      });
      notify('success', '环境已创建', '请继续配置 Inventory 和 CredentialRef。'); onDone();
    } catch (reason) { notify('error', '创建环境失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <Modal title="新建共享环境" description="环境由当前 Environment Owner 管理，其他 Owner 可用它运行测试。" onClose={onClose}><form onSubmit={(event) => void submit(event)}><div className="form-grid"><label><span>环境名称</span><input name="name" required placeholder="集群测试环境" /></label><label><span>架构</span><input name="architecture" defaultValue="amd64" required /></label><label><span>操作系统</span><input name="os" defaultValue="Kylin V10" required /></label><label><span>网络栈</span><input name="network" defaultValue="IPv4" required /></label><label className="span-2"><span>说明</span><textarea name="description" rows={3} /></label></div><footer className="modal-actions"><button type="button" className="button button--quiet" onClick={onClose}>取消</button><button className="button button--primary" disabled={busy}>{busy ? '创建中…' : '创建环境'}</button></footer></form></Modal>;
}
