import { useEffect, useRef, useState } from "react";
import { isQuotaAuthError } from "./quotaAlertPresentation";

export function useQuotaAction(onAuthBlocked?: (error: unknown) => void) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const lock = useRef(false);
  const alive = useRef(true);
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      controller.current?.abort();
    };
  }, []);
  const run = async <T>(
    action: (signal: AbortSignal) => Promise<T>,
    success: (data: T) => void,
  ) => {
    if (lock.current || !alive.current) return;
    lock.current = true;
    setBusy(true);
    setError(undefined);
    const abort = new AbortController();
    controller.current = abort;
    try {
      const data = await action(abort.signal);
      if (alive.current && !abort.signal.aborted) success(data);
    } catch (cause) {
      if (alive.current && !abort.signal.aborted) {
        setError(cause);
        if (isQuotaAuthError(cause)) onAuthBlocked?.(cause);
      }
    } finally {
      lock.current = false;
      if (alive.current && !abort.signal.aborted) setBusy(false);
    }
  };
  const blocked =
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    [
      "version_conflict",
      "password_change_required",
      "permission_denied",
      "forbidden",
      "access_denied",
      "unauthenticated",
      "rule_deleted",
    ].includes(String(error.code));
  return {
    busy,
    error,
    blocked: blocked && (!onAuthBlocked || !isQuotaAuthError(error)),
    run,
  };
}
