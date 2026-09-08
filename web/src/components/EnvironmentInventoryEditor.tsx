import { Table, Tabs } from 'antd';
import { Field } from './Field';
import { Button, Input, Checkbox } from 'antd';
import { useState } from 'react';
import { Check, Network, Pencil, Plus, Search, Server, Trash2, X } from 'lucide-react';
import type { EnvironmentHost, PlatformOption } from '../types/domain';
import { EmptyState, Modal } from './Primitives';
import './EnvironmentInventoryEditor.css';
type Props = {
    hosts: EnvironmentHost[];
    options: PlatformOption[];
    editable: boolean;
    onChange: (hosts: EnvironmentHost[]) => void;
};
function GroupTags({ groups, options }: {
    groups: string[];
    options: PlatformOption[];
}) {
    return <div className="inventory-group-tags">{groups.length ? groups.map((value) => {
            const option = options.find((item) => item.value === value);
            return <span key={value} className={`inventory-group-tag${option?.retiredAt ? ' inventory-group-tag--retired' : ''}`} title={value}>{option?.label ?? value}{option?.retiredAt ? ' · 已退役' : ''}</span>;
        }) : <span className="inventory-ungrouped">未分组</span>}</div>;
}
export function EnvironmentInventoryEditor({ hosts, options, editable, onChange }: Props) {
    const [groupsOpen, setGroupsOpen] = useState(false);
    const [editing, setEditing] = useState<number | 'new'>();
    const usedGroups = new Set(hosts.flatMap((host) => host.groups));
    return <div className="editor-section inventory-editor">
    <div className="section-title inventory-toolbar">
      <div><h3>主机与分组</h3><p>{hosts.length} 台节点 · {usedGroups.size} 个已用主机组。修改后保存为新的环境版本。</p></div>
      {editable && <div className="inventory-toolbar__actions">
        <Button className="button button--secondary" onClick={() => setGroupsOpen(true)} htmlType={"button"} type="default"><Network size={15}/> 主机组管理</Button>
        <Button className="button button--primary" disabled={hosts.length >= 256} onClick={() => setEditing('new')} htmlType={"button"} type="primary"><Plus size={15}/> 添加节点</Button>
      </div>}
    </div>
    {hosts.length ? <div className="inventory-table-scroll"><Table className="inventory-table" dataSource={hosts} rowKey="name" pagination={false} scroll={{x: 760}} columns={[
{title:'节点',key:'node',render:(_,host)=><div className="inventory-node"><span className="inventory-node__icon"><Server size={17}/></span><div><strong>{host.name}</strong><small>{host.address}</small></div></div>},
{title:'主机组',key:'groups',render:(_,host)=><GroupTags groups={host.groups} options={options}/>},
{title:'SSH 连接',key:'ssh',width:200,render:(_,host)=><span className="inventory-connection">{host.user || '默认用户'}<span>端口 {host.port || 22}</span></span>},
{title:'操作',key:'actions',width:112,hidden:!editable,render:(_,host,index)=><div className="inventory-row-actions"><Button type="text" aria-label={`编辑节点 ${host.name}`} onClick={()=>setEditing(index)}><Pencil size={15}/></Button><Button type="text" danger aria-label={`移除节点 ${host.name}，保存后生效`} onClick={()=>onChange(hosts.filter((_,i)=>i!==index))}><Trash2 size={15}/></Button></div>}
]} /></div> : <EmptyState title="尚未添加节点" description={editable ? '添加节点并选择所属主机组，开始配置环境。' : '环境 Owner 尚未配置节点。'}/>}
    {groupsOpen && editable && <HostGroupManager hosts={hosts} options={options} onClose={() => setGroupsOpen(false)} onApply={(next) => { onChange(next); setGroupsOpen(false); }}/>}
    {editing !== undefined && editable && <HostEditor host={editing === 'new' ? undefined : hosts[editing]} hosts={hosts} options={options} onClose={() => setEditing(undefined)} onApply={(host) => {
                onChange(editing === 'new' ? [...hosts, host] : hosts.map((item, index) => index === editing ? host : item));
                setEditing(undefined);
            }}/>}
  </div>;
}
function HostGroupManager({ hosts, options, onClose, onApply }: Pick<Props, 'hosts' | 'options'> & {
    onClose: () => void;
    onApply: (hosts: EnvironmentHost[]) => void;
}) {
    const [draft, setDraft] = useState(() => hosts.map((host) => ({ ...host, groups: [...host.groups] })));
    const availableGroups = options.filter((option) => !option.retiredAt || draft.some((host) => host.groups.includes(option.value)));
    const [selectedValue, setSelectedValue] = useState(() => availableGroups.find((option) => hosts.some((host) => host.groups.includes(option.value)))?.value ?? availableGroups[0]?.value ?? '');
    const [search, setSearch] = useState('');
    const [mode, setMode] = useState<'members' | 'available'>('members');
    const selected = availableGroups.find((option) => option.value === selectedValue) ?? availableGroups[0];
    const members = draft.filter((host) => host.groups.includes(selected?.value ?? ''));
    const visibleHosts = draft.map((host, index) => ({ host, index })).filter(({ host }) => {
        const matches = `${host.name} ${host.address}`.toLowerCase().includes(search.trim().toLowerCase());
        return matches && host.groups.includes(selected?.value ?? '') === (mode === 'members');
    });
    const ungrouped = draft.filter((host) => !host.groups.length);
    const added = draft.reduce((sum, host, index) => sum + host.groups.filter((group) => !hosts[index].groups.includes(group)).length, 0);
    const removed = draft.reduce((sum, host, index) => sum + hosts[index].groups.filter((group) => !host.groups.includes(group)).length, 0);
    function changeMembership(indices: number[], add: boolean) {
        if (!selected || selected.retiredAt)
            return;
        setDraft((items) => items.map((host, index) => !indices.includes(index) ? host : {
            ...host, groups: add ? [...new Set([...host.groups, selected.value])] : host.groups.filter((group) => group !== selected.value),
        }));
    }
    return <Modal size="wide" title="主机组管理" description="按组添加或移除节点；同一节点可以属于多个主机组。" onClose={onClose} footer={<footer className="modal-actions host-group-footer"><div aria-live="polite">{added || removed ? `新增 ${added} 项、移除 ${removed} 项分组关系` : '暂无分组变更'}<small>应用后，点击“保存新版本”生效。</small></div><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={(!added && !removed) || ungrouped.length > 0} onClick={() => onApply(draft)} htmlType={"button"} type="primary"><Check size={15}/> 应用更改</Button></footer>}>
    <div className="host-group-manager">
      <aside className="host-group-sidebar" aria-label="主机组列表">
        <div className="host-group-sidebar__title"><span>主机组</span><span>{availableGroups.length}</span></div>
        <div className="host-group-sidebar__list">{availableGroups.map((option) => <button key={option.id} className={`host-group-option${selected?.value === option.value ? ' is-selected' : ''}`} aria-pressed={selected?.value === option.value} onClick={() => { setSelectedValue(option.value); setSearch(''); setMode('members'); }}>
          <Network size={16}/><span><strong>{option.label}</strong>{(option.retiredAt || option.label !== option.value) && <small>{option.retiredAt ? '已退役 · 只读' : option.value}</small>}</span><b>{draft.filter((host) => host.groups.includes(option.value)).length}</b>
        </button>)}</div>
        <p>主机组名称由平台管理员维护。</p>
      </aside>
      <section className="host-group-members" aria-label="主机组节点">
        {selected ? <>
          <div className="host-group-members__header"><div><h3>{selected.label}</h3><p>{members.length} 台组内节点 / 环境共 {hosts.length} 台</p></div><span className="inventory-group-tag">{selected.retiredAt ? '已退役' : '主机组'}</span></div>
          <Tabs className="host-group-member-tabs" activeKey={mode} onChange={value => setMode(value as 'members' | 'available')} items={[
            { key: 'members', label: `组内节点 ${members.length}` },
            ...(!selected.retiredAt ? [{ key: 'available', label: `添加节点 ${hosts.length - members.length}` }] : []),
          ]}/>
          <label className="inventory-search"><Search size={16}/><Input autoFocus aria-label="搜索节点" placeholder="搜索节点名称或地址" value={search} onChange={(event) => setSearch(event.target.value)}/></label>
          <div className="host-group-result-bar"><span>{search ? `匹配 ${visibleHosts.length} 台节点` : mode === 'members' ? '移出后，节点仍保留在环境中' : '从环境已有节点中添加'} </span>{mode === 'available' && visibleHosts.length > 0 && <Button className="button button--quiet" onClick={() => changeMembership(visibleHosts.map(({ index }) => index), true)} htmlType={"button"} type="default"><Plus size={13}/> 添加当前结果</Button>}</div>
          <div className="host-group-node-list">{visibleHosts.length ? visibleHosts.map(({ host, index }) => <div className="host-group-node-row" key={index}>
            <div className="inventory-node"><span className="inventory-node__icon"><Server size={17}/></span><div><strong>{host.name}</strong><small>{host.address}</small><GroupTags groups={host.groups} options={options}/></div></div>
            {!selected.retiredAt && <Button className={`button ${mode === 'members' ? 'button--quiet' : 'button--secondary'}`} aria-label={`${mode === 'members' ? '移出' : '添加'}节点 ${host.name}`} onClick={() => changeMembership([index], mode === 'available')} htmlType={"button"}>{mode === 'members' ? <X size={14}/> : <Plus size={14}/>}{mode === 'members' ? '移出' : '添加'}</Button>}
          </div>) : <EmptyState title={search ? '没有匹配的节点' : mode === 'members' ? '主机组中还没有节点' : '所有节点均已加入此组'} description={search ? '试试其他节点名称或地址。' : !hosts.length ? '请先在环境中添加节点。' : mode === 'members' ? '切换到“添加节点”选择组内成员。' : undefined}/>}</div>
        </> : <EmptyState title="暂无可用主机组" description="请联系平台管理员配置主机组。"/>}
      </section>
    </div>
    {ungrouped.length > 0 && <div className="host-group-validation" role="alert">{ungrouped.map((host) => host.name).join('、')} 尚未分组，请至少加入一个主机组后再应用。</div>}

  </Modal>;
}
function HostEditor({ host, hosts, options, onClose, onApply }: Pick<Props, 'hosts' | 'options'> & {
    host?: EnvironmentHost;
    onClose: () => void;
    onApply: (host: EnvironmentHost) => void;
}) {
    const [draft, setDraft] = useState<EnvironmentHost>(() => host ? { ...host, groups: [...host.groups] } : { name: '', address: '', groups: [], user: 'root', port: 22 });
    const [error, setError] = useState('');
    function submit(_values?: unknown) {
        const next = { ...draft, name: draft.name.trim(), address: draft.address.trim(), user: draft.user?.trim() };
        if (!next.name || !next.address || !next.groups.length) {
            setError('请填写节点名称、地址，并至少选择一个主机组。');
            return;
        }
        if (hosts.some((item) => item !== host && item.name === next.name)) {
            setError('节点名称已存在，请使用其他名称。');
            return;
        }
        onApply(next);
    }
    return <Modal title={host ? '编辑节点' : '添加节点'} description="配置节点连接信息与所属主机组。" onClose={onClose} formProps={{ onFinish: submit, layout: "vertical", preserve: false }} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={onClose} htmlType={"button"} type="default">取消</Button><Button className="button button--primary" disabled={!draft.groups.length} htmlType={"submit"} type="primary">{host ? '应用节点更改' : '添加节点'}</Button></footer>}>
    <div className="form-grid">
      <Field label={"节点名称"} required><Input autoFocus required value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })}/></Field>
      <Field label={"节点地址"} required><Input required value={draft.address} onChange={(event) => setDraft({ ...draft, address: event.target.value })} placeholder="IP 地址或主机名"/></Field>
      <Field label={"SSH 用户"}><Input value={draft.user ?? ''} onChange={(event) => setDraft({ ...draft, user: event.target.value })}/></Field>
      <Field label={"SSH 端口"} required><Input required type="number" min={1} max={65535} value={draft.port ?? 22} onChange={(event) => setDraft({ ...draft, port: Number(event.target.value) })}/></Field>
    </div>
    <fieldset className="inventory-group-picker"><legend>所属主机组 <span>至少选择一个</span></legend><div>{options.filter((option) => !option.retiredAt || host?.groups.includes(option.value)).map((option) => <Checkbox key={option.id} className={draft.groups.includes(option.value) ? 'is-selected' : ''} checked={draft.groups.includes(option.value)} disabled={Boolean(option.retiredAt)} onChange={(event) => setDraft({ ...draft, groups: event.target.checked ? [...draft.groups, option.value] : draft.groups.filter((value) => value !== option.value) })}><span>{option.label}{option.retiredAt ? '（已退役）' : ''}</span></Checkbox>)}</div>{!options.some((option) => !option.retiredAt) && <p>暂无可选主机组，请联系平台管理员。</p>}</fieldset>
    {error && <p className="host-group-validation" role="alert">{error}</p>}

  </Modal>;
}
