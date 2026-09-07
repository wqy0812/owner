import { FileCode2, Plus, Trash2, Upload } from 'lucide-react';
import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { api } from '../../../api/client';
import { LegacyYamlNotice } from '../../../components/LegacyYamlNotice';
import { ResourceContractEditor } from '../../../components/ResourceContractEditor';
import { displayError, useApp } from '../../../context/AppContext';
import type { ActionDefinition, ComponentRelease } from '../../../types/domain';
import { ACTION_OPTIONS, splitCSV } from '../model';
import { PlaybookWorkspaceEditor } from './PlaybookWorkspaceEditor';



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

export function PlaybookActionEditor({ releaseId, releases, actions, onChange, onPersisted, onDirtyChange }: { releaseId: string; releases: ComponentRelease[]; actions: ActionDefinition[]; onChange: Dispatch<SetStateAction<ActionDefinition[]>>; onPersisted: (actions: ActionDefinition[]) => void; onDirtyChange: (dirty: boolean) => void }) {
  const { notify, platformOptionCategories } = useApp();
  const hostGroups = platformOptionCategories.find((category) => category.kind === 'host_group')?.options ?? [];
  const [selected, setSelected] = useState(0);
  const [confirmYamlMigration, setConfirmYamlMigration] = useState(false);
  useEffect(() => setConfirmYamlMigration(false), [selected, releaseId]);
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
    const kind = actions.some((item) => item.type === 'install') ? (actions.some((item) => item.type === 'rollback') ? 'check' : 'rollback') : 'install';
    const next: ActionDefinition = { name: kind, type: kind, playbook: '', hostGroup: hostGroups[0]?.value, timeoutSeconds: 1800, riskLevel: 'low' };
    loadRequest.current += 1;
    setLoading(false);
    onChange((current) => {
      pendingSelection.current = current.length;
      return [...current, next];
    });
    const template = '---\n- name: Complete this action before testing\n  ansible.builtin.fail:\n    msg: 请编写此动作的 Role 任务\n';
    setContent(template);
    setSavedContent('');
    setSavedSHA256('');
  }

  async function currentWorkspaceExpectation() {
    const workspace = await api.playbookWorkspace(releaseId);
    return {
      fileSHA256: savedSHA256 || workspace.files.find((file) => file.path === (action?.type === 'check' ? `tasks/checks/${action.id}.yml` : `tasks/${action?.type}.yml`))?.sha256 || '',
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
        await api.deleteActionPlaybook(releaseId, action.id ?? '', expected.fileSHA256, expected.treeSHA256);
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
      const playbook = await api.playbook(releaseId, action.id ?? '');
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
      const playbook = await api.uploadPlaybook(releaseId, action, file, expected.fileSHA256, expected.treeSHA256, confirmYamlMigration);
      setConfirmYamlMigration(false);
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
      const playbook = await api.savePlaybook(releaseId, action, content, expected.fileSHA256, expected.treeSHA256, confirmYamlMigration);
      setConfirmYamlMigration(false);
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
      <div><h3>Playbook 与生命周期动作</h3><p>上传 YAML 或直接在线编辑；每个版本使用独立 Role。部署依次运行前置检查、部署、后置检查；回滚默认运行回滚和回滚后检查。</p></div>
      <button type="button" className="button button--quiet" disabled={saving} onClick={addAction}><Plus size={15} /> 新增动作</button>
    </header>
    {action ? <>
      <div className="playbook-action-tabs" role="tablist" aria-label="Ansible 动作">
        {(['execute', 'check'] as const).map(group => <div key={group} role="group" aria-label={group === 'check' ? '检查动作' : '执行动作'}><span>{group === 'check' ? '检查动作' : '执行动作'}</span>{actions.map((item, index) => (item.type === 'check') === (group === 'check') && <button key={item.id ?? index} type="button" disabled={saving} className={selected === index ? 'active' : ''} onClick={() => selectAction(index)}>{item.name || item.type}</button>)}</div>)}
      </div>
      <div className="form-grid playbook-action-fields">
        <label><span>动作类型</span><select value={action.type} disabled={Boolean(action.id)} onChange={(event) => {
          const type = event.target.value as ActionDefinition['type'];
          setContent('');
          setSavedContent('');
          setSavedSHA256('');
          updateAction({ type, name: !action.name || action.name === action.type ? type : action.name, idempotent: type !== 'check' ? action.idempotent : false });
        }}>{ACTION_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.value} · {option.label}</option>)}</select>{action.id ? <small>已保存动作的类型不可修改；如需更换，请删除后新建。</small> : null}</label>
        <label><span>动作名称</span><input aria-label="动作名称" value={action.name ?? ''} onChange={event => updateAction({ name: event.target.value })} /></label><label><span>{action.type === 'check' ? '独立测试主机组（绑定时继承执行动作）' : '目标主机组'}</span><select required={action.type !== 'check'} value={action.hostGroup ?? ''} onChange={(event) => updateAction({ hostGroup: event.target.value })}><option value="">请选择主机组</option>{hostGroups.map((option) => <option key={option.id} value={option.value}>{option.label}</option>)}</select></label>
        {action.type !== 'check' && <>
          <label><span>{action.type === 'rollback' ? '回滚前检查（可选）' : '前置检查 *'}</span><select aria-label="前置检查" value={action.preCheckActionId ?? ''} onChange={event => updateAction({ preCheckActionId: event.target.value })}><option value="">{action.type === 'rollback' ? '不执行回滚前检查' : '请选择本版本的检查动作'}</option>{actions.filter(item => item.type === 'check' && item.id).map(item => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}</select></label>
          <label><span>{action.type === 'rollback' ? '回滚后检查' : '后置检查 *'}</span><select aria-label="后置检查" value={action.postCheckActionId ?? ''} onChange={event => updateAction({ postCheckActionId: event.target.value })}><option value="">{action.type === 'rollback' ? '复用被回滚动作的前置检查' : '请选择本版本的检查动作'}</option>{actions.filter(item => item.type === 'check' && item.id).map(item => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}</select></label>
          {action.type === 'rollback' && !action.postCheckActionId && <p className="info-note span-2">回滚后按实际来源动作复用检查：{actions.filter(item => ['install', 'configure', 'upgrade'].includes(item.type)).map(item => `${item.name || item.type} → ${actions.find(check => check.id === item.preCheckActionId)?.name || '待绑定'}`).join('；')}。执行计划将展示具体检查 YAML。</p>}
          {action.type !== 'rollback' && (!action.preCheckActionId || !action.postCheckActionId) && <p className="form-validation span-2">此动作尚未绑定完整检查，可保存 Draft，绑定完成后才能执行或发布。</p>}
        </>}
        <label className="checkbox-field"><input type="checkbox" checked={action.become ?? false} onChange={(event) => updateAction({ become: event.target.checked })} /><span>提权执行</span></label>
        <label><span>超时（秒）</span><input type="number" min={1} value={action.timeoutSeconds ?? 1800} onChange={(event) => updateAction({ timeoutSeconds: Number(event.target.value) })} /></label>
        <label><span>风险级别</span><select value={action.riskLevel ?? 'low'} onChange={(event) => { const riskLevel = event.target.value as NonNullable<ActionDefinition['riskLevel']>; updateAction({ riskLevel, destructive: riskLevel === 'destructive' }); }}><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="destructive">破坏性（需审批）</option></select></label>
        <LegacyYamlNotice value={action.legacyYamlSettings} checksInPrecheck={action.type !== 'check'} confirmed={confirmYamlMigration} onConfirm={setConfirmYamlMigration} />
        <p className="info-note span-2">facts 由本动作 YAML 自行采集；运行条件与残留探测写入前置检查 YAML，在依赖完成后执行。</p>
        <ResourceContractEditor value={action.resourceContract} dependencies={releases.find(release => release.id === releaseId)?.dependencies} onChange={resourceContract => updateAction({ resourceContract })} />
        <div className="span-2 platform-managed-path"><span>平台入口</span><code>{action.type === 'check' ? `tasks/checks/${action.id ?? "待保存"}.yml` : `tasks/${action.type}.yml`}</code><button type="button" className="button button--quiet" disabled={loading} onClick={() => void loadPlaybook()}>{loading ? '读取中…' : '载入编辑器'}</button></div>
        <label><span>Tags（逗号分隔）</span><input value={(action.tags ?? []).join(', ')} onChange={(event) => updateAction({ tags: splitCSV(event.target.value) })} /></label>
        <CredentialNameEditor values={action.requiredCredentials ?? []} onChange={(requiredCredentials) => updateAction({ requiredCredentials })} />
        {action.type !== 'check' ? <label className="checkbox-field span-2 action-capability-field"><input type="checkbox" checked={action.idempotent ?? false} onChange={(event) => updateAction({ idempotent: event.target.checked })} /><span><strong>可安全重试</strong><small>仅在已验证部分执行后可安全重跑时声明；已绑定的前置检查会随动作重试。</small></span></label> : null}
        {(action.type === 'upgrade' || action.type === 'rollback') ? <>
          <label><span>来源版本</span><select aria-label="来源 Release" value={action.fromReleaseId ?? ''} onChange={(event) => updateAction({ fromReleaseId: event.target.value || undefined })}><option value="">请选择来源版本</option>{action.fromReleaseId && !releaseIds.has(action.fromReleaseId) ? <option value={action.fromReleaseId}>{action.fromReleaseId} · 现有值</option> : null}{releases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.id}</option>)}</select></label>
          <label><span>目标版本</span><select aria-label="目标 Release" value={action.toReleaseId ?? ''} onChange={(event) => updateAction({ toReleaseId: event.target.value || undefined })}><option value="">请选择目标版本</option>{action.toReleaseId && !releaseIds.has(action.toReleaseId) ? <option value={action.toReleaseId}>{action.toReleaseId} · 现有值</option> : null}{releases.map((item) => <option key={item.id} value={item.id}>{item.version} · {item.id}</option>)}</select></label>
          {action.type === 'rollback' ? <small className="span-2">起止版本都留空时表示回退当前版本的安装；填写时表示从当前版本回到指定旧版本。</small> : null}
        </> : null}
      </div>
      <div className="playbook-source">
        <div className="playbook-source__toolbar">
          <label><Upload size={15} /><span>上传 .yml / .yaml</span><input type="file" accept=".yml,.yaml,text/yaml,application/x-yaml" onChange={(event) => { void uploadPlaybook(event.target.files?.[0]); event.currentTarget.value = ''; }} /></label>
          <span>Role 入口由平台生成；检查按 Action ID 定位</span><span>{dirty ? '有未保存内容' : content ? '内容已保存' : '可上传或载入现有文件'}</span>
        </div>
        <textarea className="code-editor playbook-source__editor" aria-label="Playbook 在线编辑器" spellCheck={false} value={content} placeholder={'---\n- name: Check required input\n  ansible.builtin.assert:\n    that: cf.inputs.version is defined'} onChange={(event) => setContent(event.target.value)} />
        <div className="playbook-source__actions"><button type="button" className="icon-text icon-text--danger" disabled={saving || actions.some(item => item.preCheckActionId === action.id && !!action.id || item.postCheckActionId === action.id && !!action.id)} title={action.type === 'check' ? '被执行动作引用的检查需先解除绑定' : undefined} onClick={() => void removeAction()}><Trash2 size={14} /> 移除动作</button><button type="button" className="button button--secondary" disabled={saving || !content.trim()} onClick={() => void savePlaybook()}><FileCode2 size={15} /> {saving ? '保存中…' : '保存 Playbook'}</button></div>
      </div>
      <PlaybookWorkspaceEditor releaseId={releaseId} refreshToken={`${action.type}:${savedSHA256}`} notify={notify} />
    </> : <><div className="playbook-editor__empty"><FileCode2 size={24} /><p>尚未配置生命周期动作。新增动作后即可上传或在线编写 Playbook。</p><button type="button" className="button button--secondary" onClick={addAction}><Plus size={15} /> 新增第一个动作</button></div><PlaybookWorkspaceEditor releaseId={releaseId} refreshToken="" notify={notify} /></>}
  </section>;
}
