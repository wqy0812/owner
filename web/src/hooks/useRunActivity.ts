import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import type { RunActivity } from '../types/domain';

const active = (status?: string) => status === 'running' || status === 'queued' || status === 'awaiting_approval';

export function useRunActivity(runId: string | undefined, status?: string) {
  const { user, runActivityEvents, scheduleRefresh } = useApp();
  const scope = `${user.id}:${runId ?? ''}`;
  const [stored, setStored] = useState<{ scope: string; data?: RunActivity; error?: string; loading: boolean }>();
  const currentStatus = useRef(status);
  currentStatus.current = status;
  const reloadRef = useRef<() => void>(() => {});
  useEffect(() => {
    if (!runId) return;
    const controller = new AbortController();
    let data: RunActivity | undefined;
    let running = false, queued = false;
    let timer: number | undefined;
    const schedule = () => {
      if (document.hidden || controller.signal.aborted) return;
      if (running) { queued = true; return; }
      if (timer === undefined) timer = window.setTimeout(() => { timer = undefined; void read(); }, 400);
    };
    const read = async () => {
      if (document.hidden || controller.signal.aborted) return;
      if (running) { queued = true; return; }
      running = true; queued = false;
      setStored({ scope, data, loading: true });
      try {
        let more: boolean;
        do {
          const next = await api.runActivity(runId, data?.nextAfterId, controller.signal);
          if (controller.signal.aborted) return;
          if (((data?.status ?? currentStatus.current) && (data?.status ?? currentStatus.current) !== next.status) || (data && data.archived !== next.archived)) {
            scheduleRefresh(['runs', 'components', 'environments', 'scenarios', 'workbench']);
          }
          const logs = new Map((data?.logs ?? []).map(log => [log.id, log]));
          for (const log of next.logs) logs.set(log.id, log);
          data = { ...next, logs: next.archived ? [] : [...logs.values()].sort((a, b) => a.id - b.id).slice(-2000) };
          setStored({ scope, data, loading: true });
          more = next.hasMore;
        } while (more && !controller.signal.aborted && !document.hidden);
        setStored({ scope, data, loading: false });
      } catch (error) {
        if (!controller.signal.aborted) setStored({ scope, data, error: displayError(error), loading: false });
      } finally {
        running = false;
        if (queued) schedule();
      }
    };
    reloadRef.current = () => { void read(); };
    const unsubscribe = runActivityEvents.subscribe(runId, event => {
      if (event.type === 'log' && event.logId !== undefined && data && event.logId <= data.nextAfterId) return;
      if (event.type === 'refresh') void read(); else schedule();
    });
    const poll = window.setInterval(() => { if (active(data?.status ?? currentStatus.current)) void read(); }, 10000);
    void read();
    return () => { controller.abort(); unsubscribe(); window.clearInterval(poll); window.clearTimeout(timer); reloadRef.current = () => {}; };
  }, [runId, scope, runActivityEvents, scheduleRefresh]);
  return { data: stored?.scope === scope ? stored.data : undefined, error: stored?.scope === scope ? stored.error : undefined,
    loading: stored?.scope !== scope || stored.loading, reload: () => reloadRef.current() };
}
