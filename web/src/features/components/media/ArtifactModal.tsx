import { AlertTriangle, Trash2, Upload } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../../../api/client';
import { Modal } from '../../../components/Primitives';
import { displayError, useApp } from '../../../context/AppContext';
import { useApiData } from '../../../hooks/useApiData';
import type { ComponentArtifact, ComponentRelease } from '../../../types/domain';

export function ArtifactModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user, notify, signalRefresh } = useApp();
  const { data: environments, loading } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [mode, setMode] = useState<'upload' | 'register'>('upload');
  const [environmentId, setEnvironmentId] = useState('');
  const [alias, setAlias] = useState('media');
  const [sha256, setSha256] = useState('');
  const [filename, setFilename] = useState('');
  const [sourceUrl, setSourceUrl] = useState('');
  const [file, setFile] = useState<File>();
  const [checksumFile, setChecksumFile] = useState<File>();
  const [artifacts, setArtifacts] = useState<ComponentArtifact[]>(release.artifacts ?? []);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!environmentId && environments?.length) {
      setEnvironmentId(environments.find((item) => item.currentRevision?.variables.FILE_STATION)?.id ?? environments[0].id);
    }
  }, [environmentId, environments]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    try {
      let saved: ComponentArtifact;
      if (mode === 'upload') {
        if (!file) throw new Error('请选择介质文件。');
        saved = await api.uploadArtifact(release.id, { environmentId, alias, sha256: sha256.trim() || undefined, artifact: file, checksumFile });
      } else {
        saved = await api.registerArtifact(release.id, { alias, filename, sourceUrl, sha256 });
      }
      setArtifacts((items) => [saved, ...items.filter((item) => item.alias !== saved.alias)].sort((a, b) => a.alias.localeCompare(b.alias)));
      notify('success', '组件介质已保存', `${saved.alias} · sha256:${saved.sha256}`);
      signalRefresh('components');
    } catch (reason) {
      notify('error', '保存组件介质失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function loadChecksum(next?: File) {
    setChecksumFile(next);
    if (!next) return;
    const value = (await next.text()).trim().split(/\s+/)[0] ?? '';
    setSha256(value);
  }

  async function detach(item: ComponentArtifact) {
    setBusy(true);
    try {
      await api.deleteArtifact(release.id, item.alias);
      setArtifacts((items) => items.filter((artifact) => artifact.alias !== item.alias));
      notify('success', '介质引用已移除', 'file-station 上的物理文件未删除。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '移除介质引用失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function repairSource(item: ComponentArtifact) {
    const next = window.prompt(`更新 ${item.alias} 的来源 URL（内容必须仍为 sha256:${item.sha256}）`, item.sourceUrl)?.trim();
    if (!next || next === item.sourceUrl) return;
    setBusy(true);
    try {
      const updated = await api.updateArtifactSource(release.id, item.alias, next);
      setArtifacts((items) => items.map((artifact) => artifact.alias === updated.alias ? updated : artifact));
      notify('success', '介质来源已更新', '内容身份、Release 状态和历史证据保持不变。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '更新介质来源失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const station = selectedEnvironment?.currentRevision?.variables.FILE_STATION;
  return <Modal title={`组件介质 · ${release.version}`} description="文件名和 SHA-256 是内容身份；来源 URL 可在任何 Release 状态下修复。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      {release.state === 'draft' ? <form onSubmit={(event) => void submit(event)}>
        <div className="tabs" role="tablist"><button type="button" className={mode === 'upload' ? 'active' : ''} onClick={() => setMode('upload')}>上传文件</button><button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>登记已有路径</button></div>
        <div className="form-grid image-build-form">
          {mode === 'upload' ? <label className="span-2"><span>上传到环境 FSS</span><select required value={environmentId} disabled={loading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{station ? `上传入口：http://${station}/api/v1/files；访问路径限制：未知（当前文件站未提供可读取的限制信息）` : selectedEnvironment ? '该环境尚未配置 FILE_STATION，请联系环境 Owner。' : '正在读取可用环境…'}</small></label> : null}
          <label><span>介质别名</span><input required pattern="[a-z][a-z0-9_]*" value={alias} onChange={(event) => setAlias(event.target.value.toLowerCase())} /><small>运行时注入 alias_path、alias_url、alias_sha256</small></label>
          {mode === 'register' ? <><label><span>文件名</span><input required value={filename} placeholder="example.tar.gz" onChange={(event) => setFilename(event.target.value)} /></label><label className="span-2"><span>来源 URL</span><input required value={sourceUrl} placeholder="https://files.example/example.tar.gz" onChange={(event) => setSourceUrl(event.target.value)} /></label></> : <label><span>介质文件</span><input type="file" required onChange={(event) => setFile(event.target.files?.[0])} /></label>}
          <label><span>SHA-256</span><input required={!checksumFile} value={sha256} pattern="[A-Fa-f0-9]{64}" placeholder="64 位十六进制" onChange={(event) => setSha256(event.target.value)} /></label>
          <label><span>SHA-256 文件</span><input type="file" accept=".sha256,text/plain" onChange={(event) => void loadChecksum(event.target.files?.[0])} /><small>可上传常见的 “hash 文件名” 格式</small></label>
        </div>
        <button disabled={busy || (mode === 'upload' ? (!environmentId || !station || !file) : (!filename || !sourceUrl))} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '保存中…' : mode === 'upload' ? '上传并校验' : '探测并登记'}</button>
      </form> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>已发布内容身份不可修改</strong><p>仍可在右侧为同一 SHA-256 修复来源 URL。</p></div></div>}
      <section className="image-build-results">
        <div className="image-build-history"><strong>当前介质</strong>{artifacts.length ? artifacts.map((item) => <div key={item.alias}><span>{item.alias}</span><button type="button" className="icon-text" disabled={busy} onClick={() => void repairSource(item)}>修复来源</button>{release.state === 'draft' ? <button type="button" className="icon-button icon-button--danger" disabled={busy} aria-label={`移除介质 ${item.alias}`} onClick={() => void detach(item)}><Trash2 size={14} /></button> : null}</div>) : <span>尚未录入介质</span>}</div>
        {artifacts.length > 0 && <div className="image-build-detail"><div className="image-build-ref"><span>内容身份与当前来源</span>{artifacts.map((item) => <div key={item.id}><span>{item.alias} · {item.filename}</span><code>{item.sourceUrl}</code><code>sha256:{item.sha256} · {item.sizeBytes} bytes</code></div>)}</div></div>}
      </section>
    </div>
    <footer className="modal-actions"><button className="button button--quiet" type="button" onClick={onClose}>关闭</button></footer>
  </Modal>;
}
