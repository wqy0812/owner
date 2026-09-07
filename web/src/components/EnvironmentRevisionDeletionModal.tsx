import { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import { actionableExplanation, api } from '../api/client';
import { displayError } from '../context/AppContext';
import type { Environment, EnvironmentRevision, EnvironmentRevisionDeletionImpact } from '../types/domain';
import { ErrorBlock, LoadingBlock, Modal } from './Primitives';

export function EnvironmentRevisionDeletionModal({ environment, revision, onClose, onDeleted }: {
  environment: Environment;
  revision: EnvironmentRevision;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [impact, setImpact] = useState<EnvironmentRevisionDeletionImpact>();
  const [error, setError] = useState<{ message: string; deleting: boolean }>();
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setImpact(undefined); setError(undefined); setConfirmed(false);
    void api.environmentRevisionDeletionImpact(environment.id, revision.id, controller.signal).then(value => {
      if (!controller.signal.aborted) setImpact(value);
    }).catch(reason => {
      if (!controller.signal.aborted) setError({ message: displayError(reason), deleting: false });
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [environment.id, revision.id, attempt]);

  async function remove() {
    if (!confirmed || !impact?.canDelete || loading || submitting || error) return;
    setSubmitting(true);
    try {
      await api.deleteEnvironmentRevision(environment.id, revision.id);
      onDeleted();
    } catch (reason) {
      const cause = actionableExplanation(reason)?.reasons[0]?.cause?.summary;
      setError({ message: cause || displayError(reason), deleting: true });
      setImpact(undefined); setConfirmed(false);
    } finally { setSubmitting(false); }
  }

  return <Modal title="删除环境版本" description={`${environment.name} · r${revision.revision}`} onClose={() => { if (!submitting) onClose(); }}>
    <div className="modal-body cluster-rollback-preview">
      {loading ? <LoadingBlock label="正在核对版本引用与连通性检查…" /> : <>
        {error && <ErrorBlock title={error.deleting ? '版本删除未完成' : '无法核对删除影响'} message={error.message} onRetry={() => setAttempt(value => value + 1)} />}
        {impact && <>
          <section className="cluster-rollback-summary" aria-label="版本删除影响">
            <div><span>引用 Run</span><strong>{impact.runCount}</strong></div>
            <div><span>镜像构建</span><strong>{impact.imageBuildCount}</strong></div>
            <div><span>TCP 检查</span><strong>{impact.healthCheckCount}</strong></div>
            <div><span>SSH 检查</span><strong>{impact.sshCheckCount}</strong></div>
          </section>
          {impact.canDelete ? <>
            <div className="warning-callout"><AlertTriangle size={19} /><div><strong>这是不可恢复的永久删除</strong><p>将删除此版本的环境配置及 TCP、SSH 检查，审计记录保留。当前版本和其他历史版本不受影响。</p></div></div>
            <label className="checkbox-field"><input type="checkbox" checked={confirmed} disabled={submitting || Boolean(error)} onChange={event => setConfirmed(event.target.checked)} /><span>我确认永久删除 {environment.name} 的 r{revision.revision} 版本</span></label>
          </> : <div className="warning-callout"><AlertTriangle size={19} /><div><strong>此版本不能删除</strong>{impact.blockers.map(blocker => <p key={blocker.code}>{blocker.message}</p>)}</div></div>}
        </>}
      </>}
    </div>
    <footer className="modal-actions">
      <button className="button button--quiet" disabled={submitting} onClick={onClose}>取消</button>
      <button className="button button--danger" disabled={loading || submitting || !impact?.canDelete || !confirmed || Boolean(error)} onClick={() => void remove()}>{submitting ? '正在删除…' : '确认删除版本'}</button>
    </footer>
  </Modal>;
}
