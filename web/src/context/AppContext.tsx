import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, eventsURL } from '../api/client';
import { RunActivityEvents } from './runActivityEvents';
import { ROLE_LABELS, type PlatformOptionCategory, type User } from '../types/domain';

export const DEMO_USERS: User[] = [
  { id: 'component-alice', name: '林晓', role: 'component_owner' },
  { id: 'component-bob', name: '周工', role: 'component_owner' },
  { id: 'scenario-carol', name: '陈晨', role: 'scenario_owner' },
  { id: 'environment-dave', name: '王维', role: 'environment_owner' },
  { id: 'platform-admin', name: '赵宁', role: 'platform_admin' },
];

interface Toast {
  id: number;
  tone: 'success' | 'error' | 'info';
  title: string;
  message?: string;
}

export type RefreshTarget = 'components' | 'scenarios' | 'environments' | 'runs' | 'notifications' | 'workbench' | 'catalog-repository' | 'platform-options' | 'environment-variable-definitions' | 'environment-parameter-fields';

export const ALL_REFRESH_TARGETS: readonly RefreshTarget[] = ['components', 'scenarios', 'environments', 'runs', 'notifications', 'workbench', 'catalog-repository', 'platform-options', 'environment-variable-definitions', 'environment-parameter-fields'];

type RefreshTokens = Record<RefreshTarget, number>;

interface AppContextValue {
  runActivityEvents: RunActivityEvents;
  user: User;
  users: User[];
  switching: boolean;
  connected: boolean;
  switchUser: (id: string) => Promise<void>;
  notify: (tone: Toast['tone'], title: string, message?: string) => void;
  refreshTokens: RefreshTokens;
  signalRefresh: (targets?: RefreshTarget | readonly RefreshTarget[]) => void;
  scheduleRefresh: (targets: RefreshTarget | readonly RefreshTarget[]) => void;
  platformOptionCategories: PlatformOptionCategory[];
  platformOptionsLoading: boolean;
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
  const runActivityEvents = useMemo(() => new RunActivityEvents(), [user.id]);
  const [users, setUsers] = useState<User[]>(DEMO_USERS);
  const [sessionReady, setSessionReady] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [connected, setConnected] = useState(false);
  const [platformOptionCategories, setPlatformOptionCategories] = useState<PlatformOptionCategory[]>([]);
  const [platformOptionsLoading, setPlatformOptionsLoading] = useState(true);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [refreshTokens, setRefreshTokens] = useState<RefreshTokens>({
    components: 0,
    scenarios: 0,
    environments: 0,
    runs: 0,
    notifications: 0,
    workbench: 0,
    'catalog-repository': 0,
    'platform-options': 0,
    'environment-variable-definitions': 0,
    'environment-parameter-fields': 0,
  });
  const pendingRefreshTargets = useRef(new Set<RefreshTarget>());
  const refreshTimer = useRef<number>();
  const platformOptionsLoaded = useRef(false);
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

  useEffect(() => {
    if (!sessionReady) return;
    const controller = new AbortController();
    setPlatformOptionsLoading(!platformOptionsLoaded.current);
    void api.platformOptionCategories(controller.signal)
      .then((categories) => {
        platformOptionsLoaded.current = true;
        setPlatformOptionCategories(categories);
      })
      .catch((error) => { if (!(error instanceof DOMException && error.name === 'AbortError')) notify('error', '加载平台选项失败', displayError(error)); })
      .finally(() => { if (!controller.signal.aborted) setPlatformOptionsLoading(false); });
    return () => controller.abort();
  }, [notify, refreshTokens['platform-options'], sessionReady, user.id]);

  const switchUser = useCallback(
    async (id: string) => {
      const fallback = users.find((item) => item.id === id);
      if (!fallback || fallback.id === user.id) return;
      setSwitching(true);
      try {
        const next = await api.switchUser(id);
        setUser({ ...fallback, ...next });
        signalRefresh();
        notify('success', '身份已切换', `当前以${ROLE_LABELS[fallback.role]} · ${fallback.name}身份浏览。`);
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
        .then((items) => items.length ? items.map((item) => { const demo = DEMO_USERS.find((candidate) => candidate.id === item.id); return { ...demo, ...item }; }) : DEMO_USERS)
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
    let opened = false;
    stream.onopen = () => {
      setConnected(true);
      if (opened) { runActivityEvents.refresh(); scheduleRefresh(ALL_REFRESH_TARGETS); }
      opened = true;
    };
    stream.onerror = () => setConnected(false);
    stream.onmessage = () => scheduleRefresh(ALL_REFRESH_TARGETS);
    const activity = (event: Event, type: 'log' | 'state') => {
      try {
        const value = JSON.parse((event as MessageEvent).data);
        if (typeof value.runId === 'string') runActivityEvents.emit(value.runId, { type, logId: typeof value.logId === 'number' ? value.logId : undefined });
      } catch { /* Invalid events cannot invalidate unrelated resources. */ }
    };
    const onVisible = () => { if (!document.hidden) { runActivityEvents.refresh(); scheduleRefresh(ALL_REFRESH_TARGETS); } };
    document.addEventListener('visibilitychange', onVisible);
    const listeners: Array<[string, (event: Event) => void]> = [
      ['notification', () => scheduleRefresh(['notifications', 'workbench'])],
      ['run.log', event => activity(event, 'log')],
      ['run.updated', event => { activity(event, 'state'); scheduleRefresh(['components', 'runs', 'environments', 'scenarios', 'workbench']); }],
      ['approval.updated', () => scheduleRefresh(['runs', 'workbench'])],
      ['release.published', () => scheduleRefresh(['components', 'notifications', 'workbench'])],
      ['component_release.review_updated', () => scheduleRefresh(['components', 'workbench'])],
      ['component_release.deprecated', () => scheduleRefresh(['components', 'workbench'])],
      ['component_release.restored', () => scheduleRefresh(['components', 'workbench'])],
      ['component_release.deleted', () => scheduleRefresh(['components', 'workbench'])],
      ['scenario.published', () => scheduleRefresh(['scenarios', 'workbench'])],
      ['catalog_backup.updated', () => scheduleRefresh(['catalog-repository', 'workbench'])],
      ['scenario.deleted', () => scheduleRefresh(['scenarios', 'workbench'])],
      ['environment.deleted', () => scheduleRefresh(['environments', 'workbench'])],
      ['environment.archived', () => scheduleRefresh(['environments', 'workbench'])],
      ['environment.unarchived', () => scheduleRefresh(['environments', 'workbench'])],
      ['platform_options.updated', () => scheduleRefresh(['platform-options', 'components', 'environments', 'scenarios'])],
      ['platform_parameters.updated', () => scheduleRefresh(['environment-variable-definitions', 'environment-parameter-fields'])],
    ];
    for (const [event, listener] of listeners) stream.addEventListener(event, listener);
    return () => {
      stream.close();
      document.removeEventListener('visibilitychange', onVisible);
      for (const [event, listener] of listeners) stream.removeEventListener(event, listener);
      if (refreshTimer.current !== undefined) {
        window.clearTimeout(refreshTimer.current);
        refreshTimer.current = undefined;
      }
      pendingRefreshTargets.current.clear();
    };
  }, [runActivityEvents, scheduleRefresh, sessionReady, user.id]);

  const value = useMemo(
    () => ({ user, users, switching, connected, switchUser, notify, refreshTokens, signalRefresh, scheduleRefresh, platformOptionCategories, platformOptionsLoading, runActivityEvents }),
    [connected, notify, platformOptionCategories, platformOptionsLoading, refreshTokens, scheduleRefresh, signalRefresh, switchUser, switching, user, users, runActivityEvents],
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

export { displayError };
