import { AlertTriangle, Trash2, Upload } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../../../api/client';
import { EmptyState, Modal, StatusPill } from '../../../components/Primitives';
import { displayError, useApp } from '../../../context/AppContext';
import { useApiData } from '../../../hooks/useApiData';
import type { ComponentImage, ComponentImageBuild, ComponentRelease } from '../../../types/domain';

const ACTIVE_IMAGE_BUILD_STATUSES = new Set(['queued', 'running']);

export function ImageBuildModal({ release, onClose }: { release: ComponentRelease; onClose: () => void }) {
  const { user, notify, signalRefresh } = useApp();
  const { data: environments, loading: environmentsLoading } = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const [mode, setMode] = useState<'build' | 'register'>('build');
  const [tag, setTag] = useState(release.version.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'latest');
  const [environmentId, setEnvironmentId] = useState('');
  const [dockerfile, setDockerfile] = useState<File>();
  const [logicalName, setLogicalName] = useState('main');
  const [sourceRef, setSourceRef] = useState('');
  const [expectedDigest, setExpectedDigest] = useState('');
  const [images, setImages] = useState<ComponentImage[]>(release.images ?? []);
  const [builds, setBuilds] = useState<ComponentImageBuild[]>([]);
  const [selectedBuild, setSelectedBuild] = useState<ComponentImageBuild>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!environmentId && environments?.length) {
      setEnvironmentId(environments.find((item) => item.currentRevision?.variables.IMAGE_REGISTRY)?.id ?? environments[0].id);
    }
  }, [environmentId, environments]);

  useEffect(() => {
    let active = true;
    void api.imageBuilds(release.id).then((items) => {
      if (!active) return;
      setBuilds(items);
      setSelectedBuild(items[0]);
    }).catch((reason) => notify('error', '读取镜像构建记录失败', displayError(reason))).finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [notify, release.id]);

  useEffect(() => {
    if (!selectedBuild) return;
    const controller = new AbortController();
    const wasActive = ACTIVE_IMAGE_BUILD_STATUSES.has(selectedBuild.status);
    const refresh = () => {
      void api.imageBuild(selectedBuild.id, controller.signal).then((next) => {
        if (controller.signal.aborted) return;
        setSelectedBuild(next);
        setBuilds((current) => [next, ...current.filter((item) => item.id !== next.id)]);
        if (wasActive && next.status === 'succeeded') notify('success', '镜像已发布', next.imageDigest ?? next.imageRef);
        if (wasActive && (next.status === 'failed' || next.status === 'interrupted')) notify('error', '镜像构建失败', next.error ?? '请查看构建日志。');
      }).catch((reason) => {
        if (!controller.signal.aborted) notify('error', '刷新镜像构建状态失败', displayError(reason));
      });
    };
    refresh();
    const timer = wasActive ? window.setInterval(refresh, 1000) : undefined;
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [notify, selectedBuild?.id, selectedBuild?.status]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mode === 'register') {
      setBusy(true);
      try {
        const image = await api.registerImage(release.id, { logicalName, sourceRef, digest: expectedDigest.trim() || undefined });
        setImages((current) => [image, ...current.filter((item) => item.logicalName !== image.logicalName)].sort((a, b) => a.logicalName.localeCompare(b.logicalName)));
        notify('success', '镜像内容已登记', `${image.logicalName} · ${image.digest}`);
        signalRefresh('components');
      } catch (reason) {
        notify('error', '登记镜像失败', displayError(reason));
      } finally {
        setBusy(false);
      }
      return;
    }
    if (!environmentId) {
      notify('error', '请选择目标环境');
      return;
    }
    if (!dockerfile) {
      notify('error', '请选择 Dockerfile');
      return;
    }
    setBusy(true);
    try {
      const build = await api.startImageBuild(release.id, environmentId, dockerfile, tag);
      setBuilds((current) => [build, ...current]);
      setSelectedBuild(build);
      notify('success', '镜像构建已提交', build.imageRef);
    } catch (reason) {
      notify('error', '提交镜像构建失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function repairSource(image: ComponentImage) {
    const next = window.prompt(`更新 ${image.logicalName} 的来源 Ref（内容必须仍为 ${image.digest}）`, image.sourceRef)?.trim();
    if (!next || next === image.sourceRef) return;
    setBusy(true);
    try {
      const updated = await api.updateImageSource(release.id, image.logicalName, next);
      setImages((current) => current.map((item) => item.logicalName === updated.logicalName ? updated : item));
      notify('success', '镜像来源已更新', '内容 Digest、Release 状态和历史证据保持不变。');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '更新镜像来源失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  async function removeImage(image: ComponentImage) {
    setBusy(true);
    try {
      await api.deleteImage(release.id, image.logicalName);
      setImages((current) => current.filter((item) => item.logicalName !== image.logicalName));
      notify('success', '镜像内容已移除');
      signalRefresh('components');
    } catch (reason) {
      notify('error', '移除镜像失败', displayError(reason));
    } finally {
      setBusy(false);
    }
  }

  const selectedEnvironment = environments?.find((item) => item.id === environmentId);
  const registry = selectedEnvironment?.currentRevision?.variables.IMAGE_REGISTRY;
  const logs = selectedBuild?.logs?.map((item) => `[${item.stream}] ${item.message}`).join('\n') ?? '';
  return <Modal title={`组件镜像 · ${release.version}`} description="Digest 是内容身份；来源 Ref 可在任何 Release 状态下修复。" onClose={onClose} size="wide">
    <div className="image-build-layout">
      {release.state === 'draft' ? <form onSubmit={(event) => void submit(event)}>
        <div className="tabs" role="tablist"><button type="button" className={mode === 'build' ? 'active' : ''} onClick={() => setMode('build')}>Dockerfile 构建</button><button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>登记已有镜像</button></div>
        {mode === 'build' ? <>
          <div className="warning-callout"><AlertTriangle size={19} /><div><strong>Dockerfile 会在平台构建机上执行</strong><p>构建上下文仅包含该 Dockerfile，不接受本地目录或主机路径；请只上传可信内容。</p></div></div>
          <div className="form-grid image-build-form">
            <label className="span-2"><span>目标环境</span><select aria-label="目标环境" required value={environmentId} disabled={environmentsLoading} onChange={(event) => setEnvironmentId(event.target.value)}><option value="">请选择环境</option>{environments?.map((environment) => <option key={environment.id} value={environment.id}>{environment.name} · r{environment.currentRevision?.revision ?? '?'}</option>)}</select><small>{registry ? `IMAGE_REGISTRY=${registry}` : selectedEnvironment ? '该环境尚未配置 IMAGE_REGISTRY，请联系环境 Owner。' : '正在读取可用环境…'}</small></label>
            <label className="span-2"><span>Dockerfile</span><input aria-label="Dockerfile" type="file" required onChange={(event) => setDockerfile(event.target.files?.[0])} /><small>UTF-8，最大 1 MiB，必须包含 FROM 指令</small></label>
            <label className="span-2"><span>镜像标签</span><input value={tag} required pattern="[a-z0-9][a-z0-9._\-]{0,127}" onChange={(event) => setTag(event.target.value.toLowerCase())} /><small>镜像路径固定为 IMAGE_REGISTRY / components / 组件 slug : 标签</small></label>
          </div>
          <button disabled={busy || !dockerfile || !environmentId || !registry} className="button button--primary" type="submit"><Upload size={16} /> {busy ? '提交中…' : '上传并构建'}</button>
        </> : <>
          <div className="form-grid image-build-form">
            <label><span>逻辑名称</span><input required pattern="[a-z][a-z0-9_]{0,63}" value={logicalName} onChange={(event) => setLogicalName(event.target.value.toLowerCase())} /></label>
            <label className="span-2"><span>OCI 来源 Ref</span><input required value={sourceRef} placeholder="registry.example/team/image:tag" onChange={(event) => setSourceRef(event.target.value)} /></label>
            <label className="span-2"><span>预期 Digest（可选）</span><input value={expectedDigest} pattern="sha256:[a-f0-9]{64}" placeholder="sha256:…" onChange={(event) => setExpectedDigest(event.target.value.toLowerCase())} /><small>平台会拉取并解析不可变 Digest；填写后必须完全匹配。</small></label>
          </div>
          <button disabled={busy || !logicalName || !sourceRef} className="button button--primary" type="submit">{busy ? '登记中…' : '探测并登记'}</button>
        </>}
      </form> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>已发布内容身份不可修改</strong><p>仍可为同一 Digest 修复来源 Ref。</p></div></div>}
      <section className="image-build-results">
        <div className="image-build-history"><strong>当前内容</strong>{images.length ? images.map((image) => <div key={image.logicalName}><span>{image.logicalName}</span><button type="button" className="icon-text" disabled={busy} onClick={() => void repairSource(image)}>修复来源</button>{release.state === 'draft' ? <button type="button" className="icon-button icon-button--danger" disabled={busy} aria-label={`移除镜像 ${image.logicalName}`} onClick={() => void removeImage(image)}><Trash2 size={14} /></button> : null}</div>) : <span>尚未登记镜像内容</span>}</div>
        {images.length > 0 && <div className="image-build-detail"><div className="image-build-ref">{images.map((image) => <div key={image.id}><span>{image.logicalName}</span><code>{image.digest}</code><code>{image.sourceRef}</code></div>)}</div></div>}
        <div className="image-build-history"><strong>构建记录</strong>{loading ? <span>读取中…</span> : builds.length ? builds.map((build) => <button key={build.id} type="button" className={selectedBuild?.id === build.id ? 'active' : ''} onClick={() => setSelectedBuild(build)}><span>{build.imageTag}</span><StatusPill status={build.status} /></button>) : <span>暂无构建记录</span>}</div>
        {selectedBuild ? <div className="image-build-detail">
          <div className="image-build-ref"><span>目标镜像</span><code>{selectedBuild.imageRef}</code>{selectedBuild.environmentId ? <><span>环境快照</span><code>{selectedBuild.environmentId} · {selectedBuild.environmentRevisionId ?? '历史记录未保存 Revision'}</code></> : null}{selectedBuild.imageDigest ? <code>{selectedBuild.imageDigest}</code> : null}{selectedBuild.error ? <p>{selectedBuild.error}</p> : null}</div>
          <details className="image-build-log-expander"><summary>查看构建日志</summary><pre aria-label="镜像构建日志">{logs || (ACTIVE_IMAGE_BUILD_STATUSES.has(selectedBuild.status) ? '等待构建日志…' : '无日志')}</pre></details>
        </div> : <EmptyState title="尚未选择构建" description="上传 Dockerfile 后可在这里查看实时日志与镜像摘要。" />}
      </section>
    </div>
    <footer className="modal-actions"><button className="button button--quiet" type="button" onClick={onClose}>关闭</button></footer>
  </Modal>;
}
