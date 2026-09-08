import { Upload as AntUpload } from 'antd';
import { useModalBusy } from '../../../components/ModalBusyContext';
import { useDialogs } from '../../../components/UIProvider';
import { Button, Input } from 'antd';
import { ChevronDown, ChevronRight, File, Folder, FolderOpen, Trash2, Upload } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api } from '../../../api/client';
import { displayError } from '../../../context/AppContext';
import type { PlaybookWorkspace } from '../../../types/domain';
import { formatBytes, isActionEntry } from '../model';
type WorkspaceTreeNode = {
    name: string;
    path: string;
    file?: PlaybookWorkspace['files'][number];
    children: Map<string, WorkspaceTreeNode>;
};
function buildWorkspaceTree(files: PlaybookWorkspace['files']) {
    const root = new Map<string, WorkspaceTreeNode>();
    for (const file of files) {
        const segments = file.path.split('/');
        let children = root;
        segments.forEach((name, index) => {
            const isFile = index === segments.length - 1;
            // Keep the parent folders of an empty-directory marker without showing the marker.
            if (isFile && name === '.gitkeep' && file.sizeBytes === 0)
                return;
            let node = children.get(name);
            if (!node) {
                node = { name, path: segments.slice(0, index + 1).join('/'), children: new Map() };
                children.set(name, node);
            }
            if (isFile)
                node.file = file;
            children = node.children;
        });
    }
    return root;
}
export function PlaybookWorkspaceEditor({ releaseId, refreshToken, notify, onDirtyChange, onOpenAction, onPersisted, disabled = false }: {
    releaseId: string;
    refreshToken: string;
    notify: (tone: 'success' | 'error' | 'info', title: string, message?: string) => void;
    onDirtyChange?: (dirty: boolean) => void;
    onOpenAction?: (path: string) => Promise<boolean>;
    onPersisted?: () => Promise<void>;
    disabled?: boolean;
}) {
    const { confirm, prompt } = useDialogs();
    const [workspace, setWorkspace] = useState<PlaybookWorkspace>();
    const readRequest = useRef(0);
    const [selectedPath, setSelectedPath] = useState('');
    const [content, setContent] = useState('');
    const [savedContent, setSavedContent] = useState('');
    const [loadedSHA256, setLoadedSHA256] = useState('');
    const [editable, setEditable] = useState(false);
    const [newPath, setNewPath] = useState('');
    const [working, setBusy] = useState(false);
    const busy = working || disabled;
    useModalBusy(working);
    const [collapsedDirectories, setCollapsedDirectories] = useState<Set<string>>(new Set());
    const isReferencedEntry = (path: string) => isActionEntry(path) || Boolean(workspace?.references?.[path]?.protectionReason);
    const references = workspace?.references?.[selectedPath];
    const selected = workspace?.files.find((file) => file.path === selectedPath);
    const tree = useMemo(() => buildWorkspaceTree(workspace?.files ?? []), [workspace?.files]);
    useEffect(() => onDirtyChange?.(content !== savedContent), [content, savedContent, onDirtyChange]);
    function revealPath(path: string) {
        setCollapsedDirectories((previous) => {
            const next = new Set(previous);
            const segments = path.split('/');
            segments.forEach((_, index) => next.delete(segments.slice(0, index + 1).join('/')));
            return next;
        });
    }
    function toggleDirectory(path: string) {
        setCollapsedDirectories((previous) => {
            const next = new Set(previous);
            if (next.has(path))
                next.delete(path);
            else
                next.add(path);
            return next;
        });
    }
    async function refresh(select?: string) {
        const request = ++readRequest.current;
        setBusy(true);
        try {
            const next = await api.playbookWorkspace(releaseId);
            if (request !== readRequest.current) return;
            const activePath = select ?? selectedPath;
            const remote = activePath ? next.files.find((file) => file.path === activePath) : undefined;
            const remoteChanged = (remote?.sha256 ?? '') !== loadedSHA256;
            const reloadFile = Boolean(select) || remoteChanged;
            if (reloadFile && content !== savedContent && !await confirm('辅助文件有未保存内容。是否放弃本地内容并重新载入？'))
                return;
            if (reloadFile) {
                if (remote) {
                    const file = await api.workspaceFile(releaseId, activePath);
                    if (request !== readRequest.current) return;
                    setContent(file.content ?? '');
                    setSavedContent(file.content ?? '');
                    setLoadedSHA256(file.sha256);
                    setSelectedPath(activePath);
                    setEditable(file.editable === true);
                }
                else {
                    setSelectedPath('');
                    setContent('');
                    setSavedContent('');
                    setLoadedSHA256('');
                    setEditable(false);
                }
            }
            setWorkspace(next);
            if (select && next.files.some((file) => file.path === select)) {
                setSelectedPath(select);
                revealPath(select);
            }
            else if (selectedPath && !next.files.some((file) => file.path === selectedPath)) {
                setSelectedPath('');
                setContent('');
                setSavedContent('');
                setLoadedSHA256('');
            }
        }
        catch (reason) {
            if (request === readRequest.current) throw reason;
        }
        finally {
            if (request === readRequest.current) setBusy(false);
        }
    }
    useEffect(() => {
        void refresh().catch((reason) => notify('error', '读取工作区失败', displayError(reason)));
        return () => { readRequest.current += 1; };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [releaseId, refreshToken]);
    useEffect(() => { setCollapsedDirectories(new Set()); }, [releaseId]);
    async function openFile(path: string) {
        if (path === selectedPath && !isReferencedEntry(path)) return;
        if (content !== savedContent && !await confirm('辅助文件有未保存内容，确认放弃？'))
            return;
        if (isReferencedEntry(path) && onOpenAction) {
            if (await onOpenAction(path)) {
                setContent(savedContent);
                return;
            }
            return;
        }
        const request = ++readRequest.current;
        setBusy(true);
        try {
            const file = await api.workspaceFile(releaseId, path);
            if (request !== readRequest.current) return;
            setSelectedPath(path);
            setContent(file.content ?? '');
            setSavedContent(file.content ?? '');
            setLoadedSHA256(file.sha256);
            setEditable(file.editable === true);
        }
        catch (reason) {
            if (request === readRequest.current) notify('error', '读取文件失败', displayError(reason));
        }
        finally {
            if (request === readRequest.current) setBusy(false);
        }
    }
    async function createTextFile() {
        if (!newPath.trim())
            return;
        setBusy(true);
        try {
            await api.saveWorkspaceFile(releaseId, newPath.trim(), '', '', workspace?.treeSha256 ?? '');
            await onPersisted?.();
            await refresh(newPath.trim());
            notify('success', '文件已创建', newPath.trim());
        }
        catch (reason) {
            notify('error', '创建文件失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function createDirectory() {
        const directory = newPath.trim().replace(/\/+$/, '');
        if (!directory)
            return;
        setBusy(true);
        try {
            const marker = `${directory}/.gitkeep`;
            await api.saveWorkspaceFile(releaseId, marker, '', '', workspace?.treeSha256 ?? '');
            await onPersisted?.();
            await refresh();
            revealPath(directory);
            notify('success', '目录已创建', directory);
        }
        catch (reason) {
            notify('error', '创建目录失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function saveSelected() {
        if (!selectedPath)
            return;
        setBusy(true);
        try {
            if (!selected)
                return;
            const saved = await api.saveWorkspaceFile(releaseId, selectedPath, content, loadedSHA256, workspace?.treeSha256 ?? '');
            setSavedContent(content);
            setLoadedSHA256(saved.sha256);
            await onPersisted?.();
            setWorkspace(await api.playbookWorkspace(releaseId));
            notify('success', '辅助文件已保存', selectedPath);
        }
        catch (reason) {
            notify('error', '保存文件失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function upload(file?: File) {
        if (!file)
            return;
        const path = newPath.trim() || file.name;
        setBusy(true);
        try {
            const existingSHA256 = workspace?.files.find((item) => item.path === path)?.sha256 ?? '';
            await api.uploadWorkspaceFile(releaseId, path, file, existingSHA256, workspace?.treeSha256 ?? '');
            await onPersisted?.();
            await refresh(path);
            notify('success', '文件已上传', path);
        }
        catch (reason) {
            notify('error', '上传文件失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function renameSelected() {
        if (!selectedPath || isReferencedEntry(selectedPath))
            return;
        const next = (await prompt('输入新的工作区相对路径', selectedPath))?.trim();
        if (!next || next === selectedPath)
            return;
        setBusy(true);
        try {
            if (!selected)
                return;
            const updated = await api.renameWorkspaceFile(releaseId, selectedPath, next, loadedSHA256, workspace?.treeSha256 ?? '');
            setWorkspace(updated);
            await onPersisted?.();
            setSelectedPath(next);
            revealPath(next);
            notify('success', '文件已改名', `${selectedPath} → ${next}`);
        }
        catch (reason) {
            notify('error', '改名失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function deleteSelected() {
        if (!selectedPath || isReferencedEntry(selectedPath) || !await confirm(`确认删除 ${selectedPath}？`))
            return;
        setBusy(true);
        try {
            if (!selected)
                return;
            setWorkspace(await api.deleteWorkspaceFile(releaseId, selectedPath, loadedSHA256, workspace?.treeSha256 ?? ''));
            await onPersisted?.();
            setSelectedPath('');
            setContent('');
            setSavedContent('');
            setLoadedSHA256('');
            notify('success', '文件已删除');
        }
        catch (reason) {
            notify('error', '删除文件失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    function directoryFiles(path: string) {
        return workspace?.files.filter(file => file.path.startsWith(`${path}/`)) ?? [];
    }
    async function deleteDirectory(path: string) {
        if (busy || !workspace)
            return;
        const files = directoryFiles(path);
        if (files.some(file => isReferencedEntry(file.path)))
            return;
        const visible = files.filter(file => !file.path.endsWith('/.gitkeep') || file.sizeBytes !== 0);
        const unsaved = selectedPath.startsWith(`${path}/`) && content !== savedContent;
        const details = visible.slice(0, 10).map(file => file.path).join('\n');
        if (!await confirm(`确认删除目录“${path}”及其中的全部内容？\n包含 ${visible.length} 个文件${details ? `：\n${details}` : '。'}${visible.length > 10 ? '\n…' : ''}${unsaved ? '\n当前文件有未保存内容，将一并放弃。' : ''}\n\n此操作不可恢复；请先核对脚本中的动态路径引用。`))
            return;
        setBusy(true);
        try {
            const updated = await api.deleteWorkspaceDirectory(releaseId, path, workspace.treeSha256);
            setWorkspace(updated);
            await onPersisted?.();
            if (selectedPath.startsWith(`${path}/`)) {
                setSelectedPath('');
                setContent('');
                setSavedContent('');
                setLoadedSHA256('');
                setEditable(false);
            }
            notify('success', '目录已删除', path);
        }
        catch (reason) {
            notify('error', '删除目录失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    function renderTree(nodes: Map<string, WorkspaceTreeNode>, depth = 0): ReactNode {
        const sorted = [...nodes.values()].sort((left, right) => Number(Boolean(left.file)) - Number(Boolean(right.file))
            || left.name.localeCompare(right.name, 'zh-CN', { numeric: true }));
        return <ul className="workspace-tree">
      {sorted.map((node) => {
                const expanded = !collapsedDirectories.has(node.path);
                return <li key={node.path}>
          {node.file ? <button type="button" disabled={busy} className={`workspace-tree__row${node.path === selectedPath ? ' active' : ''}`} style={{ paddingLeft: 12 + depth * 16 }} title={node.path} aria-label={node.path} aria-current={node.path === selectedPath ? 'true' : undefined} onClick={() => void openFile(node.path)}>
            <span className="workspace-tree__chevron" aria-hidden="true"/>
            <File size={14} aria-hidden="true"/>
            <span className="workspace-tree__name">{node.name}</span>
            <small>{isReferencedEntry(node.path) ? '动作入口' : formatBytes(node.file.sizeBytes)}</small>
          </button> : <>
            <div className="workspace-tree__directory-actions">
            <button type="button" disabled={busy} className="workspace-tree__row workspace-tree__directory" style={{ paddingLeft: 12 + depth * 16 }} title={node.path} aria-label={node.path} aria-expanded={expanded} onClick={() => toggleDirectory(node.path)}>
              {expanded ? <ChevronDown size={12} aria-hidden="true"/> : <ChevronRight size={12} aria-hidden="true"/>}
              {expanded ? <FolderOpen size={14} aria-hidden="true"/> : <Folder size={14} aria-hidden="true"/>}
              <span className="workspace-tree__name">{node.name}</span>
              {!node.children.size && <small>空目录</small>}
            </button>
            <button type="button" className="workspace-tree__delete" aria-label={`删除目录 ${node.path}`} disabled={busy || directoryFiles(node.path).some(file => isReferencedEntry(file.path))} title={directoryFiles(node.path).some(file => isReferencedEntry(file.path)) ? '目录包含动作入口，请先删除对应动作' : '删除目录及全部内容'} onClick={() => void deleteDirectory(node.path)}><Trash2 size={13} aria-hidden="true"/></button>
            </div>
            {expanded && node.children.size > 0 && renderTree(node.children, depth + 1)}
          </>}
        </li>;
            })}
    </ul>;
    }
    return <section className="workspace-browser">
    <header><div><h4>Ansible 工作区 <small>{workspace?.files.length ?? 0} 个文件</small></h4><p><code>{workspace?.root ?? '正在读取工作区…'}</code></p></div><Button className="button button--quiet" disabled={busy} onClick={() => void refresh().catch(reason => notify('error', '读取工作区失败', displayError(reason)))} htmlType={"button"} type="default">刷新</Button></header>
    <div className="workspace-browser__create"><Input disabled={busy} aria-label="工作区相对路径" value={newPath} onChange={(event) => setNewPath(event.target.value)} placeholder="输入新文件或上传目标路径，例如 templates/config.j2"/><Button className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createDirectory()} htmlType={"button"} type="default">新建目录</Button><Button className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createTextFile()} htmlType={"button"} type="default">新建文本文件</Button><AntUpload showUploadList={false} disabled={busy} beforeUpload={selectedUpload => { void upload(selectedUpload); return false;}}><Button disabled={busy}><Upload size={14}/>上传/替换</Button></AntUpload></div>
    <div className="workspace-browser__body">
      <nav aria-label="Ansible 工作区文件树">{tree.size ? renderTree(tree) : workspace ? <p>工作区尚无文件。</p> : null}</nav>
      <div className="workspace-browser__editor">{selected ? <><div className="workspace-browser__selection"><strong>{selected.path}</strong><span>{content !== savedContent ? '有未保存内容' : '内容已保存'} · {formatBytes(selected.sizeBytes)}</span></div>{references && <aside className="workspace-references"><strong>{references.actions.length ? '引用动作' : '没有直接引用的动作'}</strong>{references.actions.map(action => <p key={action.actionId}>{action.actionName}{action.usedAs.length ? ` · ${action.usedAs.join("、")}` : ''}</p>)}{references.staticReferences.length > 0 && <p>文件引用：{references.staticReferences.join("、")}</p>}<small>动态路径引用无法完全判断；删除前请核对脚本。</small></aside>}{!editable ? <p>该文件较大或不是 UTF-8 文本，只能下载、替换或删除。</p> : <Input.TextArea className="code-editor" aria-label="辅助文件在线编辑器" disabled={busy || isReferencedEntry(selected.path)} spellCheck={false} value={content} onChange={(event) => setContent(event.target.value)}/>}<div className="playbook-source__actions"><Button className="button button--quiet" href={api.workspaceFileDownloadURL(releaseId, selected.path)} type="default">下载</Button>{!isReferencedEntry(selected.path) ? <><Button className="button button--quiet" disabled={busy} onClick={() => void renameSelected()} htmlType={"button"} type="default">改名</Button><Button className="icon-text icon-text--danger" disabled={busy} onClick={() => void deleteSelected()} htmlType={"button"} type="text" danger>删除</Button></> : null}{editable && !isReferencedEntry(selected.path) ? <Button className="button button--secondary" disabled={busy || content === savedContent} onClick={() => void saveSelected()} htmlType={"button"} type="default">保存辅助文件</Button> : null}</div></> : <div className="playbook-entry-empty"><FolderOpen size={32}/><h4>选择工作区文件</h4><p>在左侧选择模板、脚本或其他文件进行编辑。</p><small>点击动作入口文件会跳转到对应动作。</small></div>}</div>
    </div>
  </section>;
}
