import { QueryCache, QueryClient } from "@tanstack/react-query";
import type { Problem } from "../../client";

/**
 * The Console's server-state layer (issue 271).
 *
 * Generated SDK calls resolve to `{ data, error }` instead of throwing. Queries
 * go through `unwrap`, which throws the SDK's own error value unchanged, so the
 * existing `ErrorBanner` and `isNotFound` keep reading Problem documents and
 * plain-text gateway answers exactly as before.
 */
export async function unwrap<T>(
  request: Promise<{ data?: T; error?: unknown }>,
): Promise<T> {
  const { data, error } = await request;
  if (error !== undefined && error !== null) throw error;
  return data as T;
}

function statusOf(error: unknown): number | undefined {
  const status = (error as Problem | undefined)?.status;
  return typeof status === "number" ? status : undefined;
}

/**
 * Retry once, and only when a second attempt can change the answer: the
 * network failed (no status) or the gateway answered 5xx. Authorization,
 * validation and not-found answers are final.
 */
export function shouldRetry(failureCount: number, error: unknown): boolean {
  if (failureCount >= 1) return false;
  const status = statusOf(error);
  return status === undefined ? typeof error !== "string" : status >= 500;
}

export function createQueryClient(onAuthFailure?: (status: 401 | 403) => void) {
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error) => {
        const status = statusOf(error);
        if (status === 401 || status === 403) onAuthFailure?.(status);
      },
    }),
    defaultOptions: {
      queries: {
        // Operators expect what they just changed to be on screen; mutations
        // invalidate precisely, so a short freshness window is enough to
        // de-duplicate the reads that several sections issue on mount.
        staleTime: 30_000,
        retry: shouldRetry,
        refetchOnWindowFocus: false,
      },
      mutations: { retry: false },
    },
  });
}
