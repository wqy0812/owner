import { useState } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { ErrorBlock, LoadingBlock, Modal } from './Primitives';
import type { CleanupItem } from '../types/runRetention';

export function RunCleanupModal({ runIds, onClose, onDeleted }: { runIds: string[]; onClose: () => void; onDeleted: (ids: string[]) => void }) {
  const { user, notify, signalRefresh } = useApp();
  const { data: preview, loading, error, reload } = useApiData<CleanupItem[]>(
    () => api.cleanupPreview(runIds), [user.id, runIds.join(':')], [],
  );
  const [busy, setBusy] = useState(false);
  const eligible = runIds.length > 0 && preview?.length === runIds.length && new Set(preview.map(item => item.runId)).size === runIds.length && preview.every(item => item.eligible && runIds.includes(item.runId));
  async function remove() {
    if (!eligible || busy) return;
    setBusy(true);
    try {
      await api.cleanupRuns(runIds);
      notify('success', '失败记录已删除', '已保留最小历史与删除审计。');
      onDeleted(runIds);
      signalRefresh(['runs', 'workbench', 'components', 'scenarios', 'environments', 'notifications']);
      onClose();
    } catch (reason) {
      notify('error', '删除失败', displayError(reason));
      void reload();
    } finally { setBusy(false); }
  }
  return <Modal title="删除失败记录" description="仅可删除超过保留期限且未被引用的失败记录。日志和执行详情删除后无法恢复，最小历史与审计记录会保留。" onClose={() => { if (!busy) onClose(); }}>
    <div className="modal-body">
      {loading ? <LoadingBlock label="正在检查保留期限和记录引用…" /> : error ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <div className="cleanup-preview">{preview?.map(item => <article key={item.runId}>
        <strong>{item.runId}</strong><p>{item.eligible ? '可以删除' : '暂时不能删除'}</p>
        {item.reasons.length > 0 && <ul>{item.reasons.map(reason => <li key={reason}>{reason}</li>)}</ul>}
      </article>)}</div>}
    </div>
    <footer className="modal-actions"><button type="button" className="button button--quiet" disabled={busy} onClick={onClose}>取消</button><button type="button" className="button button--danger-soft" disabled={busy || loading || Boolean(error) || !eligible} onClick={() => void remove()}>{busy ? '删除中…' : '确认删除'}</button></footer>
  </Modal>;
}
