import {
  Bell,
  Boxes,
  ChevronDown,
  CircleGauge,
  CloudCog,
  Network,
  PlayCircle,
  Radio,
  ShieldCheck,
} from 'lucide-react';
import { NavLink, Outlet } from 'react-router-dom';
import { useApp } from '../context/AppContext';
import { ROLE_LABELS } from '../types/domain';

const links = [
  { to: '/', label: '概览', icon: CircleGauge, end: true },
  { to: '/components', label: '组件', icon: Boxes },
  { to: '/scenarios', label: '场景', icon: Network },
  { to: '/environments', label: '环境', icon: CloudCog },
  { to: '/runs', label: '运行', icon: PlayCircle },
  { to: '/notifications', label: '通知', icon: Bell },
];

export function AppShell() {
  const { user, users, switchUser, switching, connected } = useApp();

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand__mark"><Boxes size={21} /></span>
          <div>
            <strong>ClusterForge</strong>
            <small>交付编排中心</small>
          </div>
        </div>

        <nav className="nav" aria-label="主导航">
          <div className="nav__caption">工作台</div>
          {links.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} className={({ isActive }) => `nav__link${isActive ? ' active' : ''}`}>
              <Icon size={18} />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="sidebar__foot">
          <div className="runtime-state">
            <span className={connected ? 'signal signal--online' : 'signal'} />
            <div>
              <strong>{connected ? '实时通道在线' : '等待实时通道'}</strong>
              <small>SSE · 单实例 Demo</small>
            </div>
            <Radio size={15} />
          </div>
          <div className="security-note"><ShieldCheck size={15} /> 操作均受后端 RBAC 保护</div>
        </div>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div className="demo-ribbon">
            <span>DEMO</span>
            无密码身份模式，仅用于本地演示
          </div>
          <label className="identity-switcher">
            <span className="identity-avatar">{user.name.slice(0, 1)}</span>
            <span className="identity-copy">
              <small>{ROLE_LABELS[user.role]}</small>
              <strong>{user.name}</strong>
            </span>
            <select
              aria-label="切换演示身份"
              value={user.id}
              disabled={switching}
              onChange={(event) => void switchUser(event.target.value)}
            >
              {users.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {candidate.title} · {candidate.name}
                </option>
              ))}
            </select>
            <ChevronDown size={15} />
          </label>
        </header>
        <main className="main-content"><Outlet /></main>
      </div>
    </div>
  );
}
