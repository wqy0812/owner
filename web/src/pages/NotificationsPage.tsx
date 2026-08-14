import { useMemo, useState } from 'react';
import { AlertTriangle, Bell, CheckCheck, GitBranch, Network, PackageOpen } from 'lucide-react';
import { Link } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, PageHeader, RefreshNotice, formatTime } from '../components/Primitives';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';

export function NotificationsPage() {
  const { user, notify, signalRefresh } = useApp();
  const { data: notifications, loading, error, isRefreshing, reload } = useApiData((signal) => api.notifications(signal), [user.id], 'notifications');
  const [filter, setFilter] = useState<'all' | 'unread'>('all');
  const visible = useMemo(() => (notifications ?? []).filter((item) => filter === 'all' || !item.read), [filter, notifications]);

  async function mark(id: string) {
    try { await api.markNotificationRead(id); signalRefresh('notifications'); }
    catch (reason) { notify('error', '更新通知失败', displayError(reason)); }
  }

  async function markAll() {
    try { await Promise.all((notifications ?? []).filter((item) => !item.read).map((item) => api.markNotificationRead(item.id))); notify('success', '全部标记为已读'); signalRefresh('notifications'); }
    catch (reason) { notify('error', '更新通知失败', displayError(reason)); }
  }

  return <div className="page">
    <PageHeader eyebrow="Impact inbox" title="通知中心" description="组件发布时沿反向依赖图追踪直接与传递影响，不会自动修改任何下游版本。" actions={(notifications ?? []).some((item) => !item.read) ? <button className="button button--quiet" onClick={() => void markAll()}><CheckCheck size={16} /> 全部已读</button> : undefined} />
    <RefreshNotice loading={isRefreshing} error={notifications ? error : undefined} onRetry={() => void reload()} />
    {loading && !notifications ? <LoadingBlock label="正在加载影响通知…" /> : error && !notifications ? <ErrorBlock message={error} onRetry={() => void reload()} /> : <section className="notification-center panel">
      <div className="notification-filter"><button className={filter === 'all' ? 'active' : ''} onClick={() => setFilter('all')}>全部 <span>{notifications?.length ?? 0}</span></button><button className={filter === 'unread' ? 'active' : ''} onClick={() => setFilter('unread')}>未读 <span>{notifications?.filter((item) => !item.read).length ?? 0}</span></button></div>
      {visible.length ? <div className="notification-list">{visible.map((notice) => <article key={notice.id} className={`notification-card${notice.read ? '' : ' notification-card--unread'}`}>
        <div className={`notification-card__icon${notice.breaking ? ' notification-card__icon--warning' : ''}`}>{notice.breaking ? <AlertTriangle size={20} /> : <Bell size={20} />}</div>
        <div className="notification-card__content"><header><div><strong>{notice.title}</strong>{notice.breaking && <span className="breaking-badge">BREAKING</span>}</div><time>{formatTime(notice.createdAt)}</time></header><p>{notice.message}</p>
          {(notice.componentName || notice.oldVersion || notice.newVersion) && <div className="version-change"><PackageOpen size={16} /><strong>{notice.componentName ?? '上游组件'}</strong><span>{notice.oldVersion ?? '—'}</span><em>→</em><span className="version-change__new">{notice.newVersion ?? '新版本'}</span></div>}
          {notice.impactPaths?.length ? <div className="notification-impact"><div><GitBranch size={15} /><strong>影响路径</strong></div>{notice.impactPaths.map((path, index) => <span key={index}>{path.join('  →  ')}</span>)}</div> : null}
          {notice.scenarioIds?.length ? <div className="scenario-impact"><Network size={14} /> 关联 {notice.scenarioIds.length} 个场景</div> : null}
        </div>
        <div className="row-actions">{notice.resourceUrl && <Link className="icon-text icon-text--primary" to={notice.resourceUrl}>查看关联资源</Link>}{!notice.read && <button className="icon-text" onClick={() => void mark(notice.id)}><CheckCheck size={15} /> 标为已读</button>}</div>
      </article>)}</div> : <EmptyState title={filter === 'unread' ? '没有未读通知' : '通知箱是空的'} description="组件新版本发布后，与你有关的影响路径会出现在这里。" />}
    </section>}
  </div>;
}
