import { AlertTriangle, ArrowRight, BellRing, Boxes, CheckCircle2, Clock3, CloudCog, Network, PlayCircle, ShieldAlert } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Metric, PageHeader, RefreshNotice, formatTime } from '../components/Primitives';
import { ReleaseReviewModal } from '../components/ReleaseReviewModal';
import { useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { ROLE_LABELS, type WorkItem, type Workbench } from '../types/domain';

type Section = { title: string; description: string; icon: typeof AlertTriangle; items: WorkItem[]; tone: string };

function sections(workbench: Workbench): Section[] {
  const critical = workbench.items.filter((item) => item.priority === 'critical');
  const action = workbench.items.filter((item) => item.priority !== 'critical' && (item.status === 'blocked' || item.status === 'action_required'));
  const progress = workbench.items.filter((item) => item.status === 'in_progress' && item.priority !== 'critical');
  const information = workbench.items.filter((item) => item.status === 'attention');
  return [
    { title: '阻塞交付', description: '失败、审批和必须立即处理的风险', icon: ShieldAlert, items: critical, tone: 'critical' },
    { title: '等待我处理', description: workbench.role === 'platform_admin' ? '等待预览与决定的 Component Release 合同' : 'Draft、证据、测试和环境维护事项', icon: AlertTriangle, items: action, tone: 'action' },
    { title: '进行中', description: '正在审批、排队或执行的交付工作', icon: Clock3, items: progress, tone: 'progress' },
    { title: '待评估影响', description: '上游变化只要求评估，不会自动使锁定版本失效', icon: BellRing, items: information, tone: 'info' },
  ];
}

function subjectLabel(item: WorkItem) {
  if (item.subject.version) return item.subject.version;
  if (item.subject.revision) return `Revision ${item.subject.revision}`;
  if (item.subject.environment) return item.subject.environment;
  return item.subject.type.replaceAll('_', ' ');
}

function WorkCard({ item, onReview }: { item: WorkItem; onReview?: (releaseId: string) => void }) {
  return <article className={`work-card work-card--${item.priority}`}>
    <div className="work-card__mark">{item.priority === 'critical' ? <ShieldAlert size={19} /> : item.status === 'in_progress' ? <Clock3 size={19} /> : item.status === 'attention' ? <BellRing size={19} /> : <AlertTriangle size={19} />}</div>
    <div className="work-card__body">
      <header><div><span>{subjectLabel(item)}</span><h3>{item.title}</h3></div><time>{formatTime(item.updatedAt)}</time></header>
      <ul>{item.reasons.map((reason) => <li key={`${reason.code}-${reason.evidenceRunId ?? reason.message}`}><span /><div><strong>{reason.message}</strong>{reason.cause && <small>{reason.cause.actorName ? `${reason.cause.actorName} · ` : ''}{reason.cause.summary}{reason.cause.at ? ` · ${formatTime(reason.cause.at)}` : ''}</small>}<div>{reason.evidenceRunId && reason.nextAction?.href !== `/runs?selected=${reason.evidenceRunId}` && <Link to={`/runs?selected=${reason.evidenceRunId}`}>查看证据 Run</Link>}{reason.nextAction && <Link to={reason.nextAction.href}>{reason.nextAction.label}</Link>}</div></div></li>)}</ul>
    </div>
    <div className="work-card__actions">{item.kind === 'component_review' && onReview ? <button type="button" className="button button--primary" onClick={() => onReview(item.subject.id)}>{item.primaryAction.label}<ArrowRight size={14} /></button> : <Link className="button button--primary" to={item.primaryAction.href}>{item.primaryAction.label}<ArrowRight size={14} /></Link>}{item.secondaryActions.map((action) => <Link key={action.href} className="icon-text" to={action.href}>{action.label}</Link>)}</div>
  </article>;
}

export function DashboardPage() {
  const { user, signalRefresh } = useApp();
  const [reviewReleaseId, setReviewReleaseId] = useState('');
  const query = useApiData((signal) => api.workbench(signal), [user.id], 'workbench');
  if (!query.data && !query.error) return <LoadingBlock label="正在整理我的交付待办…" />;
  if (query.error && !query.data) return <ErrorBlock message={query.error} onRetry={() => void query.reload()} />;
  const workbench = query.data!;
  const grouped = sections(workbench);
  const total = workbench.items.length;

  return <div className="page page--dashboard page--workbench">
    <PageHeader
      eyebrow="My delivery work"
      title={`早上好，${user.name.split('·')[0].trim()}`}
      description={`当前身份：${ROLE_LABELS[user.role]}。这里按优先级告诉你为什么被阻塞，以及下一步前往哪里处理。`}
      actions={<div className="workbench-header-actions"><small>生成于 {formatTime(workbench.generatedAt)}</small>{total ? workbench.items[0].kind === 'component_review' ? <button type="button" className="button button--primary" onClick={() => setReviewReleaseId(workbench.items[0].subject.id)}><PlayCircle size={16} /> 处理首要事项</button> : <Link className="button button--primary" to={workbench.items[0].primaryAction.href}><PlayCircle size={16} /> 处理首要事项</Link> : null}</div>}
    />
    <RefreshNotice loading={query.isRefreshing} error={query.data ? query.error : undefined} onRetry={() => void query.reload()} />
    <section className="metrics-grid">
      <Metric label="阻塞交付" value={workbench.summary.critical} note={workbench.summary.critical ? '优先处理失败与审批' : '当前没有严重阻塞'} tone="rose" />
      <Metric label="等待我处理" value={workbench.summary.actionRequired} note="需要你完成或决策" tone="amber" />
      <Metric label="进行中" value={workbench.summary.inProgress} note="审批、排队或执行中" tone="cyan" />
      <Metric label="待评估影响" value={workbench.summary.informational} note="评估后可标记已读" tone="indigo" />
    </section>

    {!total ? <section className="panel workbench-empty"><CheckCircle2 size={34} /><EmptyState title="当前没有待办" description={user.role === 'platform_admin' ? '当前没有等待平台 Owner 审核的 Component Release 合同。' : '你负责的交付对象没有阻断、活动运行或待评估影响。'} /></section> : <div className="work-sections">
      {grouped.map((section) => section.items.length ? <section className={`panel work-section work-section--${section.tone}`} key={section.title}>
        <header className="panel__header"><div><span className="panel__icon"><section.icon size={18} /></span><div><h2>{section.title}</h2><p>{section.description}</p></div></div><strong className="work-section__count">{section.items.length}</strong></header>
        <div className="work-list">{section.items.map((item) => <WorkCard item={item} key={item.id} onReview={setReviewReleaseId} />)}</div>
      </section> : null)}
    </div>}

    {user.role !== 'platform_admin' ? <section className="panel work-assets">
      <header className="panel__header"><div><span className="panel__icon"><Boxes size={18} /></span><div><h2>我负责的资产</h2><p>资产目录保留为辅助入口，首页主任务仍是处理交付待办</p></div></div></header>
      <div className="asset-summary">
        <Link to="/components"><Boxes size={19} /><div><strong>{workbench.assets.components}</strong><span>组件</span></div><ArrowRight size={15} /></Link>
        <Link to="/scenarios"><Network size={19} /><div><strong>{workbench.assets.scenarios}</strong><span>场景</span></div><ArrowRight size={15} /></Link>
        <Link to="/environments"><CloudCog size={19} /><div><strong>{workbench.assets.environments}</strong><span>环境</span></div><ArrowRight size={15} /></Link>
      </div>
    </section> : null}
    {reviewReleaseId ? <ReleaseReviewModal releaseId={reviewReleaseId} onClose={() => setReviewReleaseId('')} onDecided={async () => { setReviewReleaseId(''); signalRefresh('components'); await query.reload(); }} /> : null}
  </div>;
}
