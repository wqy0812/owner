import { Archive, CheckCircle2, Container, Rocket } from 'lucide-react';
import { useState, type CSSProperties, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { formatTime, StatusPill } from '../../../components/Primitives';
import { useApp } from '../../../context/AppContext';
import type { ComponentRelease, ReleaseEvidenceSummary } from '../../../types/domain';
import { environmentConstraintDimensions, environmentConstraintGroups } from '../../../types/environmentConstraints';
import { lifecycleSummary, releaseEvidence } from '../model';
import { EvidenceLink } from '../verification/ReleaseVerification';

export function CompatibilityBadge({ release }: { release: ComponentRelease }) {
  const label = release.compatibility === 'not_applicable' ? '全新基线' : release.compatibility === 'compatible' ? '兼容升级' : '破坏性升级';
  return <span className={release.compatibility === 'breaking' ? 'breaking-badge' : 'release-line-badge'}>{label}</span>;
}

export function ReleaseReviewDetails({ release }: { release: ComponentRelease }) {
  const { users } = useApp();
  const { review } = release;
  if (review.status !== 'rejected') return null;
  const reviewer = users.find((item) => item.id === review.reviewedBy)?.name ?? review.reviewedBy;
  return <section className="contract-section release-review-details" aria-label="平台合同审批意见">
    <h3>平台合同审批意见</h3>
    <div className="release-detail-grid">
      <div><span>审核结果</span><StatusPill status={review.status}>已驳回</StatusPill></div>
      <div><span>审批人</span><strong>{reviewer || '—'}</strong></div>
      <div><span>提交时间</span><strong>{review.submittedAt ? formatTime(review.submittedAt) : '—'}</strong></div>
      <div><span>审批时间</span><strong>{review.reviewedAt ? formatTime(review.reviewedAt) : '—'}</strong></div>
    </div>
    <p className="release-review-comment">{review.comment?.trim() ? review.comment : '平台 Owner 未填写审批意见。'}</p>
  </section>;
}

export function DraftReadiness({ release, evidence, onContract, onReview, onLifecycle, onImage, onArtifact, onValidate, onPublish }: {
  release: ComponentRelease;
  evidence?: ReleaseEvidenceSummary;
  onContract: () => void;
  onReview: () => void;
  onLifecycle: () => void;
  onImage: () => void;
  onArtifact: () => void;
  onValidate: () => void;
  onPublish: () => void;
}) {
  const lifecycle = lifecycleSummary(release.actions);
  const install = releaseEvidence(evidence, 'install');
  const rollback = releaseEvidence(evidence, 'rollback');
  const transition = evidence?.currentTransition ?? evidence?.historicalTransition;
  const evolution = Boolean(release.parentReleaseId);
  const [expanded, setExpanded] = useState<string>();
  const blockersByStep = new Map<string, typeof release.readiness.blockers>();
  for (const blocker of release.readiness.blockers) {
    const step = blocker.code === 'install_evidence_missing' ? 'install'
      : blocker.code === 'rollback_evidence_missing' ? 'rollback'
        : blocker.code === 'evolution_evidence_missing' ? 'transition'
          : blocker.actionUrl.includes('action=lifecycle') || ['lifecycle_action_missing', 'action_checks_invalid', 'playbook_workspace_invalid'].includes(blocker.code) ? 'lifecycle'
            : blocker.code.includes('review') ? 'review' : 'contract';
    blockersByStep.set(step, [...(blockersByStep.get(step) ?? []), blocker]);
  }
  const steps: Array<{ id: string; name: string; done: boolean; detail: ReactNode; action?: string; onAction?: () => void }> = [
    { id: 'contract', name: 'Release 合同', done: true, detail: <p>{release.dependencies?.length ?? 0} 项依赖 · {release.parameters?.length ?? 0} 个参数</p>, action: '编辑合同', onAction: onContract },
    { id: 'lifecycle', name: '生命周期动作', done: true, detail: <p>{lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}{evolution ? '；升级能力与回退目标以当前合同检查为准。' : ''}</p>, action: '配置动作', onAction: onLifecycle },
    { id: 'review', name: '平台合同审核', done: release.review.status === 'approved', detail: <p>{release.review.status === 'approved' ? '当前参数、Action 与环境绑定已批准' : release.review.status === 'pending' ? '等待平台 Owner 审核' : release.review.status === 'rejected' ? `已驳回：${release.review.comment || '请修改后重提'}` : '请在版本列表提交审核'}</p>, ...(release.review.status === 'rejected' ? { action: '查看审批意见', onAction: onReview } : {}) },
    ...(evolution ? [
      { id: 'transition', name: '升级与回退闭环', done: Boolean(release.readiness.transitionEvidenceRunId), detail: <EvidenceLink run={transition} />, action: '环境验证', onAction: onValidate },
    ] : [
      { id: 'install', name: '安装与验证', done: Boolean(release.readiness.installEvidenceRunId), detail: <EvidenceLink run={install} />, action: '环境验证', onAction: onValidate },
      { id: 'rollback', name: '回退与回退后验证', done: Boolean(release.readiness.rollbackEvidenceRunId), detail: <EvidenceLink run={rollback} />, action: '环境验证', onAction: onValidate },
    ]),
  ].map(step => ({ ...step, done: step.done && !blockersByStep.has(step.id) }));
  const complete = steps.filter(step => step.done).length;
  const ready = complete === steps.length && release.readiness.status !== 'blocked' && !release.readiness.blockers.length;
  const current = steps.find(step => step.id === expanded);
  const detailId = `readiness-detail-${release.id}`;
  return <article className="panel readiness-panel" aria-label={`Draft ${release.version} 发布就绪度`}>
    <header className="readiness-header">
      <div><span className="panel__icon"><CheckCircle2 size={18} /></span><div><h2>Draft 发布就绪度</h2><p>{release.version} · 点击进度节点查看详情</p></div></div>
      <div className={`readiness-score${ready ? ' readiness-score--ready' : ''}`}><strong>{complete}/{steps.length}</strong><span>{ready ? '可以发布' : '仍有阻断项'}</span></div>
    </header>
    <div className="readiness-milestones" style={{ '--readiness-count': steps.length } as CSSProperties}>
      <div className="readiness-progress" role="progressbar" aria-label="Draft 发布就绪进度" aria-valuemin={0} aria-valuemax={steps.length} aria-valuenow={complete} aria-valuetext={`已完成 ${complete} 项，共 ${steps.length} 项`}><span style={{ width: `${complete / steps.length * 100}%` }} /></div>
      <ol className="readiness-nodes">
        {steps.map((step, index) => <li key={step.id} className={step.done ? 'readiness-node readiness-node--done' : 'readiness-node'}>
          <button type="button" aria-label={`${step.name} · ${step.done ? '已完成' : '待完成'}`} aria-expanded={expanded === step.id} aria-controls={expanded === step.id ? detailId : undefined} onClick={() => setExpanded(expanded === step.id ? undefined : step.id)}>
            <span className="readiness-node__marker">{step.done ? <CheckCircle2 size={18} /> : index + 1}</span>
            <strong>{step.name}</strong><small>{step.done ? '已完成' : '待完成'}</small>
          </button>
        </li>)}
      </ol>
    </div>
    {current && <section id={detailId} className="readiness-detail" aria-label={`${current.name}详情`}>
      <header><h3>{current.name}</h3><button type="button" className="icon-text" onClick={() => setExpanded(undefined)}>收起详情</button></header>
      {current.detail}
      {blockersByStep.get(current.id)?.map(blocker => <Link className="readiness-detail__blocker" key={`${blocker.code}:${blocker.message}`} to={blocker.actionUrl}>{blocker.message}</Link>)}
      {current.action && <button type="button" className="button button--quiet" onClick={current.onAction}>{current.action}</button>}
    </section>}
    <footer className="readiness-actions">
      <div><span>可选交付物：</span><button className="icon-text" onClick={onImage}><Container size={14} /> 镜像构建</button><button className="icon-text" onClick={onArtifact}><Archive size={14} /> 管理介质</button></div>
      <div><span>{ready ? '发布前将展示完整下游影响。' : '完成所有阻断项后才能发布。'}</span><button className="button button--primary" disabled={!ready} title={ready ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={onPublish}><Rocket size={15} /> 预览影响并发布</button></div>
    </footer>
  </article>;
}

export function EnvironmentConstraints({ constraints }: { constraints?: Record<string, unknown> }) {
  const { platformOptionCategories } = useApp();
  const groups = environmentConstraintGroups(constraints, environmentConstraintDimensions(platformOptionCategories));
  if (!groups.length) return <div className="env-constraints env-constraints--empty">不限制适配范围</div>;
  return <div className="env-constraints">{groups.map((group) => (
    <span className="env-constraint" key={group.key}>
      <em>{group.label}</em>
      {group.values.map((value) => <b key={value}>{value}</b>)}
    </span>
  ))}</div>;
}
