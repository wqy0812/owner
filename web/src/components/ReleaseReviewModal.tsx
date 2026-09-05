import { Check, RefreshCcw, X } from 'lucide-react';
import { useState } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { ParameterDefinition } from '../types/domain';
import { ErrorBlock, LoadingBlock, Modal, StatusPill } from './Primitives';

const providerLabels: Record<ParameterDefinition['valueProvider'], string> = {
  component_owner: '组件 Owner', scenario_owner: '集群 Owner', environment_owner: '环境 Owner', upstream_mapping: '上游映射',
};

function showValue(value: unknown) {
  if (value === undefined) return '—';
  return typeof value === 'string' ? value : JSON.stringify(value);
}

export function ReleaseReviewModal({ releaseId, onClose, onDecided }: { releaseId: string; onClose: () => void; onDecided: () => void | Promise<void> }) {
  const { notify, platformOptionCategories } = useApp();
  const query = useApiData((signal) => api.previewReleaseReview(releaseId, signal), [releaseId], 'workbench');
  const [comment, setComment] = useState('');
  const [busy, setBusy] = useState<'approve' | 'reject' | ''>('');
  const [decisionError, setDecisionError] = useState('');
  const [staleDecision, setStaleDecision] = useState(false);

  async function reload() {
    setDecisionError('');
    setStaleDecision(false);
    await query.reload();
  }

  async function decide(decision: 'approve' | 'reject') {
    if (!query.data || (decision === 'reject' && !comment.trim())) return;
    setBusy(decision);
    setDecisionError('');
    try {
      await api.decideReleaseReview(releaseId, decision, comment.trim(), query.data.previewDigest);
      notify('success', decision === 'approve' ? '合同审核已通过' : '合同审核已驳回');
      await onDecided();
    } catch (reason) {
      setDecisionError(displayError(reason));
      setStaleDecision(true);
    } finally {
      setBusy('');
    }
  }

  const preview = query.data;
  const release = preview?.release;
  const optionLabel = (key: string, value: string) => platformOptionCategories.find((item) => item.key === key)?.options.find((item) => item.value === value)?.label ?? value;
  return <Modal size="wide" title="Component Release 合同审核" description="批准或驳回前必须完整读取当前合同与所有锁定 Playbook；任何内容漂移都需要重新预览。" onClose={onClose}>
    {!preview && query.loading ? <LoadingBlock label="正在加载待审组件…" /> : !preview && query.error ? <ErrorBlock message={query.error} onRetry={() => void reload()} /> : preview && release ? <>
      <div className="modal-body inspect-contract release-review-preview">
        <section className="contract-section release-detail-summary">
          <h3>组件与版本</h3>
          <div className="release-detail-grid">
            <div><span>组件</span><strong>{preview.componentName}</strong></div>
            <div><span>组件 Owner</span><strong>{preview.ownerName}</strong></div>
            <div><span>版本</span><strong>{release.version}</strong></div>
            <div><span>状态</span><StatusPill status={release.review.status}>待审核</StatusPill></div>
            <div><span>发布线</span><strong>{release.lineName}</strong></div>
            <div><span>版本关系</span><strong>{release.compatibility === 'not_applicable' ? '全新基线' : release.compatibility === 'compatible' ? '兼容升级' : '破坏性升级'}</strong></div>
            <div><span>风险等级</span><strong>{release.riskLevel ?? 'low'}</strong></div>
            <div><span>合同摘要</span><code>{release.review.contractDigest}</code></div>
          </div>
          <p>{release.releaseNotes || '未填写发布说明'}</p>
        </section>

        <section className="contract-section">
          <h3>适配环境</h3>
          <div className="env-constraints">{Object.entries(release.environmentConstraints ?? {}).flatMap(([key, raw]) => (Array.isArray(raw) ? raw : [raw]).map((value) => <span className="env-constraint" key={`${key}-${String(value)}`}><em>{platformOptionCategories.find((item) => item.key === key)?.label ?? key}</em><b>{optionLabel(key, String(value))}</b></span>))}</div>
          {!Object.keys(release.environmentConstraints ?? {}).length ? <div className="mapping-empty">未声明适配环境</div> : null}
        </section>

        <section className="contract-section">
          <h3>参数合同</h3>
          <div className="table-wrap"><table className="review-parameter-table"><colgroup><col /><col /><col /><col /><col /></colgroup><thead><tr><th>参数</th><th>类型</th><th>可见性</th><th>提供方</th><th>合同值</th></tr></thead><tbody>
            {(release.parameters ?? []).map((parameter) => <tr key={parameter.name}><td><strong>{parameter.name}</strong><small>{parameter.description}</small></td><td>{parameter.type}{parameter.required ? ' · 必填' : ''}</td><td>{parameter.visibility === 'public' ? '公开' : '内部'} · {parameter.modifiable ? '可修改' : '固定'}</td><td>{providerLabels[parameter.valueProvider]}</td><td><code>{parameter.valueProvider === 'component_owner' ? showValue(parameter.fixedValue) : parameter.valueProvider === 'scenario_owner' ? showValue(parameter.suggestedValue) : parameter.valueProvider === 'environment_owner' ? '组件环境字段' : '由依赖映射解析'}</code>{parameter.testValue !== undefined ? <small>测试值：{showValue(parameter.testValue)}</small> : null}</td></tr>)}
            {!release.parameters?.length ? <tr><td colSpan={5}>无参数</td></tr> : null}
          </tbody></table></div>
        </section>

        <section className="contract-section">
          <h3>依赖与参数映射</h3>
          {(release.dependencies ?? []).map((dependency) => <article className="review-contract-item" key={dependency.id ?? `${dependency.componentId}-${dependency.releaseId}`}><strong>{dependency.componentName || dependency.componentId} · {dependency.version || dependency.releaseId}</strong><p>{dependency.purpose || '未填写依赖用途'}</p>{dependency.parameterMappings?.map((mapping) => <code key={`${mapping.upstreamParameter}-${mapping.targetParameter}`}>{mapping.upstreamParameter} → {mapping.targetParameter}</code>)}</article>)}
          {!release.dependencies?.length ? <div className="mapping-empty">无直接依赖</div> : null}
        </section>

        <section className="contract-section">
          <h3>介质与镜像内容身份</h3>
          {(release.artifacts ?? []).map((artifact) => <article className="review-contract-item" key={artifact.id}><strong>介质 · {artifact.alias} / {artifact.filename}</strong><code>sha256:{artifact.sha256}</code></article>)}
          {(release.images ?? []).map((image) => <article className="review-contract-item" key={image.id}><strong>镜像 · {image.logicalName}</strong><code>{image.digest}</code></article>)}
          {!release.artifacts?.length && !release.images?.length ? <div className="mapping-empty">未登记介质或镜像</div> : null}
        </section>

        <section className="contract-section release-action-inspector">
          <h3>生命周期 Action 与完整 Playbook</h3>
          {(release.actions ?? []).map((action) => {
            const playbook = preview.playbooks.find((item) => item.actionId === action.id && item.path === action.playbook);
            return <article className="review-playbook" key={action.id ?? `${action.type}-${action.playbook}`}>
              <header><div><strong>{action.type} · {action.name || action.type}</strong><small>{action.hostGroup || '未指定主机组'} · {action.timeoutSeconds ?? 1800}s · {action.riskLevel ?? 'low'}</small></div><code>{action.playbook}</code></header>
              <div className="release-action-facts"><div><span>CredentialRef</span><strong>{action.requiredCredentials?.join('、') || '无'}</strong></div><div><span>能力</span><strong>{[action.idempotent ? '幂等' : '', action.destructive ? '破坏性' : ''].filter(Boolean).join('、') || '标准'}</strong></div><div><span>版本转换</span><strong>{action.fromReleaseId || '—'} → {action.toReleaseId || '—'}</strong></div></div>
              {playbook ? <div className="release-playbook-source"><div><span>{playbook.filename}</span><code>sha256:{playbook.sha256}</code></div><pre className="code-editor release-playbook-preview">{playbook.content}</pre></div> : <div className="inline-warning" role="alert"><span>预览响应缺少该 Action 的 Playbook，不能审批。</span></div>}
            </article>;
          })}
          {!release.actions?.length ? <div className="mapping-empty">当前 Release 没有生命周期 Action。</div> : null}
        </section>
        <section className="contract-section review-digest"><span>本次预览摘要</span><code>{preview.previewDigest}</code></section>
        <label><span>审核备注 / 驳回原因</span><textarea rows={3} value={comment} onChange={(event) => setComment(event.target.value)} placeholder="批准时可选；驳回时必填" /></label>
        {decisionError ? <div className="inline-warning" role="alert"><span>{decisionError}</span><button type="button" className="button button--quiet" disabled={Boolean(busy)} onClick={() => void reload()}><RefreshCcw size={15} /> 重新预览</button></div> : null}
      </div>
      <footer className="modal-actions">
        <button type="button" className="button button--quiet" disabled={Boolean(busy)} onClick={onClose}>关闭</button>
        <button type="button" className="button button--quiet" disabled={Boolean(busy) || staleDecision || !comment.trim()} onClick={() => void decide('reject')}><X size={15} /> {busy === 'reject' ? '驳回中…' : '驳回'}</button>
        <button type="button" className="button button--primary" disabled={Boolean(busy) || staleDecision || preview.playbooks.length !== (release.actions?.length ?? 0)} onClick={() => void decide('approve')}><Check size={15} /> {busy === 'approve' ? '批准中…' : '批准'}</button>
      </footer>
    </> : null}
  </Modal>;
}
