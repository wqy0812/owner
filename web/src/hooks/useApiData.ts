import { useCallback, useEffect, useMemo, useRef, useState, type SetStateAction } from 'react';
import { displayError, type RefreshTarget, useApp } from '../context/AppContext';

interface ScopedData<T> {
  scope: string;
  value: T;
}

function isAbortError(reason: unknown): boolean {
  return (reason instanceof DOMException || reason instanceof Error) && reason.name === 'AbortError';
}

export function useApiData<T>(
  loader: (signal: AbortSignal) => Promise<T>,
  dependencies: readonly unknown[] = [],
  refreshTargets: RefreshTarget | readonly RefreshTarget[] = 'components',
  shouldPoll?: (data: T) => boolean,
) {
  const { refreshTokens } = useApp();
  const targets: readonly RefreshTarget[] = typeof refreshTargets === 'string' ? [refreshTargets] : refreshTargets;
  const refreshToken = targets.map((target) => refreshTokens[target]).join(':');
  const scope = JSON.stringify(dependencies);
  const [storedData, setStoredData] = useState<ScopedData<T>>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const requestId = useRef(0);
  const controller = useRef<AbortController>();
  const inFlight = useRef(false);
  const refreshQueued = useRef(false);
  const lastRefreshToken = useRef(refreshToken);

  const data = storedData?.scope === scope ? storedData.value : undefined;

  const reload = useCallback(async () => {
    controller.current?.abort();
    const nextController = new AbortController();
    controller.current = nextController;
    const current = ++requestId.current;
    inFlight.current = true;
    setLoading(true);
    setError(undefined);
    try {
      do {
        refreshQueued.current = false;
        try {
          const next = await loader(nextController.signal);
          if (current === requestId.current && !nextController.signal.aborted) {
            setStoredData({ scope, value: next });
            setError(undefined);
          }
        } catch (reason) {
          if (current === requestId.current && !nextController.signal.aborted && !isAbortError(reason)) {
            setError(displayError(reason));
          }
        }
        // Continuous SSE invalidations must not starve a slow request. Finish
        // it, then perform one follow-up read for all events received meanwhile.
      } while (current === requestId.current && !nextController.signal.aborted && refreshQueued.current);
    } finally {
      if (current === requestId.current && !nextController.signal.aborted) {
        inFlight.current = false;
        setLoading(false);
      }
    }
    // The caller deliberately supplies stable dependencies for its loader.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope, ...dependencies]);

  useEffect(() => {
    lastRefreshToken.current = refreshToken;
    void reload();
    return () => controller.current?.abort();
    // Scope changes and explicit reloads supersede old requests immediately.
    // Refresh tokens are handled separately so they cannot abort active reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reload]);

  useEffect(() => {
    if (lastRefreshToken.current === refreshToken) return;
    lastRefreshToken.current = refreshToken;
    if (inFlight.current && !controller.current?.signal.aborted) {
      refreshQueued.current = true;
    } else {
      void reload();
    }
  }, [reload, refreshToken]);

  const polling = data !== undefined && Boolean(shouldPoll?.(data));
  useEffect(() => {
    if (!polling) return;
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      if (inFlight.current && !controller.current?.signal.aborted) refreshQueued.current = true;
      else void reload();
    }, 10000);
    return () => window.clearInterval(timer);
  }, [polling, reload]);

  const setData = useCallback((update: SetStateAction<T | undefined>) => {
    setStoredData((current) => {
      const previous = current?.scope === scope ? current.value : undefined;
      const value = typeof update === 'function' ? (update as (previous: T | undefined) => T | undefined)(previous) : update;
      return value === undefined ? undefined : { scope, value };
    });
  }, [scope]);

  return useMemo(() => ({
    data,
    loading,
    error,
    isInitialLoading: loading && !data,
    isRefreshing: loading && Boolean(data),
    stale: Boolean(data && error),
    reload,
    setData,
  }), [data, error, loading, reload, setData]);
}
