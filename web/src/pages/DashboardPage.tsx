import { ArrowRight, BellRing, Boxes, CheckCircle2, Clock3, Network, PlayCircle, ShieldAlert } from 'lucide-react';
import { Link } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Metric, PageHeader, StatusPill, formatTime } from '../components/Primitives';
import { useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { ROLE_LABELS, type Component, type Notification, type Run, type Scenario } from '../types/domain';

interface OverviewData {
  components: Component[];
  scenarios: Scenario[];
  notifications: Notification[];
  runs: Run[];
}

const ACTIVE_STATUSES = new Set(['queued', 'awaiting_approval', 'running']);

export function DashboardPage() {
  const { user } = useApp();
  const { data, loading, error, reload } = useApiData<OverviewData>(async () => {
    const [components, scenarios, notifications, runs] = await Promise.all([
      api.components(),
      api.scenarios(),
      api.notifications(),
      api.runs(),
    ]);
    return { components, scenarios, notifications, runs };
  }, [user.id]);

  if (loading && !data) return <LoadingBlock label="正在汇总交付态势…" />;
  if (error && !data) return <ErrorBlock message={error} onRetry={() => void reload()} />;

  const components = data?.components ?? [];
  const scenarios = data?.scenarios ?? [];
  const notifications = data?.notifications ?? [];
  const runs = data?.runs ?? [];
  const ownedComponents = components.filter((item) => item.ownerId === user.id).length;
  const ownedScenarios = scenarios.filter((item) => item.ownerId === user.id).length;
  const unread = notifications.filter((item) => !item.read).length;
  const active = runs.filter((item) => ACTIVE_STATUSES.has(item.status)).length;
  const pendingApproval = runs.filter((item) => item.status === 'awaiting_approval').length;

  return (
    <div className="page page--dashboard">
      <PageHeader
        eyebrow="Delivery cockpit"
        title={`早上好，${user.name.split('·')[0].trim()}`}
        description={`当前身份：${ROLE_LABELS[user.role]}。这里汇总与你相关的组件、场景和运行状态。`}
        actions={<Link className="button button--primary" to="/runs"><PlayCircle size={16} /> 查看运行</Link>}
      />

      <section className="metrics-grid">
        <Metric label="我的组件" value={ownedComponents} note={`${components.length} 个可见组件`} tone="indigo" />
        <Metric label="我的场景" value={ownedScenarios} note={`${scenarios.length} 个可见场景`} tone="cyan" />
        <Metric label="进行中的运行" value={active} note={pendingApproval ? `${pendingApproval} 个等待环境审批` : '当前无审批阻塞'} tone="amber" />
        <Metric label="未读影响通知" value={unread} note={unread ? '请检查组件更新影响' : '所有通知已处理'} tone="rose" />
      </section>

      <section className="dashboard-grid">
        <article className="panel panel--wide">
          <header className="panel__header">
            <div><span className="panel__icon"><PlayCircle size={18} /></span><div><h2>最近运行</h2><p>环境队列与 Ansible 执行状态</p></div></div>
            <Link to="/runs" className="text-link">全部运行 <ArrowRight size={14} /></Link>
          </header>
          {runs.length ? (
            <div className="run-list run-list--compact">
              {runs.slice(0, 5).map((run) => (
                <Link key={run.id} to={`/runs?selected=${run.id}`} className="run-row">
                  <span className={`run-row__mark run-row__mark--${run.status}`}>
                    {run.status === 'succeeded' ? <CheckCircle2 size={17} /> : run.status === 'awaiting_approval' ? <ShieldAlert size={17} /> : <Clock3 size={17} />}
                  </span>
                  <div className="run-row__main">
                    <strong>{run.name ?? run.scenarioName ?? run.componentName ?? `运行 ${run.id.slice(0, 8)}`}</strong>
                    <span>{run.environmentName ?? '未命名环境'} · {formatTime(run.createdAt)}</span>
                  </div>
                  <StatusPill status={run.status} />
                </Link>
              ))}
            </div>
          ) : <EmptyState title="还没有运行记录" description="从组件或场景页面发起一次环境测试。" />}
        </article>

        <article className="panel">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--rose"><BellRing size={18} /></span><div><h2>影响通知</h2><p>上游组件发布动态</p></div></div>
            <Link to="/notifications" className="text-link">通知中心 <ArrowRight size={14} /></Link>
          </header>
          {notifications.length ? (
            <div className="feed-list">
              {notifications.slice(0, 4).map((notice) => (
                <Link key={notice.id} to="/notifications" className={`feed-item${notice.read ? '' : ' feed-item--unread'}`}>
                  <span className="feed-item__dot" />
                  <div><strong>{notice.title}</strong><p>{notice.message}</p><small>{formatTime(notice.createdAt)}</small></div>
                </Link>
              ))}
            </div>
          ) : <EmptyState title="没有影响通知" description="上游组件发布后，影响会出现在这里。" />}
        </article>

        <article className="panel">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--cyan"><Boxes size={18} /></span><div><h2>交付资产</h2><p>精确锁定的发布版本</p></div></div>
          </header>
          <div className="asset-summary">
            <Link to="/components"><Boxes size={19} /><div><strong>{components.length}</strong><span>可见组件</span></div><ArrowRight size={15} /></Link>
            <Link to="/scenarios"><Network size={19} /><div><strong>{scenarios.length}</strong><span>可见场景</span></div><ArrowRight size={15} /></Link>
          </div>
          <div className="policy-hint">
            <ShieldAlert size={18} />
            <div><strong>发布策略</strong><p>场景必须通过当前 revision 的完整测试，组件允许以“未验证”状态发布。</p></div>
          </div>
        </article>
      </section>
    </div>
  );
}
