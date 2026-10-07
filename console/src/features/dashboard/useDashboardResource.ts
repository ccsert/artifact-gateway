import { useCallback, useEffect, useRef, useState } from "react";

/** Each overview source owns its loading state and latest request. */
export function useDashboardResource<T>(
  read: (signal: AbortSignal) => Promise<T>,
) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [loading, setLoading] = useState(true);
  const generation = useRef(0);
  const controller = useRef<AbortController>(undefined);

  const invalidate = useCallback(() => {
    ++generation.current;
    controller.current?.abort();
  }, []);

  const reload = useCallback(async () => {
    const id = ++generation.current;
    controller.current?.abort();
    const next = new AbortController();
    controller.current = next;
    setLoading(true);
    setError(undefined);
    try {
      const result = await read(next.signal);
      if (id === generation.current && !next.signal.aborted) setData(result);
    } catch (cause) {
      if (id === generation.current && !next.signal.aborted) setError(cause);
    } finally {
      if (id === generation.current && !next.signal.aborted) setLoading(false);
    }
  }, [read]);

  useEffect(() => {
    void reload();
    return invalidate;
  }, [reload, invalidate]);

  return { data, error, loading, reload };
}
