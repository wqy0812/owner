import { RouteButton } from '../components/RouteButton';
import { Button, Tabs } from 'antd';
import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Bell, CheckCheck, GitBranch, Network, PackageOpen } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, PageHeader, RefreshNotice, formatTime } from '../components/Primitives';
import { displayError, useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
export function NotificationsPage() {
    const { user, notify, signalRefresh } = useApp();
    const [searchParams] = useSearchParams();
    const { data: notifications, loading, error, isRefreshing, reload } = useApiData((signal) => api.notifications(signal), [user.id], 'notifications');
    const [filter, setFilter] = useState<'all' | 'unread'>('all');
    const selectedId = searchParams.get('selected');
    const selectedScenario = searchParams.get('scenario');
    const selectedRef = useRef<HTMLElement>(null);
    const visible = useMemo(() => (notifications ?? []).filter((item) => (filter === 'all' || !item.read) && (!selectedScenario || item.scenarioIds?.includes(selectedScenario))), [filter, notifications, selectedScenario]);
    useEffect(() => { selectedRef.current?.scrollIntoView({ block: 'center' }); }, [selectedId, selectedScenario, visible.length]);
    async function mark(id: string) {
        try {
            await api.markNotificationRead(id);
            signalRefresh('notifications');
        }
        catch (reason) {
            notify('error', '更新通知失败', displayError(reason));
        }
    }
    async function markAll() {
        try {
            await Promise.all((notifications ?? []).filter((item) => !item.read).map((item) => api.markNotificationRead(item.id)));
            notify('success', '全部标记为已读');
            signalRefresh('notifications');
        }
        catch (reason) {
            notify('error', '更新通知失败', displayError(reason));
        }
    }
    return <div className="page">
    <PageHeader eyebrow="Impact inbox" title="通知中心" description="组件发布时沿反向依赖图追踪直接与传递影响，不会自动修改任何下游版本。" actions={(notifications ?? []).some((item) => !item.read) ? <Button className="button button--quiet" onClick={() => void markAll()} htmlType={"button"} type="default"><CheckCheck size={16}/> 全部已读</Button> : undefined}/>
    <RefreshNotice loading={isRefreshing} error={notifications ? error : undefined} onRetry={() => void reload()}/>
    {loading && !notifications ? <LoadingBlock label="正在加载影响通知…"/> : error && !notifications ? <ErrorBlock message={error} onRetry={() => void reload()}/> : <section className="notification-center panel">
      <Tabs className="notification-filter" activeKey={filter} onChange={key => setFilter(key as 'all' | 'unread')} items={[
        { key: 'all', label: `全部 ${notifications?.length ?? 0}` },
        { key: 'unread', label: `未读 ${notifications?.filter(item => !item.read).length ?? 0}` },
      ]}/>

      {visible.length ? <div className="notification-list">{visible.map((notice) => <article key={notice.id} ref={notice.id === selectedId || Boolean(selectedScenario && notice.scenarioIds?.includes(selectedScenario)) ? selectedRef : undefined} className={`notification-card${notice.read ? '' : ' notification-card--unread'}${notice.id === selectedId ? ' notification-card--selected' : ''}`}>
        <div className={`notification-card__icon${notice.breaking ? ' notification-card__icon--warning' : ''}`}>{notice.breaking ? <AlertTriangle size={20}/> : <Bell size={20}/>}</div>
        <div className="notification-card__content"><header><div><strong>{notice.title}</strong>{notice.breaking && <span className="breaking-badge">BREAKING</span>}</div><time>{formatTime(notice.createdAt)}</time></header><p>{notice.message}</p>
          {(notice.componentName || notice.oldVersion || notice.newVersion) && <div className="version-change"><PackageOpen size={16}/><strong>{notice.componentName ?? '上游组件'}</strong><span>{notice.oldVersion ?? '—'}</span><em>→</em><span className="version-change__new">{notice.newVersion ?? '新版本'}</span></div>}
          {notice.impactPaths?.length ? <div className="notification-impact"><div><GitBranch size={15}/><strong>影响路径</strong></div>{notice.impactPaths.map((path, index) => <span key={index}>{path.join('  →  ')}</span>)}</div> : null}
          {notice.scenarioIds?.length ? <div className="scenario-impact"><Network size={14}/> 关联 {notice.scenarioIds.length} 个场景</div> : null}
        </div>
        <div className="row-actions">{notice.resourceUrl && <RouteButton className="icon-text icon-text--primary" to={notice.resourceUrl} type="primary">查看关联资源</RouteButton>}{!notice.read && <Button className="icon-text" onClick={() => void mark(notice.id)} htmlType={"button"} type="text"><CheckCheck size={15}/> 标为已读</Button>}</div>
      </article>)}</div> : <EmptyState title={filter === 'unread' ? '没有未读通知' : '通知箱是空的'} description="组件新版本发布后，与你有关的影响路径会出现在这里。"/>}
    </section>}
  </div>;
}
