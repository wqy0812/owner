import { AlertTriangle, ArrowRight, CircleHelp, History } from 'lucide-react';
import { Link } from 'react-router-dom';
import type { WorkExplanation, WorkItem, WorkReason } from '../types/domain';
import { formatTime } from './Primitives';

function ReasonRow({ reason }: { reason: WorkReason }) {
  const evidenceHref = reason.evidenceRunId ? `/runs?selected=${reason.evidenceRunId}` : undefined;
  return <li>
    <span className="status-explanation__bullet" />
    <div>
      <strong>{reason.message}</strong>
      {reason.cause && <small><History size={13} />{reason.cause.actorName ? `${reason.cause.actorName} · ` : ''}{reason.cause.summary}{reason.cause.at ? ` · ${formatTime(reason.cause.at)}` : ''}</small>}
    </div>
    <div className="status-explanation__reason-actions">
      {evidenceHref && reason.nextAction?.href !== evidenceHref && <Link className="icon-text" to={evidenceHref}>查看证据 Run</Link>}
      {reason.nextAction && <Link className="icon-text icon-text--primary" to={reason.nextAction.href}>{reason.nextAction.label}<ArrowRight size={13} /></Link>}
    </div>
  </li>;
}

export function StatusExplanationPanel({ item, explanation, title }: { item?: WorkItem; explanation?: WorkExplanation; title?: string }) {
  const reasons = item?.reasons ?? explanation?.reasons ?? [];
  if (!reasons.length) return null;
  const primaryAction = item?.primaryAction ?? explanation?.primaryAction;
  const secondaryActions = item?.secondaryActions ?? explanation?.secondaryActions ?? [];
  return <article className={`panel status-explanation status-explanation--${item?.priority ?? 'high'}`} role={item?.priority === 'critical' ? 'alert' : undefined}>
    <header>
      <span>{item?.priority === 'critical' ? <AlertTriangle size={21} /> : <CircleHelp size={21} />}</span>
      <div><small>状态说明</small><h2>{title ?? item?.title ?? '当前操作被阻断'}</h2></div>
      {primaryAction && <Link className="button button--primary" to={primaryAction.href}>{primaryAction.label}<ArrowRight size={14} /></Link>}
    </header>
    <ul>{reasons.map((reason) => <ReasonRow key={`${reason.code}-${reason.evidenceRunId ?? reason.message}`} reason={reason} />)}</ul>
    {secondaryActions.length > 0 && <footer>{secondaryActions.map((action) => <Link className="icon-text" key={`${action.href}-${action.label}`} to={action.href}>{action.label}</Link>)}</footer>}
  </article>;
}
