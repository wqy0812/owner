import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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

  const data = storedData?.scope === scope ? storedData.value : undefined;

  const reload = useCallback(async () => {
    controller.current?.abort();
    const nextController = new AbortController();
    controller.current = nextController;
    const current = ++requestId.current;
    setLoading(true);
    setError(undefined);
    try {
      const next = await loader(nextController.signal);
      if (current === requestId.current && !nextController.signal.aborted) {
        setStoredData({ scope, value: next });
      }
    } catch (reason) {
      if (current === requestId.current && !nextController.signal.aborted && !isAbortError(reason)) {
        setError(displayError(reason));
      }
    } finally {
      if (current === requestId.current) setLoading(false);
    }
    // The caller deliberately supplies stable dependencies for its loader.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope, ...dependencies]);

  useEffect(() => {
    void reload();
    return () => controller.current?.abort();
  }, [reload, refreshToken]);

  const setData = useCallback((value: T | undefined) => {
    setStoredData(value === undefined ? undefined : { scope, value });
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
