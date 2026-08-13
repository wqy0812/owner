import { useCallback, useEffect, useRef, useState } from 'react';
import { displayError, useApp } from '../context/AppContext';

export function useApiData<T>(loader: () => Promise<T>, dependencies: unknown[] = []) {
  const { refreshToken } = useApp();
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const requestId = useRef(0);

  const reload = useCallback(async () => {
    const current = ++requestId.current;
    setLoading(true);
    setError(undefined);
    try {
      const next = await loader();
      if (current === requestId.current) setData(next);
    } catch (reason) {
      if (current === requestId.current) setError(displayError(reason));
    } finally {
      if (current === requestId.current) setLoading(false);
    }
    // The caller deliberately supplies stable dependencies for its loader.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, dependencies);

  useEffect(() => {
    void reload();
  }, [reload, refreshToken]);

  return { data, loading, error, reload, setData };
}
