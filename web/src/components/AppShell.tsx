import { Select } from 'antd';
import { Bell, BookOpenText, Boxes, ChevronLeft, ChevronRight, CircleGauge, CloudCog, GitBranch, Network, PlayCircle, Radio, ShieldCheck, Settings2, } from 'lucide-react';
import { Suspense, useState } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import { useApp } from '../context/AppContext';
import { ROLE_LABELS } from '../types/domain';
import { LoadingBlock } from './Primitives';
const links = [
    { group: '工作', to: '/', label: '我的工作', icon: CircleGauge, end: true },
    { group: '交付', to: '/components', label: '组件', icon: Boxes },
    { group: '交付', to: '/scenarios', label: '集群', icon: Network },
    { group: '交付', to: '/environments', label: '环境', icon: CloudCog },
    { group: '管理', to: '/disaster-recovery', label: '灾备目录', icon: GitBranch, environmentOwnerOnly: true },
    { group: '交付', to: '/runs', label: '运行', icon: PlayCircle },
    { group: '管理', to: '/platform-management', label: '平台管理', icon: Settings2, platformAdminOnly: true },
    { group: '帮助', to: '/manual', label: '平台说明书', icon: BookOpenText },
];
export function AppShell() {
    const { user, users, switchUser, switching, connected } = useApp();
    const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
    return (<div className={`app-shell${sidebarCollapsed ? ' app-shell--sidebar-collapsed' : ''}`}>
      <aside className="sidebar">
        <div className="brand">
          <span className="brand__mark"><Boxes size={21}/></span>
          <div>
            <strong>ClusterForge</strong>
            <small>交付编排中心</small>
          </div>
        </div>

        <button type="button" className="sidebar-collapse" aria-controls="primary-navigation" aria-expanded={!sidebarCollapsed} aria-label={sidebarCollapsed ? '展开侧边栏' : '收起侧边栏'} title={sidebarCollapsed ? '展开侧边栏' : '收起侧边栏'} onClick={() => setSidebarCollapsed((collapsed) => !collapsed)}>
          {sidebarCollapsed ? <ChevronRight size={14}/> : <ChevronLeft size={14}/>}
        </button>

        <nav className="nav" id="primary-navigation" aria-label="主导航">
          {['工作', '交付', '管理', '帮助'].map(group => {
            const items = links.filter(link => link.group === group && (!link.environmentOwnerOnly || user.role === 'environment_owner') && (!link.platformAdminOnly || user.role === 'platform_admin'));
            return items.length ? <div className="nav__group" key={group}><div className="nav__caption">{group}</div>{items.map(({to, label, icon: Icon, end}) => <NavLink key={to} to={to} end={end} aria-label={label} title={label} className={({isActive}) => `nav__link${isActive ? ' active' : ''}`}><Icon size={18}/><span>{label}</span></NavLink>)}</div> : null;
          })}
        </nav>

        <div className="sidebar__foot">
          <div className="runtime-state">
            <span className={connected ? 'signal signal--online' : 'signal'}/>
            <div>
              <strong>{connected ? '实时通道在线' : '等待实时通道'}</strong>
              <small>SSE · 单实例 Demo</small>
            </div>
            <Radio size={15}/>
          </div>
          <div className="security-note"><ShieldCheck size={15}/> 操作均受后端 RBAC 保护</div>
        </div>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div className="demo-ribbon">
            <span>DEMO</span>
            无密码身份模式，仅用于本地演示
          </div>
          <div className="topbar__actions">
            <NavLink className={({ isActive }) => `topbar__notification${isActive ? ' active' : ''}`} to="/notifications" aria-label="通知中心" title="通知中心">
              <Bell size={18}/>
            </NavLink>
            <div className="identity-switcher"><Select aria-label="切换演示身份" value={user.id} disabled={switching} onChange={id => void switchUser(id)} options={users.map(candidate => ({value: candidate.id, label: `${ROLE_LABELS[candidate.role]} · ${candidate.name}`}))} /></div>
          </div>
        </header>
        <main className="main-content">
          <Suspense fallback={<LoadingBlock label="正在加载页面…"/>}>
            <Outlet />
          </Suspense>
        </main>
      </div>
    </div>);
}
