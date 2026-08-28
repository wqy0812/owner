import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, eventsURL } from '../api/client';
import type { Role, User } from '../types/domain';

export const DEMO_USERS: User[] = [
  { id: 'component-alice', name: '林晓 · Runtime', role: 'component_owner', title: '组件 Owner A' },
  { id: 'component-bob', name: '周工 · Kubernetes', role: 'component_owner', title: '组件 Owner B' },
  { id: 'scenario-carol', name: '陈晨 · 集群交付', role: 'scenario_owner', title: '场景 Owner' },
  { id: 'environment-dave', name: '王维 · 基础设施', role: 'environment_owner', title: '环境 Owner' },
];

interface Toast {
  id: number;
  tone: 'success' | 'error' | 'info';
  title: string;
  message?: string;
}

export type RefreshTarget = 'components' | 'scenarios' | 'environments' | 'runs' | 'notifications' | 'workbench';

export const ALL_REFRESH_TARGETS: readonly RefreshTarget[] = ['components', 'scenarios', 'environments', 'runs', 'notifications', 'workbench'];

type RefreshTokens = Record<RefreshTarget, number>;

interface AppContextValue {
  user: User;
  users: User[];
  switching: boolean;
  connected: boolean;
  switchUser: (id: string) => Promise<void>;
  notify: (tone: Toast['tone'], title: string, message?: string) => void;
  refreshTokens: RefreshTokens;
  signalRefresh: (targets?: RefreshTarget | readonly RefreshTarget[]) => void;
}

const AppContext = createContext<AppContextValue | undefined>(undefined);

function displayError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 403) return `权限不足：${error.message}`;
    return error.message;
  }
  return error instanceof Error ? error.message : '发生未知错误';
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User>(DEMO_USERS[0]);
  const [users, setUsers] = useState<User[]>(DEMO_USERS);
  const [sessionReady, setSessionReady] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [connected, setConnected] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [refreshTokens, setRefreshTokens] = useState<RefreshTokens>({
    components: 0,
    scenarios: 0,
    environments: 0,
    runs: 0,
    notifications: 0,
    workbench: 0,
  });
  const pendingRefreshTargets = useRef(new Set<RefreshTarget>());
  const refreshTimer = useRef<number>();
  const sessionInitialization = useRef<Promise<{ user: User; users: User[]; switched: boolean }>>();

  const notify = useCallback((tone: Toast['tone'], title: string, message?: string) => {
    const id = Date.now() + Math.random();
    setToasts((items) => [...items, { id, tone, title, message }]);
    window.setTimeout(() => setToasts((items) => items.filter((item) => item.id !== id)), 4600);
  }, []);

  const signalRefresh = useCallback((targets: RefreshTarget | readonly RefreshTarget[] = ALL_REFRESH_TARGETS) => {
    const affected: readonly RefreshTarget[] = typeof targets === 'string' ? [targets] : targets;
    setRefreshTokens((current) => {
      const next = { ...current };
      for (const target of affected) next[target] += 1;
      return next;
    });
  }, []);

  const scheduleRefresh = useCallback((targets: RefreshTarget | readonly RefreshTarget[]) => {
    const affected: readonly RefreshTarget[] = typeof targets === 'string' ? [targets] : targets;
    for (const target of affected) pendingRefreshTargets.current.add(target);
    if (refreshTimer.current !== undefined) return;
    refreshTimer.current = window.setTimeout(() => {
      const affected = [...pendingRefreshTargets.current];
      pendingRefreshTargets.current.clear();
      refreshTimer.current = undefined;
      if (affected.length) signalRefresh(affected);
    }, 400);
  }, [signalRefresh]);

  const switchUser = useCallback(
    async (id: string) => {
      const fallback = users.find((item) => item.id === id);
      if (!fallback || fallback.id === user.id) return;
      setSwitching(true);
      try {
        const next = await api.switchUser(id);
        setUser({ ...fallback, ...next });
        signalRefresh();
        notify('success', '身份已切换', `当前以${fallback.title}身份浏览。`);
      } catch (error) {
        notify('error', '身份切换失败', displayError(error));
      } finally {
        setSwitching(false);
      }
    },
    [notify, signalRefresh, user.id, users],
  );

  useEffect(() => {
    let active = true;
    if (!sessionInitialization.current) sessionInitialization.current = (async () => {
      const availableUsers = await api.sessionUsers()
        .then((items) => items.length ? items.map((item) => ({ ...DEMO_USERS.find((candidate) => candidate.id === item.id), ...item })) : DEMO_USERS)
        .catch(() => DEMO_USERS);
      try {
        return { user: await api.me(), users: availableUsers, switched: false };
      } catch (error) {
        if (error instanceof ApiError && (error.status === 401 || error.status === 404)) {
          return { user: await api.switchUser(availableUsers[0].id), users: availableUsers, switched: true };
        }
        throw error;
      }
    })();
    void sessionInitialization.current
      .then(({ user: me, users: availableUsers, switched }) => {
        if (!active) return;
        const demo = availableUsers.find((item) => item.id === me.id);
        setUsers(availableUsers);
        setUser({ ...(demo ?? availableUsers[0]), ...me });
        if (switched) signalRefresh();
      })
      .catch(() => {
        // Protected page requests will surface a useful API error after
        // initialization instead of racing the session bootstrap.
      })
      .finally(() => {
        if (active) setSessionReady(true);
      });
    return () => {
      active = false;
    };
  }, [signalRefresh]);

  useEffect(() => {
    if (!sessionReady) return;
    const stream = new EventSource(eventsURL(), { withCredentials: true });
    stream.onopen = () => setConnected(true);
    stream.onerror = () => setConnected(false);
    stream.onmessage = () => scheduleRefresh(ALL_REFRESH_TARGETS);
    const listeners: Array<[string, () => void]> = [
      ['notification', () => scheduleRefresh(['notifications', 'workbench'])],
      ['run.log', () => scheduleRefresh('runs')],
      ['run.updated', () => scheduleRefresh(['components', 'runs', 'environments', 'scenarios', 'workbench'])],
      ['approval.updated', () => scheduleRefresh(['runs', 'workbench'])],
      ['release.published', () => scheduleRefresh(['components', 'notifications', 'workbench'])],
      ['scenario.published', () => scheduleRefresh(['scenarios', 'workbench'])],
      ['scenario.deleted', () => scheduleRefresh(['scenarios', 'workbench'])],
      ['environment.deleted', () => scheduleRefresh(['environments', 'workbench'])],
      ['environment.archived', () => scheduleRefresh(['environments', 'workbench'])],
      ['environment.unarchived', () => scheduleRefresh(['environments', 'workbench'])],
    ];
    for (const [event, listener] of listeners) stream.addEventListener(event, listener);
    return () => {
      stream.close();
      for (const [event, listener] of listeners) stream.removeEventListener(event, listener);
      if (refreshTimer.current !== undefined) {
        window.clearTimeout(refreshTimer.current);
        refreshTimer.current = undefined;
      }
      pendingRefreshTargets.current.clear();
    };
  }, [scheduleRefresh, sessionReady, user.id]);

  const value = useMemo(
    () => ({ user, users, switching, connected, switchUser, notify, refreshTokens, signalRefresh }),
    [connected, notify, refreshTokens, signalRefresh, switchUser, switching, user, users],
  );

  return (
    <AppContext.Provider value={value}>
      {sessionReady ? children : <div className="state-block" role="status">正在初始化 Demo 身份…</div>}
      <div className="toast-stack" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast toast--${toast.tone}`}>
            <strong>{toast.title}</strong>
            {toast.message && <span>{toast.message}</span>}
            <button type="button" aria-label="关闭提示" onClick={() => setToasts((items) => items.filter((i) => i.id !== toast.id))}>
              ×
            </button>
          </div>
        ))}
      </div>
    </AppContext.Provider>
  );
}

export function useApp(): AppContextValue {
  const context = useContext(AppContext);
  if (!context) throw new Error('useApp must be used within AppProvider');
  return context;
}

export function canManage(role: Role, resource: 'component' | 'scenario' | 'environment'): boolean {
  return (
    (role === 'component_owner' && resource === 'component') ||
    (role === 'scenario_owner' && resource === 'scenario') ||
    (role === 'environment_owner' && resource === 'environment')
  );
}

export { displayError };
