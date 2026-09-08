import { Table } from 'antd';
import { Field } from './Field';
import { Button, Input } from 'antd';
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
    if (value === undefined)
        return '—';
    return typeof value === 'string' ? value : JSON.stringify(value);
}
export function ReleaseReviewModal({ releaseId, onClose, onDecided }: {
    releaseId: string;
    onClose: () => void;
    onDecided: () => void | Promise<void>;
}) {
    const { notify, platformOptionCategories } = useApp();
    const query = useApiData((signal) => api.previewReleaseReview(releaseId, signal), [releaseId], 'workbench');
    const [decision, setDecision] = useState<'approve' | 'reject'>();
    const [affected, setAffected] = useState<import('../types/componentUsage').ComponentUsage>();
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
        if (!query.data || (decision === 'reject' && !comment.trim()))
            return;
        setBusy(decision);
        setDecisionError('');
        try {
            await api.decideReleaseReview(releaseId, decision, comment.trim(), query.data.previewDigest);
            notify('success', decision === 'approve' ? '合同审核已通过' : '合同审核已驳回');
            setDecision(decision);
            try {
                setAffected(await api.componentUsage(query.data.componentId, releaseId, false));
            }
            catch { /* The result stays visible even if usage requires a refresh. */ }
            await onDecided();
        }
        catch (reason) {
            setDecisionError(displayError(reason));
            setStaleDecision(true);
        }
        finally {
            setBusy('');
        }
    }
    const preview = query.data;
    const release = preview?.release;
    const optionLabel = (key: string, value: string) => platformOptionCategories.find((item) => item.key === key)?.options.find((item) => item.value === value)?.label ?? value;
    if (decision && preview && release)
        return <Modal busy={Boolean(busy)} size="wide" title="合同评审结果" onClose={onClose} footer={<footer className="modal-actions"><Button className="button button--primary" onClick={onClose} htmlType={"button"} type="primary">关闭</Button></footer>}><div className="modal-body"><div className="release-detail-grid"><div><span>审核状态</span><strong>{decision === 'approve' ? '审核通过' : '已驳回'}</strong></div><div><span>共享状态</span><strong>{release.candidate ? '已共享' : '尚未共享'}</strong></div><div><span>组件 Owner</span><strong>{preview.ownerName}</strong></div></div><p>{decision === 'approve' ? '审核通过后仍需组件 Owner 显式共享，引用场景才能读取并继续校验合同。' : '修改后需重新提交合同审核。'}</p><h3>受影响的直接引用场景</h3>{affected ? affected.scenarios.length ? affected.scenarios.map(item => <p key={item.revisionId}>{item.name} · r{item.revision} · {item.ownerName}</p>) : <p>当前没有直接引用场景。</p> : <p>引用信息暂不可用，请在组件详情查看。</p>}</div></Modal>;
    return <Modal busy={Boolean(busy)} size="wide" title="Component Release 合同审核" description="批准或驳回前必须完整读取当前合同与所有锁定 Playbook；任何内容漂移都需要重新预览。" onClose={onClose} footer={preview && release ? <footer className="modal-actions">
        <Button className="button button--quiet" disabled={Boolean(busy)} onClick={onClose} htmlType={"button"} type="default">关闭</Button>
        <Button className="button button--quiet" disabled={Boolean(busy) || staleDecision || !comment.trim()} onClick={() => void decide('reject')} htmlType={"button"} type="default"><X size={15}/> {busy === 'reject' ? '驳回中…' : '驳回'}</Button>
        <Button className="button button--primary" disabled={Boolean(busy) || staleDecision || preview.playbooks.length !== (release.actions?.length ?? 0)} onClick={() => void decide('approve')} htmlType={"button"} type="primary"><Check size={15}/> {busy === 'approve' ? '批准中…' : '批准'}</Button>
      </footer> : null}>
    {!preview && query.loading ? <LoadingBlock label="正在加载待审组件…"/> : !preview && query.error ? <ErrorBlock message={query.error} onRetry={() => void reload()}/> : preview && release ? <>
      <div className="modal-body inspect-contract release-review-preview">
        <section className="contract-section release-detail-summary">
          <h3>组件与版本</h3>
          <div className="release-detail-grid">
            <div><span>组件</span><strong>{preview.componentName}</strong></div>
            <div><span>组件 Owner</span><strong>{preview.ownerName}</strong></div>
            <div><span>版本</span><strong>{release.version}</strong></div>
            <div><span>审核状态</span><StatusPill status={release.review.status}>待审核</StatusPill></div><div><span>共享状态</span><strong>{release.candidate ? '已共享' : '尚未共享'}</strong></div>
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
          <div className="table-wrap"><Table className="review-parameter-table" dataSource={release.parameters ?? []} rowKey="name" pagination={false} scroll={{x: 900}} locale={{emptyText: '无参数'}} columns={[
{title:'参数',key:'name',width:220,render:(_,parameter)=><><strong>{parameter.name}</strong><small>{parameter.description}</small></>},
{title:'类型',key:'type',width:120,render:(_,parameter)=><>{parameter.type}{parameter.required ? ' · 必填' : ''}</>},
{title:'可见性',key:'visibility',width:150,render:(_,parameter)=><>{parameter.visibility === 'public' ? '公开' : '内部'} · {parameter.modifiable ? '可修改' : '固定'}</>},
{title:'提供方',key:'provider',width:140,render:(_,parameter)=>providerLabels[parameter.valueProvider]},
{title:'合同值',key:'value',render:(_,parameter)=><><code>{parameter.valueProvider === 'component_owner' ? showValue(parameter.fixedValue) : parameter.valueProvider === 'scenario_owner' ? showValue(parameter.suggestedValue) : parameter.valueProvider === 'environment_owner' ? '组件环境字段' : '由依赖映射解析'}</code>{parameter.testValue !== undefined ? <small>测试值：{showValue(parameter.testValue)}</small> : null}</>}
]} /></div>
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
              <header><div><strong>{action.type} · {action.name || action.type}</strong><small><span title={action.hostGroup}>{optionLabel('hostGroup', action.hostGroup ?? '')}</span> · {action.timeoutSeconds ?? 1800}s · {action.riskLevel ?? 'low'}</small></div><code>{action.playbook}</code></header>
              <div className="release-action-facts"><div><span>CredentialRef</span><strong>{action.requiredCredentials?.join('、') || '无'}</strong></div><div><span>能力</span><strong>{[action.idempotent ? '幂等' : '', action.destructive ? '破坏性' : ''].filter(Boolean).join('、') || '标准'}</strong></div><div><span>版本转换</span><strong>{action.fromReleaseId || '—'} → {action.toReleaseId || '—'}</strong></div></div>
              {playbook ? <div className="release-playbook-source"><div><span>{playbook.filename}</span><code>sha256:{playbook.sha256}</code></div><pre className="code-editor release-playbook-preview">{playbook.content}</pre></div> : <div className="inline-warning" role="alert"><span>预览响应缺少该 Action 的 Playbook，不能审批。</span></div>}
            </article>;
            })}
          {!release.actions?.length ? <div className="mapping-empty">当前 Release 没有生命周期 Action。</div> : null}
        </section>
        <section className="contract-section review-digest"><span>本次预览摘要</span><code>{preview.previewDigest}</code></section>
        <Field label={"审核备注 / 驳回原因"}><Input.TextArea rows={3} value={comment} onChange={(event) => setComment(event.target.value)} placeholder="批准时可选；驳回时必填"/></Field>
        {decisionError ? <div className="inline-warning" role="alert"><span>{decisionError}</span><Button className="button button--quiet" disabled={Boolean(busy)} onClick={() => void reload()} htmlType={"button"} type="default"><RefreshCcw size={15}/> 重新预览</Button></div> : null}
      </div>

    </> : null}
  </Modal>;
}
