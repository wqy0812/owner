import { lazy } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { AppShell } from './components/AppShell';

const ComponentsPage = lazy(() => import('./pages/ComponentsPage').then((module) => ({ default: module.ComponentsPage })));
const DashboardPage = lazy(() => import('./pages/DashboardPage').then((module) => ({ default: module.DashboardPage })));
const DisasterRecoveryPage = lazy(() => import('./pages/DisasterRecoveryPage').then((module) => ({ default: module.DisasterRecoveryPage })));
const EnvironmentsPage = lazy(() => import('./pages/EnvironmentsPage').then((module) => ({ default: module.EnvironmentsPage })));
const NotificationsPage = lazy(() => import('./pages/NotificationsPage').then((module) => ({ default: module.NotificationsPage })));
const PlatformManualPage = lazy(() => import('./pages/PlatformManualPage').then((module) => ({ default: module.PlatformManualPage })));
const RunsPage = lazy(() => import('./pages/RunsPage').then((module) => ({ default: module.RunsPage })));
const ScenariosPage = lazy(() => import('./pages/ScenariosPage').then((module) => ({ default: module.ScenariosPage })));
const PlatformManagementPage = lazy(() => import('./pages/PlatformManagementPage').then((module) => ({ default: module.PlatformManagementPage })));


function DefaultPage() {
  return <DashboardPage />;
}

export function App() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<DefaultPage />} />
        <Route path="components" element={<ComponentsPage />} />
        <Route path="scenarios" element={<ScenariosPage />} />
        <Route path="environments" element={<EnvironmentsPage />} />
        <Route path="reference-rebuild" element={<Navigate to="/scenarios" replace />} />
        <Route path="disaster-recovery" element={<DisasterRecoveryPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="notifications" element={<NotificationsPage />} />
        <Route path="platform-management" element={<PlatformManagementPage />} />
        <Route path="manual" element={<PlatformManualPage />} />
        <Route path="manual/:section" element={<PlatformManualPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
