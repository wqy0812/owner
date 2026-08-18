import { Navigate, Route, Routes } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { ComponentsPage } from './pages/ComponentsPage';
import { DashboardPage } from './pages/DashboardPage';
import { EnvironmentsPage } from './pages/EnvironmentsPage';
import { NotificationsPage } from './pages/NotificationsPage';
import { OperationManualPage } from './pages/OperationManualPage';
import { RunsPage } from './pages/RunsPage';
import { ScenariosPage } from './pages/ScenariosPage';

export function App() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<DashboardPage />} />
        <Route path="components" element={<ComponentsPage />} />
        <Route path="scenarios" element={<ScenariosPage />} />
        <Route path="environments" element={<EnvironmentsPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="notifications" element={<NotificationsPage />} />
        <Route path="manual" element={<OperationManualPage />} />
        <Route path="manual/:section" element={<OperationManualPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
