import { Upload } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api } from '../../../api/client';
import { displayError } from '../../../context/AppContext';
import type { PlaybookWorkspace } from '../../../types/domain';
import { formatBytes, isActionEntry } from '../model';

export function PlaybookWorkspaceEditor({ releaseId, refreshToken, notify }: { releaseId: string; refreshToken: string; notify: (tone: 'success' | 'error' | 'info', title: string, message?: string) => void }) {
  const [workspace, setWorkspace] = useState<PlaybookWorkspace>();
  const [selectedPath, setSelectedPath] = useState('');
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
  const [loadedSHA256, setLoadedSHA256] = useState('');
  const [editable, setEditable] = useState(false);
  const [newPath, setNewPath] = useState('templates/example.j2');
  const [busy, setBusy] = useState(false);
  const isReferencedEntry = (path: string) => workspace?.references ? Boolean(workspace.references[path]?.protectionReason) : isActionEntry(path);
  const references = workspace?.references?.[selectedPath];
  const selected = workspace?.files.find((file) => file.path === selectedPath);

  async function refresh(select?: string) {
    const next = await api.playbookWorkspace(releaseId);
    const activePath = select ?? selectedPath;
    const remote = activePath ? next.files.find((file) => file.path === activePath) : undefined;
    const remoteChanged = (remote?.sha256 ?? '') !== loadedSHA256;
    const reloadFile = Boolean(select) || remoteChanged;
    if (reloadFile && content !== savedContent && !window.confirm('辅助文件有未保存内容。是否放弃本地内容并重新载入？')) return;
    if (reloadFile) {
      if (remote) {
        const file = await api.workspaceFile(releaseId, activePath);
        setContent(file.content ?? ''); setSavedContent(file.content ?? ''); setLoadedSHA256(file.sha256);
        setSelectedPath(activePath); setEditable(file.editable === true);
      } else {
        setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
        setEditable(false);
      }
    }
    setWorkspace(next);
    if (select && next.files.some((file) => file.path === select)) setSelectedPath(select);
    else if (selectedPath && !next.files.some((file) => file.path === selectedPath)) {
      setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
    }
  }

  useEffect(() => {
    void refresh().catch((reason) => notify('error', '读取工作区失败', displayError(reason)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [releaseId, refreshToken]);

  async function openFile(path: string) {
    if (content !== savedContent && !window.confirm('辅助文件有未保存内容，确认放弃？')) return;
    setBusy(true);
    try {
      const file = await api.workspaceFile(releaseId, path);
      setSelectedPath(path);
      setContent(file.content ?? '');
      setSavedContent(file.content ?? '');
      setLoadedSHA256(file.sha256);
      setEditable(file.editable === true);
    } catch (reason) { notify('error', '读取文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function createTextFile() {
    if (!newPath.trim()) return;
    setBusy(true);
    try {
      await api.saveWorkspaceFile(releaseId, newPath.trim(), '', '', workspace?.treeSha256 ?? '');
      await refresh(newPath.trim());
      notify('success', '文件已创建', newPath.trim());
    } catch (reason) { notify('error', '创建文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function createDirectory() {
    const directory = newPath.trim().replace(/\/+$/, '');
    if (!directory) return;
    setBusy(true);
    try {
      const marker = `${directory}/.gitkeep`;
      await api.saveWorkspaceFile(releaseId, marker, '', '', workspace?.treeSha256 ?? '');
      await refresh(marker);
      notify('success', '目录已创建', directory);
    } catch (reason) { notify('error', '创建目录失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function saveSelected() {
    if (!selectedPath) return;
    setBusy(true);
    try {
      if (!selected) return;
      const saved = await api.saveWorkspaceFile(releaseId, selectedPath, content, loadedSHA256, workspace?.treeSha256 ?? '');
      setSavedContent(content);
      setLoadedSHA256(saved.sha256);
      setWorkspace(await api.playbookWorkspace(releaseId));
      notify('success', '辅助文件已保存', selectedPath);
    } catch (reason) { notify('error', '保存文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function upload(file?: File) {
    if (!file) return;
    const path = newPath.trim() || file.name;
    setBusy(true);
    try {
      const existingSHA256 = workspace?.files.find((item) => item.path === path)?.sha256 ?? '';
      await api.uploadWorkspaceFile(releaseId, path, file, existingSHA256, workspace?.treeSha256 ?? '');
      await refresh(path);
      notify('success', '文件已上传', path);
    } catch (reason) { notify('error', '上传文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function renameSelected() {
    if (!selectedPath || isReferencedEntry(selectedPath)) return;
    const next = window.prompt('输入新的工作区相对路径', selectedPath)?.trim();
    if (!next || next === selectedPath) return;
    setBusy(true);
    try {
      if (!selected) return;
      const updated = await api.renameWorkspaceFile(releaseId, selectedPath, next, loadedSHA256, workspace?.treeSha256 ?? '');
      setWorkspace(updated); setSelectedPath(next);
      notify('success', '文件已改名', `${selectedPath} → ${next}`);
    } catch (reason) { notify('error', '改名失败', displayError(reason)); } finally { setBusy(false); }
  }

  async function deleteSelected() {
    if (!selectedPath || isReferencedEntry(selectedPath) || !window.confirm(`确认删除 ${selectedPath}？`)) return;
    setBusy(true);
    try {
      if (!selected) return;
      setWorkspace(await api.deleteWorkspaceFile(releaseId, selectedPath, loadedSHA256, workspace?.treeSha256 ?? ''));
      setSelectedPath(''); setContent(''); setSavedContent(''); setLoadedSHA256('');
      notify('success', '文件已删除');
    } catch (reason) { notify('error', '删除文件失败', displayError(reason)); } finally { setBusy(false); }
  }

  return <section className="workspace-browser">
    <header><div><h4>Ansible 工作区</h4><p><code>{workspace?.root ?? 'managed/…'}</code> · {workspace?.files.length ?? 0}/1000 文件 · 树摘要 <code>{workspace?.treeSha256?.slice(0, 12) || '—'}</code></p></div><button type="button" className="button button--quiet" disabled={busy} onClick={() => void refresh()}>刷新</button></header>
    <div className="workspace-browser__create"><input aria-label="工作区相对路径" value={newPath} onChange={(event) => setNewPath(event.target.value)} placeholder="templates/config.j2" /><button type="button" className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createDirectory()}>新建目录</button><button type="button" className="button button--quiet" disabled={busy || !newPath.trim()} onClick={() => void createTextFile()}>新建文本文件</button><label className="button button--quiet"><Upload size={14} /> 上传/替换<input type="file" disabled={busy} onChange={(event) => { void upload(event.target.files?.[0]); event.currentTarget.value = ''; }} /></label></div>
    <div className="workspace-browser__body">
      <nav aria-label="Ansible 工作区文件树">{workspace?.files.map((file) => <button type="button" key={file.path} disabled={busy} className={file.path === selectedPath ? 'active' : ''} style={{ paddingLeft: `${12 + Math.max(0, file.path.split('/').length - 1) * 14}px` }} onClick={() => void openFile(file.path)}><span>{file.path}</span><small>{formatBytes(file.sizeBytes)}{isReferencedEntry(file.path) ? ' · 动作入口' : ''}</small></button>)}{workspace && !workspace.files.length ? <p>工作区尚无文件。</p> : null}</nav>
      <div className="workspace-browser__editor">{selected ? <><div className="workspace-browser__selection"><strong>{selected.path}</strong><span>{selected.mediaType} · {formatBytes(selected.sizeBytes)}</span></div>{references && <aside className="workspace-references"><strong>{references.actions.length ? '引用动作' : '没有直接引用的动作'}</strong>{references.actions.map(action => <p key={action.actionId}>{action.actionName}{action.usedAs.length ? ` · ${action.usedAs.join("、")}` : ''}</p>)}{references.staticReferences.length > 0 && <p>文件引用：{references.staticReferences.join("、")}</p>}<small>动态路径引用无法完全判断；删除前请核对脚本。</small></aside>}{!editable ? <p>该文件较大或不是 UTF-8 文本，只能下载、替换或删除。</p> : <textarea className="code-editor" aria-label="辅助文件在线编辑器" disabled={busy || isReferencedEntry(selected.path)} spellCheck={false} value={content} onChange={(event) => setContent(event.target.value)} />}<div className="playbook-source__actions"><a className="button button--quiet" href={api.workspaceFileDownloadURL(releaseId, selected.path)}>下载</a>{!isReferencedEntry(selected.path) ? <><button type="button" className="button button--quiet" disabled={busy} onClick={() => void renameSelected()}>改名</button><button type="button" className="icon-text icon-text--danger" disabled={busy} onClick={() => void deleteSelected()}>删除</button></> : null}{editable && !isReferencedEntry(selected.path) ? <button type="button" className="button button--secondary" disabled={busy || content === savedContent} onClick={() => void saveSelected()}>保存辅助文件</button> : null}</div></> : <p>从左侧选择文件。动作入口请在上方生命周期编辑器维护。</p>}</div>
    </div>
  </section>;
}
