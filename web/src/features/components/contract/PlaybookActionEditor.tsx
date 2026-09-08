import { Upload as AntUpload } from 'antd';
import { useModalBusy } from '../../../components/ModalBusyContext';
import { useDialogs } from '../../../components/UIProvider';
import { Input, Button } from 'antd';
import { FileCode2, Plus, Trash2, Upload } from 'lucide-react';
import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { api } from '../../../api/client';
import { displayError, useApp } from '../../../context/AppContext';
import type { ActionDefinition, ComponentRelease } from '../../../types/domain';
import { ACTION_OPTIONS } from '../model';
import { PlaybookActionSettings } from './PlaybookActionSettings';
import './PlaybookEditor.css';
import { PlaybookWorkspaceEditor } from './PlaybookWorkspaceEditor';
export function PlaybookActionEditor({ releaseId, releases, actions, onChange, onPersisted, onWorkspacePersisted, onDirtyChange, savedActions = actions, disabled = false }: {
    releaseId: string;
    releases: ComponentRelease[];
    actions: ActionDefinition[];
    onChange: Dispatch<SetStateAction<ActionDefinition[]>>;
    onPersisted: (actions: ActionDefinition[], actionId?: string) => void | Promise<void>;
    onWorkspacePersisted?: () => Promise<void>;
    savedActions?: ActionDefinition[];
    disabled?: boolean;
    onDirtyChange: (dirty: boolean) => void;
}) {
    const { confirm } = useDialogs();
    const { notify, platformOptionCategories } = useApp();
    const hostGroups = platformOptionCategories.find((category) => category.kind === 'host_group')?.options ?? [];
    const [selected, setSelected] = useState(0);
    const [content, setContent] = useState('');
    const [savedContent, setSavedContent] = useState('');
    const [savedSHA256, setSavedSHA256] = useState('');
    const [view, setView] = useState<'actions' | 'files'>('actions');
    const [workspaceDirty, setWorkspaceDirty] = useState(false);
    const [loadError, setLoadError] = useState('');
    const [loaded, setLoaded] = useState(false);
    const [loading, setLoading] = useState(false);
    const [writing, setSaving] = useState(false);
    const saving = writing || disabled;
    useModalBusy(writing || loading);
    const pendingSelection = useRef<number>();
    const loadRequest = useRef(0);
    const action = actions[selected];
    const dirty = content !== savedContent;
    const entryPath = action?.type === 'check' ? `tasks/checks/${action.id ?? '待保存'}.yml` : `tasks/${action?.type}.yml`;
    const configurationDirty = !!action && JSON.stringify(action) !== JSON.stringify(savedActions.find(item => item.id === action.id && item.type === action.type));
    useEffect(() => onDirtyChange(dirty || workspaceDirty), [dirty, workspaceDirty, onDirtyChange]);
    useEffect(() => {
        if (action?.id && !loaded) void loadPlaybook();
        return () => { loadRequest.current += 1; };
        // Selection owns loading; metadata edits must never replace the current text.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [releaseId, selected, action?.id]);
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
        if (selected < actions.length)
            return;
        setSelected(Math.max(0, actions.length - 1));
        setContent('');
        setSavedContent('');
        setSavedSHA256('');
    }, [actions.length, selected]);
    async function selectAction(index: number) {
        if (index === selected)
            return;
        if (dirty && !await confirm('当前 Playbook 有未保存内容，确认放弃并切换动作？'))
            return;
        await selectActionAfterConfirmation(index);
    }
    function updateAction(patch: Partial<ActionDefinition>, index = selected) {
        // Playbook saves are asynchronous. Always merge into the latest parent
        // state so a completed save cannot replace actions added while it ran.
        onChange((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
    }
    async function addAction() {
        if (dirty && !await confirm('当前 Playbook 有未保存内容，确认放弃并新增动作？'))
            return;
        const kind = actions.some((item) => item.type === 'install') ? (actions.some((item) => item.type === 'rollback') ? 'check' : 'rollback') : 'install';
        const next: ActionDefinition = { name: kind, type: kind, playbook: '', hostGroup: hostGroups[0]?.value, timeoutSeconds: 1800, riskLevel: 'low' };
        loadRequest.current += 1;
        setLoading(false);
        onChange((current) => {
            pendingSelection.current = current.length;
            return [...current, next];
        });
        setLoaded(true);
        setLoadError('');
        setView('actions');
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
        if (!action || !await confirm(action.id
            ? `确认立即删除 ${action.name || action.type} Action 及入口 ${entryPath}？该操作不会因关闭 Draft 编辑器而撤销。`
            : `确认移除尚未保存的 ${action.type} Action？`))
            return;
        setSaving(true);
        try {
            if (action.id) {
                const expected = await currentWorkspaceExpectation();
                await api.deleteActionPlaybook(releaseId, action.id, expected.fileSHA256, expected.treeSHA256);
            }
            const remaining = actions.filter((_, index) => index !== selected);
            if (action.id) await onPersisted(remaining, action.id);
            else onChange(remaining);
            loadRequest.current += 1;
            setLoading(false);
            setLoaded(false);
            setLoadError('');
            setSelected(Math.max(0, selected - 1));
            setContent('');
            setSavedContent('');
            setSavedSHA256('');
            if (action.id) notify('success', 'Action 已删除', '动作配置、入口文件与工作区清单已原子更新。');
        }
        catch (reason) {
            notify('error', '删除动作入口失败', displayError(reason));
        }
        finally {
            setSaving(false);
        }
    }
    async function loadPlaybook() {
        if (!action?.id)
            return;
        if (dirty && !await confirm('当前 Playbook 有未保存内容，确认放弃并重新载入？')) return;
        const request = ++loadRequest.current;
        setLoadError('');
        setLoading(true);
        try {
            const playbook = await api.playbook(releaseId, action.id ?? '');
            if (request !== loadRequest.current)
                return;
            setContent(playbook.content);
            setLoaded(true);
            setSavedContent(playbook.content);
            setSavedSHA256(playbook.sha256);
        }
        catch (reason) {
            if (request !== loadRequest.current)
                return;
            setLoadError(displayError(reason));
            setLoaded(false);
            notify('error', '读取 Playbook 失败', displayError(reason));
        }
        finally {
            if (request === loadRequest.current)
                setLoading(false);
        }
    }
    async function uploadPlaybook(file?: File) {
        if (!file)
            return;
        const actionIndex = selected;
        setSaving(true);
        try {
            if (!action)
                return;
            const expected = await currentWorkspaceExpectation();
            const playbook = await api.uploadPlaybook(releaseId, action, file, expected.fileSHA256, expected.treeSHA256);
            const persistedAction = { ...(playbook.action ?? action), playbook: playbook.path };
            const nextActions = actions.map((item, index) => index === actionIndex ? persistedAction : item);
            setContent(playbook.content);
            await onPersisted(nextActions, persistedAction.id);
            setLoaded(true);
            setSavedContent(playbook.content);
            setSavedSHA256(playbook.sha256);
            notify('success', 'Action 已保存', '动作配置、入口文件与工作区清单已作为一个操作保存。');
        }
        catch (reason) {
            notify('error', '上传 Playbook 失败', displayError(reason));
        }
        finally {
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
            await onPersisted(nextActions, persistedAction.id);
            setLoaded(true);
            setSavedContent(content);
            setSavedSHA256(playbook.sha256);
            notify('success', 'Action 已保存', '动作配置、入口文件与工作区清单已作为一个操作保存。');
        }
        catch (reason) {
            notify('error', '保存 Playbook 失败', displayError(reason));
        }
        finally {
            setSaving(false);
        }
    }
    async function openActionFile(path: string) {
        const index = actions.findIndex(item => (item.type === 'check' ? `tasks/checks/${item.id}.yml` : `tasks/${item.type}.yml`) === path);
        if (index < 0) { notify('error', '未找到对应动作', path); return false; }
        if (index !== selected) {
            if (dirty && !await confirm('当前 Playbook 有未保存内容，确认放弃并切换动作？')) return false;
            await selectActionAfterConfirmation(index);
        }
        setView('actions');
        return true;
    }
    async function selectActionAfterConfirmation(index: number) {
        loadRequest.current += 1;
        setLoading(false);
        setSelected(index);
        setLoaded(false);
        setLoadError('');
        setContent('');
        setSavedContent('');
        setSavedSHA256('');
    }
    return <section className="playbook-editor playbook-workbench">
      <header className="playbook-editor__header">
        <div><h3>Playbook 工作台</h3><p>选择动作编写入口任务，或维护本版本的模板与文件。</p></div>
        <div className="playbook-view-switch" aria-label="编辑视图">
          <Button disabled={saving} aria-pressed={view === 'actions'} onClick={() => setView('actions')}>动作入口 <span>{actions.length}</span></Button>
          <Button disabled={saving} aria-pressed={view === 'files'} onClick={() => setView('files')}>工作区文件{workspaceDirty ? ' · 未保存' : ''}</Button>
        </div>
      </header>
      <div hidden={view !== 'actions'} className="playbook-action-view">
        <nav className="playbook-action-nav" aria-label="Ansible 动作">
          <div className="playbook-action-nav__heading"><strong>生命周期动作</strong><Button type="text" size="small" disabled={saving || loading} onClick={() => void addAction()}><Plus size={14}/>新增动作</Button></div>
          {(['execute', 'check'] as const).map(group => <div key={group} role="group" aria-label={group === 'check' ? '检查动作' : '执行动作'}>
            <h4>{group === 'check' ? '检查动作' : '执行动作'}</h4>
            {actions.map((item, index) => (item.type === 'check') === (group === 'check') && <button key={item.id ?? index} type="button" aria-label={item.name || item.type} aria-current={selected === index ? 'true' : undefined} disabled={saving} className={selected === index ? 'active' : ''} onClick={() => void selectAction(index)}>
              <FileCode2 size={16}/><span><strong>{item.name || item.type}</strong><small>{ACTION_OPTIONS.find(option => option.value === item.type)?.label} · {item.type}</small></span>
              {JSON.stringify(item) !== JSON.stringify(savedActions.find(saved => saved.id === item.id && saved.type === item.type)) && <i title="动作配置未保存"/>}
            </button>)}
            {!actions.some(item => (item.type === 'check') === (group === 'check')) && <p>暂无{group === 'check' ? '检查' : '执行'}动作</p>}
          </div>)}
          <p className="playbook-action-nav__note">执行动作可绑定前置与后置检查；检查的 YAML 在对应检查动作中维护。</p>
        </nav>
        {action ? <>
          <section className="playbook-source" aria-label="当前动作入口">
            <header className="playbook-entry-heading"><div><span>当前编辑 · {action.name || action.type}</span><strong><FileCode2 size={16}/><code>{entryPath}</code></strong></div><span className={`playbook-save-state${dirty || configurationDirty ? ' is-dirty' : ''}`} role="status">{loading ? '正在载入…' : dirty || configurationDirty ? '有未保存内容' : loaded ? '内容已保存' : loadError ? '载入失败' : '尚未载入'}</span></header>
            <div className="playbook-source__toolbar">
              <span>Role 任务 · YAML</span>
              <Button size="small" disabled={loading || saving || !action.id} onClick={() => void loadPlaybook()} title="重新读取当前动作的入口文件">{loaded ? '重新载入' : '载入编辑器'}</Button>
              <AntUpload showUploadList={false} disabled={loading || saving} accept=".yml,.yaml,text/yaml,application/x-yaml" beforeUpload={file => { void uploadPlaybook(file); return false; }}><Button size="small" disabled={loading || saving}><Upload size={14}/>上传并保存 YAML</Button></AntUpload>
            </div>
            {loadError && <div className="playbook-load-error" role="alert">载入失败：{loadError}。请重新载入后编辑。</div>}
            {!loaded && action.id ? <div className="playbook-entry-empty"><FileCode2 size={30}/><p>{loading ? '正在读取当前动作的 YAML…' : '入口文件尚未载入'}</p><small>载入成功后将在这里显示实际文件内容。</small></div> : <Input.TextArea className="code-editor playbook-source__editor" aria-label="Playbook 在线编辑器" disabled={loading || saving} spellCheck={false} value={content} placeholder="在这里编写当前动作的 Role 任务 YAML" onChange={event => setContent(event.target.value)}/>}
            <footer className="playbook-source__actions"><span>保存当前动作配置与入口 YAML，立即生效。</span><Button type="primary" disabled={saving || loading || !content.trim() || !loaded && !!action.id} onClick={() => void savePlaybook()}><FileCode2 size={15}/>{writing ? '保存中…' : '保存 Playbook'}</Button></footer>
          </section>
          <div className="playbook-settings-panel"><PlaybookActionSettings disabled={saving || loading} action={action} actions={actions} releases={releases} updateAction={updateAction} onTypeChange={() => { setContent(''); setSavedContent(''); setSavedSHA256(''); setLoaded(true); setLoadError(''); }}/>
            <div className="playbook-settings-delete"><Button type="text" danger disabled={saving || loading || actions.some(item => !!action.id && (item.preCheckActionId === action.id || item.postCheckActionId === action.id))} title={action.type === 'check' ? '被执行动作引用的检查需先解除绑定' : undefined} onClick={() => void removeAction()}><Trash2 size={14}/>移除动作</Button></div>
          </div>
        </> : <div className="playbook-editor__empty"><FileCode2 size={32}/><h4>从第一个生命周期动作开始</h4><p>新增安装或检查动作后，在这里编辑 YAML 并配置执行方式。</p><Button onClick={() => void addAction()}><Plus size={15}/>新增第一个动作</Button></div>}
      </div>
      <div hidden={view !== 'files'} className="playbook-files-view"><PlaybookWorkspaceEditor disabled={saving || loading} releaseId={releaseId} refreshToken={`${actions.map(item => item.id).join(':')}:${savedSHA256}`} notify={notify} onDirtyChange={setWorkspaceDirty} onOpenAction={openActionFile} onPersisted={onWorkspacePersisted}/></div>
    </section>;
}
