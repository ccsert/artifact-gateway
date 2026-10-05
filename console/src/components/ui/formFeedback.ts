import { useEffect, useRef, useState } from "react";

/** Focus after React has committed the field errors, including repeated submits. */
export function useFormValidationFocus() {
  const formRef = useRef<HTMLFormElement>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (attempt === 0) return;
    formRef.current
      ?.querySelector<HTMLInputElement>(
        'input[aria-invalid="true"]:not(:disabled)',
      )
      ?.focus();
  }, [attempt]);
  return {
    formRef,
    attempted: attempt > 0,
    reportInvalid: () => setAttempt((old) => old + 1),
  };
}

export function fieldFeedback(id: string, error?: string, hint = false) {
  return {
    id,
    status: error ? ("error" as const) : undefined,
    "aria-invalid": error ? true : undefined,
    "aria-describedby":
      [hint && `${id}-hint`, error && `${id}-error`]
        .filter(Boolean)
        .join(" ") || undefined,
  };
}
