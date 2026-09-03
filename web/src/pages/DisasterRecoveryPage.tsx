import { useState, type FormEvent } from 'react';
import { AlertTriangle, GitBranch, GitCompare, Plus, RotateCcw, Save } from 'lucide-react';
import { api } from '../api/client';
import { ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice, formatTime } from '../components/Primitives';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { CatalogRepositoryStatus, CatalogRestorePlan } from '../types/domain';

export function DisasterRecoveryPage() {
  return <div className="page">
    <PageHeader eyebrow="Catalog disaster recovery" title="灾备目录" description="集中管理发布目录的私有 Git 仓库、定时备份状态与空库恢复点。" />
    <CatalogRepositoryPanel />
  </div>;
}

function CatalogRepositoryPanel() {
  const { notify, signalRefresh } = useApp();
  const { data: repository, loading, error, isRefreshing, reload } = useApiData<CatalogRepositoryStatus>((signal) => api.catalogRepository(signal), [], 'catalog-repository');
  const [repositoryMode, setRepositoryMode] = useState<'create' | 'connect' | 'restore'>();
  const [restoreRepository, setRestoreRepository] = useState<CatalogRepositoryStatus>();
  const [backupBusy, setBackupBusy] = useState(false);
  const repositoryEnabled = repository?.enabled === true;

  function updated(status: CatalogRepositoryStatus, message: string, continueToRestore = false) {
    notify('success', message, `${status.path} · ${status.branch}`);
    setRepositoryMode(undefined);
    if (continueToRestore) {
      if (!status.restoreTargetKnown || !status.targetCatalogEmpty) {
        notify('info', '仓库已接入，暂不能恢复', '当前数据库不是可确认的空目录，请清空组件和场景后再恢复。');
      } else if (!status.recoveryPoints.length) {
        notify('info', '仓库已接入，暂无恢复点', '该仓库没有可用的远端 backup/* 标签。');
      } else {
        setRestoreRepository(status);
      }
    }
    signalRefresh(['catalog-repository', 'workbench']);
    void reload();
  }

  async function backupNow() {
    setBackupBusy(true);
    try {
      const result = await api.createCatalogBackup();
      notify('success', 'Git 恢复点已创建', `${result.gitTag} · Commit ${result.gitCommit.slice(0, 16)}…`);
      signalRefresh(['catalog-repository', 'workbench']);
      await reload();
    } catch (reason) {
      notify('error', '立即备份失败', displayError(reason));
    } finally {
      setBackupBusy(false);
    }
  }

  return <article className="panel catalog-repository-panel" id="catalog-repository">
    <header className="panel__header catalog-repository-panel__header">
      <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>发布目录灾备</h2><p>自动与人工恢复点统一写入所选私有 Git 仓库；恢复只允许组件和场景均为空的数据库。</p></div></div>
      <div className="catalog-repository-panel__toolbar" role="group" aria-label="灾备操作">
        <button
          className={`button ${repository?.configured || !repository?.targetCatalogEmpty ? 'button--quiet' : 'button--danger'}`}
          disabled={!repositoryEnabled || backupBusy}
          title={repository && !repository.enabled ? repository.reason : undefined}
          onClick={() => setRepositoryMode(repository?.configured || !repository?.targetCatalogEmpty ? 'connect' : 'restore')}
        >{repository?.configured ? '更换备份仓库' : repository?.targetCatalogEmpty ? '从已有 Git 仓库恢复' : '接入已有备份仓库'}</button>
        <button className="button button--secondary" disabled={!repositoryEnabled || backupBusy} title={repository && !repository.enabled ? repository.reason : undefined} onClick={() => setRepositoryMode('create')}><Plus size={15} /> 创建私有仓库</button>
        <button className="button button--primary" disabled={!repositoryEnabled || !repository?.configured || backupBusy} title={!repositoryEnabled ? repository?.reason : !repository?.configured ? '请先创建或接入私有仓库' : undefined} onClick={() => void backupNow()}>{backupBusy ? '正在备份…' : <><Save size={15} /> 立即备份</>}</button>
      </div>
    </header>
    <section className="catalog-backup-policy" aria-labelledby="catalog-backup-policy-title">
      <div className="catalog-backup-policy__heading"><h3 id="catalog-backup-policy-title">备份策略</h3><p>最近一次成功恢复点才是平台承诺可恢复的版本。</p></div>
      <div className="catalog-backup-policy__grid">
        <div><strong>发布后自动备份</strong><p>组件、场景发布或废弃后，平台在 30 秒窗口内合并变更并异步备份，不阻塞发布事务。</p></div>
        <div><strong>前台立即备份</strong><p>环境 Owner 可随时创建恢复点；只有 Git 分支和不可变标签均推送成功后才提示完成。</p></div>
        <div><strong>完整性与范围</strong><p>校验 SQLite、外键、数据库与 Catalog/Playbook 摘要；不包含 Draft、Run 日志、凭据值和大型制品。</p></div>
      </div>
    </section>
    <RefreshNotice loading={Boolean(isRefreshing)} error={repository ? error : undefined} onRetry={reload} />
    {loading && !repository ? <LoadingBlock label="正在读取私有仓库…" /> : error && !repository ? <ErrorBlock message={error} onRetry={reload} /> : repository ? <div className="revision-preview catalog-repository-panel__body">
      <div className="revision-diff"><GitBranch size={18} /><div><strong>{!repository.enabled ? '服务端未启用发布目录灾备' : repository.configured ? '已接入私有仓库' : '尚未配置私有仓库'}</strong><p>{!repository.enabled ? repository.reason ?? '请由平台 Owner 完成服务端配置后重试。' : repository.configured ? repository.path : `允许根目录：${repository.allowedRoot}`}</p>{repository.enabled && repository.configured && <p>分支：{repository.branch} · 恢复点：{repository.recoveryPoints.length} 个</p>}{repository.enabled && repository.configured && <p>最近成功：{repository.lastSuccessfulAt ? formatTime(repository.lastSuccessfulAt) : '尚无成功恢复点'} · 发布代次：{repository.backedUpGeneration}/{repository.currentGeneration}</p>}</div></div>
      {!repository.enabled && <div className="inline-warning"><AlertTriangle size={16} /><span>服务端能力启用前，创建、接入和备份入口保持关闭。</span></div>}
      {repository.enabled && !repository.configured && <div className="inline-warning"><AlertTriangle size={16} /><span>未通过前台选择仓库，发布后自动备份与立即备份不可用。</span></div>}
      {repository.enabled && repository.configured && repository.behind && <div className="inline-warning"><AlertTriangle size={16} /><span>恢复点落后：当前发布代次 {repository.currentGeneration}，已备份代次 {repository.backedUpGeneration}。</span></div>}
      {repository.enabled && repository.lastError && <div className="inline-warning"><AlertTriangle size={16} /><span>最近备份异常：{repository.lastError}</span></div>}
      {repository.enabled && repository.configured && repository.restoreTargetKnown && !repository.targetCatalogEmpty && <div className="inline-warning"><AlertTriangle size={16} /><span>当前发布目录非空：组件 {repository.targetComponentCount} 个、场景 {repository.targetScenarioCount} 个；只能管理备份，不能从 Git 恢复。</span></div>}
      {repository.enabled && repository.configured && <div className="modal-actions"><button className="button button--danger" disabled={!repository.recoveryPoints.length || !repository.restoreTargetKnown || !repository.targetCatalogEmpty} title={!repository.recoveryPoints.length ? '当前仓库没有可用恢复点' : !repository.restoreTargetKnown ? '暂时无法确认数据库是否为空' : !repository.targetCatalogEmpty ? '只有组件和场景均为空时才能恢复' : undefined} onClick={() => setRestoreRepository(repository)}><RotateCcw size={15} /> 从恢复点恢复空库</button></div>}
    </div> : null}
    {repositoryMode && repositoryEnabled && repository && <CatalogRepositoryModal mode={repositoryMode} allowedRoot={repository.allowedRoot} onClose={() => setRepositoryMode(undefined)} onDone={updated} />}
    {restoreRepository && repositoryEnabled && <CatalogRestoreModal repository={restoreRepository} onClose={() => setRestoreRepository(undefined)} onDone={() => { setRestoreRepository(undefined); notify('success', '发布目录已从 Git 恢复', '组件、场景和 Playbook 已写入空数据库，并已触发恢复后快照。'); signalRefresh(['catalog-repository', 'components', 'scenarios', 'workbench']); void reload(); }} />}
  </article>;
}

function CatalogRepositoryModal({ mode, allowedRoot, onClose, onDone }: { mode: 'create' | 'connect' | 'restore'; allowedRoot: string; onClose: () => void; onDone: (status: CatalogRepositoryStatus, message: string, continueToRestore?: boolean) => void }) {
  const { notify } = useApp();
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const pathState = catalogRepositoryPathState(allowedRoot, path);
  async function submit(continueToRestore = false) {
    if (pathState.error) return;
    setBusy(true);
    try {
      const status = mode === 'create' ? await api.createCatalogRepository(path.trim()) : await api.connectCatalogRepository(path.trim());
      onDone(status, mode === 'create' ? '私有仓库已创建' : '已有仓库已接入', continueToRestore);
    } catch (reason) { notify('error', mode === 'create' ? '创建私有仓库失败' : '接入私有仓库失败', displayError(reason)); }
    finally { setBusy(false); }
  }
  const restoreMode = mode === 'restore';
  return <Modal title={mode === 'create' ? '创建私有 Catalog 仓库' : restoreMode ? '从已有 Git 仓库恢复' : '接入已有 Catalog 仓库'} description={restoreMode ? `先验证并接入 ${allowedRoot} 内的仓库，再选择恢复点并预检。` : `路径必须位于 ${allowedRoot} 内；相对路径会基于该目录解析。`} onClose={onClose}>
    <form onSubmit={(event: FormEvent<HTMLFormElement>) => { event.preventDefault(); void submit(restoreMode); }}><div className="modal-body"><label><span>服务器仓库路径</span><input aria-label="服务器仓库路径" aria-invalid={Boolean(pathState.error)} value={path} onChange={(event) => setPath(event.target.value)} placeholder={mode === 'create' ? 'catalog-production.git' : '/data/private-catalog-repositories/catalog-production.git'} autoFocus required /><small>{mode === 'create' ? '目标必须不存在；平台将创建权限为 0700 的 bare Git 仓库。' : restoreMode ? '已有仓库必须包含 catalog 分支；验证成功后继续选择远端 backup/* 恢复点。' : `已有仓库必须包含 catalog 分支，且路径位于 ${allowedRoot}。`}</small>{pathState.resolvedPath && !pathState.error ? <small>最终路径：<code>{pathState.resolvedPath}</code></small> : null}</label>{pathState.error ? <div className="form-validation" role="alert">{pathState.error}</div> : null}</div><footer className="modal-actions"><button type="button" className="button button--quiet" disabled={busy} onClick={onClose}>取消</button>{restoreMode && <button type="button" className="button button--quiet" disabled={busy || !path.trim() || Boolean(pathState.error)} onClick={() => void submit(false)}>仅接入，暂不恢复</button>}<button className="button button--primary" disabled={busy || !path.trim() || Boolean(pathState.error)}>{busy ? '处理中…' : mode === 'create' ? '创建并接入' : restoreMode ? '验证并继续恢复' : '验证并接入'}</button></footer></form>
  </Modal>;
}

function catalogRepositoryPathState(allowedRoot: string, input: string): { resolvedPath?: string; error?: string } {
  const path = input.trim();
  if (!path) return {};
  const root = allowedRoot.replace(/\/+$/, '') || '/';
  if (path.startsWith('/')) return { resolvedPath: path };
  const rootWithoutSlash = root.replace(/^\/+/, '');
  const cleanRelative = path.replace(/^\.\//, '').replace(/\/+$/, '');
  if (cleanRelative === rootWithoutSlash || cleanRelative.startsWith(`${rootWithoutSlash}/`)) {
    return { error: `这看起来是缺少开头“/”的绝对路径；请填写 /${cleanRelative}，或只填写相对于 ${root} 的部分。` };
  }
  return { resolvedPath: root === '/' ? `/${cleanRelative}` : `${root}/${cleanRelative}` };
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
