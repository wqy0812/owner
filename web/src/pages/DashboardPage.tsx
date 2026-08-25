import { Activity, AlertTriangle, ArrowRight, BellRing, Boxes, CheckCircle2, Clock3, CloudCog, History, Network, PlayCircle, ShieldAlert } from 'lucide-react';
import { Link } from 'react-router-dom';
import { api } from '../api/client';
import { EmptyState, ErrorBlock, LoadingBlock, Metric, PageHeader, RefreshNotice, StatusPill, formatTime } from '../components/Primitives';
import { useApp } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import { ROLE_LABELS } from '../types/domain';

const ACTIVE_STATUSES = new Set(['queued', 'awaiting_approval', 'running']);

export function DashboardPage() {
  const { user, users } = useApp();
  const componentsQuery = useApiData((signal) => api.components(signal), [user.id], 'components');
  const scenariosQuery = useApiData((signal) => api.scenarios(signal), [user.id], 'scenarios');
  const notificationsQuery = useApiData((signal) => api.notifications(signal), [user.id], 'notifications');
  const runsQuery = useApiData((signal) => api.runs(signal), [user.id], 'runs');
  const environmentsQuery = useApiData((signal) => api.environments(signal), [user.id], 'environments');
  const auditsQuery = useApiData((signal) => user.role === 'environment_owner' ? api.auditEvents(signal) : Promise.resolve([]), [user.id, user.role], ['environments', 'runs']);
  const queries = [componentsQuery, scenariosQuery, notificationsQuery, runsQuery, environmentsQuery, auditsQuery];
  const loading = queries.some((query) => query.isInitialLoading);
  const error = queries.find((query) => query.error && !query.data)?.error;
  const refreshError = queries.find((query) => query.error && query.data)?.error;
  const isRefreshing = queries.some((query) => query.isRefreshing);
  const reload = () => queries.forEach((query) => void query.reload());

  if (loading) return <LoadingBlock label="正在汇总交付态势…" />;
  if (error) return <ErrorBlock message={error} onRetry={reload} />;

  const components = componentsQuery.data ?? [];
  const scenarios = scenariosQuery.data ?? [];
  const notifications = notificationsQuery.data ?? [];
  const runs = runsQuery.data ?? [];
  const environments = environmentsQuery.data ?? [];
  const audits = auditsQuery.data ?? [];
  const ownedComponents = components.filter((item) => item.ownerId === user.id).length;
  const ownedScenarios = scenarios.filter((item) => item.ownerId === user.id).length;
  const unread = notifications.filter((item) => !item.read).length;
  const active = runs.filter((item) => ACTIVE_STATUSES.has(item.status)).length;
  const pendingApproval = runs.filter((item) => item.status === 'awaiting_approval').length;

  if (user.role === 'environment_owner') {
    const owned = environments.filter((item) => item.ownerId === user.id);
    const currentHealth = (environment: typeof owned[number]) => environment.healthCheck?.environmentRevisionId === environment.currentRevision?.id
      ? environment.healthCheck
      : undefined;
    const degraded = owned.filter((item) => currentHealth(item)?.status === 'degraded').length;
    const unchecked = owned.filter((item) => !currentHealth(item)).length;
    const configurationRisks = owned.filter((item) => {
      const revision = item.currentRevision;
      return !revision?.hosts.length || !revision.variables.IMAGE_REGISTRY || !revision.variables.FILE_STATION;
    }).length;
    const attentionRuns = runs.filter((item) => ['awaiting_approval', 'failed', 'interrupted'].includes(item.status)).slice(0, 6);
    const ownedIDs = new Set(owned.map((item) => item.id));
    const environmentAudits = audits.filter((item) => item.resourceType === 'environment' && ownedIDs.has(item.resourceId)).slice(0, 6);
    const actorName = (id: string) => users.find((item) => item.id === id)?.name ?? id;
    const actionLabel = (action: string) => ({
      'environment.created': '创建环境', 'environment.inventory_updated': '更新 Inventory', 'environment.facts_updated': '更新环境事实',
      'environment.variables_updated': '更新环境变量', 'environment.credentials_updated': '更新凭据引用',
      'environment.revision_restored': '恢复历史 Revision', 'environment.health_checked': '执行环境检查',
    }[action] ?? action);

    return <div className="page page--dashboard">
      <PageHeader eyebrow="Environment operations" title={`早上好，${user.name.split('·')[0].trim()}`} description="这里优先展示环境健康、调度占用、待审批作业和配置审计。" actions={<Link className="button button--primary" to="/environments"><CloudCog size={16} /> 管理环境</Link>} />
      <RefreshNotice loading={isRefreshing} error={refreshError} onRetry={reload} />
      <section className="metrics-grid">
        <Metric label="我的环境" value={owned.length} note={`${owned.reduce((count, item) => count + (item.currentRevision?.hosts.length ?? 0), 0)} 台已配置主机`} tone="indigo" />
        <Metric label="健康异常" value={degraded} note={unchecked ? `${unchecked} 个环境尚未检查` : '所有环境均有检查记录'} tone="rose" />
        <Metric label="活动运行" value={active} note={pendingApproval ? `${pendingApproval} 个等待审批` : '当前无审批阻塞'} tone="amber" />
        <Metric label="配置风险" value={configurationRisks} note="缺少主机、Registry 或介质站" tone="cyan" />
      </section>
      <section className="dashboard-grid environment-dashboard">
        <article className="panel panel--wide"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><Activity size={18} /></span><div><h2>环境态势</h2><p>调度状态与当前 Revision 的最近连通性检查分开展示</p></div></div><Link to="/environments" className="text-link">全部环境 <ArrowRight size={14} /></Link></header>{owned.length ? <div className="environment-overview-list">{owned.map((environment) => {
          const health = currentHealth(environment);
          return <Link key={environment.id} to="/environments"><span className={`health-orb health-orb--${health?.status ?? 'unchecked'}`} /><div><strong>{environment.name}</strong><small>r{environment.currentRevision?.revision ?? 1} · {environment.currentRevision?.hosts.length ?? 0} 台主机 · {environment.schedulingStatus === 'idle' ? '调度空闲' : environment.schedulingStatus === 'running' ? '执行中' : environment.schedulingStatus === 'awaiting_approval' ? '等待审批' : '队列等待'}</small></div><span>{health?.status === 'healthy' ? '健康' : health?.status === 'degraded' ? '异常' : environment.healthCheck ? '需重检' : '未检查'}</span></Link>;
        })}</div> : <EmptyState title="尚未创建环境" />}</article>
        <article className="panel"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><AlertTriangle size={18} /></span><div><h2>待处理运行</h2><p>审批、失败和中断优先</p></div></div><Link to="/runs" className="text-link">运行中心 <ArrowRight size={14} /></Link></header>{attentionRuns.length ? <div className="run-list run-list--compact">{attentionRuns.map((run) => <Link key={run.id} to={`/runs?selected=${run.id}`} className="run-row"><span className={`run-row__mark run-row__mark--${run.status}`}>{run.status === 'awaiting_approval' ? <ShieldAlert size={17} /> : <AlertTriangle size={17} />}</span><div className="run-row__main"><strong>{run.name ?? run.scenarioName ?? run.componentName}</strong><span>{run.environmentName} · {formatTime(run.createdAt)}</span></div><StatusPill status={run.status} /></Link>)}</div> : <EmptyState title="没有待处理运行" description="当前没有审批阻塞、失败或中断。" />}</article>
        <article className="panel"><header className="panel__header"><div><span className="panel__icon"><History size={18} /></span><div><h2>环境审计</h2><p>Revision、检查与配置操作</p></div></div></header>{environmentAudits.length ? <div className="audit-list">{environmentAudits.map((event) => <div key={event.id}><span className="feed-item__dot" /><div><strong>{actionLabel(event.action)}</strong><p>{actorName(event.actorId)} · {String(event.metadata.changeReason ?? event.resourceId)}</p><small>{formatTime(event.createdAt)}</small></div></div>)}</div> : <EmptyState title="暂无环境审计" />}</article>
      </section>
    </div>;
  }

  return (
    <div className="page page--dashboard">
      <PageHeader
        eyebrow="Delivery cockpit"
        title={`早上好，${user.name.split('·')[0].trim()}`}
        description={`当前身份：${ROLE_LABELS[user.role]}。这里汇总与你相关的组件、场景和运行状态。`}
        actions={<Link className="button button--primary" to="/runs"><PlayCircle size={16} /> 查看运行</Link>}
      />
      <RefreshNotice loading={isRefreshing} error={refreshError} onRetry={reload} />

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
            <div><strong>发布策略</strong><p>场景必须通过当前 Revision 的完整测试；组件必须具备当前合同的安装验证和回退证据。</p></div>
          </div>
        </article>
      </section>
    </div>
  );
}
