import { useCallback, useEffect, useRef, useState } from "react";
import { isQuotaAuthError } from "./quotaAlertPresentation";

/** One request generation owns a snapshot. A failed refresh retains its data. */
export function useQuotaSnapshot<T>(
  read: (signal: AbortSignal) => Promise<T>,
  key: string,
  onAuthBlocked?: (error: unknown) => void,
) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [loading, setLoading] = useState(true);
  const [readAt, setReadAt] = useState<string>();
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const reader = useRef(read);
  const authHandler = useRef(onAuthBlocked);
  useEffect(() => {
    authHandler.current = onAuthBlocked;
  }, [onAuthBlocked]);
  useEffect(() => {
    reader.current = read;
  }, [read]);
  const invalidate = useCallback(() => {
    ++generation.current;
    controller.current?.abort();
  }, []);
  const refresh = useCallback(async () => {
    const id = ++generation.current;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    setLoading(true);
    try {
      const next = await reader.current(abort.signal);
      if (id !== generation.current || abort.signal.aborted) return false;
      setData(next);
      setError(undefined);
      setReadAt(new Date().toISOString());
      return true;
    } catch (cause) {
      if (id === generation.current && !abort.signal.aborted) {
        setError(cause);
        if (isQuotaAuthError(cause)) authHandler.current?.(cause);
      }
      return false;
    } finally {
      if (id === generation.current && !abort.signal.aborted) setLoading(false);
    }
  }, []);
  useEffect(() => {
    setData(undefined);
    setError(undefined);
    setReadAt(undefined);
    void refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "hidden") void refresh();
    }, 30000);
    const visibility = () => {
      if (document.visibilityState !== "hidden") void refresh();
    };
    document.addEventListener("visibilitychange", visibility);
    return () => {
      invalidate();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, [key, refresh, invalidate]);
  const update = (change: (old: T) => T) => {
    invalidate();
    setData((old) => (old === undefined ? old : change(old)));
    setError(undefined);
    setLoading(false);
  };
  return { data, error, loading, readAt, refresh, update };
}

export function quotaResult<T>(result: { data?: T; error?: unknown }): T {
  if (result.error || result.data === undefined)
    throw result.error ?? new Error("unavailable");
  return result.data;
}
