import { AlertTriangle, ArrowRight, ChevronDown, CircleHelp, History } from 'lucide-react';
import { useId, useState } from 'react';
import { Link } from 'react-router-dom';
import type { WorkExplanation, WorkItem, WorkReason } from '../types/domain';
import { formatTime } from './Primitives';

function ReasonRow({ reason, currentResourceHref }: { reason: WorkReason; currentResourceHref?: string }) {
  const evidenceHref = reason.evidenceRunId ? `/runs?selected=${reason.evidenceRunId}` : undefined;
  const nextAction = reason.nextAction?.href === currentResourceHref ? undefined : reason.nextAction;
  return <li>
    <span className="status-explanation__bullet" />
    <div>
      <strong>{reason.message}</strong>
      {reason.cause && <small><History size={13} />{reason.cause.actorName ? `${reason.cause.actorName} · ` : ''}{reason.cause.summary}{reason.cause.at ? ` · ${formatTime(reason.cause.at)}` : ''}</small>}
    </div>
    <div className="status-explanation__reason-actions">
      {evidenceHref && evidenceHref !== currentResourceHref && nextAction?.href !== evidenceHref && <Link className="icon-text" to={evidenceHref}>查看证据 Run</Link>}
      {nextAction && <Link className="icon-text icon-text--primary" to={nextAction.href}>{nextAction.label}<ArrowRight size={13} /></Link>}
    </div>
  </li>;
}

export function StatusExplanationPanel({ item, explanation, title, currentResourceHref }: { item?: WorkItem; explanation?: WorkExplanation; title?: string; currentResourceHref?: string }) {
  const reasons = item?.reasons ?? explanation?.reasons ?? [];
  const reasonsKey = reasons.map((reason) => `${reason.code}:${reason.evidenceRunId ?? reason.message}`).join('|');
  const [expandedReasonsKey, setExpandedReasonsKey] = useState<string>();
  const reasonsId = useId();
  if (!reasons.length) return null;
  const reasonsExpanded = expandedReasonsKey === reasonsKey;
  const candidatePrimaryAction = item?.primaryAction ?? explanation?.primaryAction;
  const primaryAction = candidatePrimaryAction?.href === currentResourceHref ? undefined : candidatePrimaryAction;
  const secondaryActions = (item?.secondaryActions ?? explanation?.secondaryActions ?? []).filter((action) => action.href !== currentResourceHref);
  return <article className={`panel status-explanation status-explanation--${item?.priority ?? 'high'}${reasonsExpanded ? ' status-explanation--expanded' : ''}`} role={item?.priority === 'critical' ? 'alert' : undefined}>
    <header>
      <span>{item?.priority === 'critical' ? <AlertTriangle size={21} /> : <CircleHelp size={21} />}</span>
      <div className="status-explanation__heading"><small>状态说明</small><h2>{title ?? item?.title ?? '当前操作被阻断'}</h2></div>
      <div className="status-explanation__header-actions">
        {primaryAction && <Link className="button button--primary" to={primaryAction.href}>{primaryAction.label}<ArrowRight size={14} /></Link>}
        <button type="button" className={`status-explanation__toggle${reasonsExpanded ? ' is-expanded' : ''}`} aria-expanded={reasonsExpanded} aria-controls={reasonsId} onClick={() => setExpandedReasonsKey(reasonsExpanded ? undefined : reasonsKey)}><span>{reasonsExpanded ? '收起原因' : `展开 ${reasons.length} 项原因`}</span><ChevronDown size={14} /></button>
      </div>
    </header>
    <ul id={reasonsId} hidden={!reasonsExpanded}>{reasons.map((reason) => <ReasonRow key={`${reason.code}-${reason.evidenceRunId ?? reason.message}`} reason={reason} currentResourceHref={currentResourceHref} />)}</ul>
    {reasonsExpanded && secondaryActions.length > 0 && <footer>{secondaryActions.map((action) => <Link className="icon-text" key={`${action.href}-${action.label}`} to={action.href}>{action.label}</Link>)}</footer>}
  </article>;
}
