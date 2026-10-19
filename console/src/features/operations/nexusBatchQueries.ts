import {
  queryOptions,
  useInfiniteQuery,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  getNexusMigrationCapabilities,
  getNexusMigrationBatch,
  listNexusMigrationBatches,
  listNexusMigrationEntries,
  getRepository,
} from "../../client";
import { batchActive, batchError, batchRequest } from "./nexusBatchModel";

export const batchKeys = {
  all: ["nexus-migration-batches"] as const,
  capabilities: ["nexus-migration-batches", "capabilities"] as const,
  list: ["nexus-migration-batches", "list"] as const,
  detail: (id: string) => ["nexus-migration-batches", "detail", id] as const,
  entries: (id: string) => ["nexus-migration-batches", "entries", id] as const,
};
export function useBatchCapabilities() {
  return useQuery({
    queryKey: batchKeys.capabilities,
    queryFn: ({ signal }) =>
      batchRequest(getNexusMigrationCapabilities({ signal })),
    staleTime: 0,
  });
}
export function batchOptions(id: string) {
  return queryOptions({
    queryKey: batchKeys.detail(id),
    enabled: Boolean(id),
    staleTime: 0,
    queryFn: ({ signal }) =>
      batchRequest(getNexusMigrationBatch({ path: { batchId: id }, signal })),
    refetchInterval: (query) =>
      query.state.data && batchActive(query.state.data) ? 2_000 : false,
    refetchOnWindowFocus: "always",
  });
}
export function useBatch(id: string) {
  return useQuery(batchOptions(id));
}
export function useBatchPages() {
  return useInfiniteQuery({
    queryKey: batchKeys.list,
    initialPageParam: "",
    staleTime: 0,
    queryFn: ({ signal, pageParam }) =>
      batchRequest(
        listNexusMigrationBatches({
          query: { limit: 50, cursor: pageParam || undefined },
          signal,
        }),
      ),
    getNextPageParam: (page, _pages, _last, cursors) =>
      page.nextCursor && !cursors.includes(page.nextCursor)
        ? page.nextCursor
        : undefined,
    refetchInterval: 5_000,
    refetchOnWindowFocus: "always",
  });
}
export function useEntryPages(id: string, version: string, active: boolean) {
  const queryClient = useQueryClient();
  const query = useInfiniteQuery({
    queryKey: batchKeys.entries(id),
    enabled: Boolean(id),
    staleTime: 0,
    initialPageParam: "",
    // Byte and publication checkpoints do not change the batch's CAS version.
    refetchInterval: active ? 2_000 : false,
    refetchOnWindowFocus: "always",
    queryFn: ({ signal, pageParam }) =>
      batchRequest(
        listNexusMigrationEntries({
          path: { batchId: id },
          query: { limit: 100, cursor: pageParam || undefined },
          signal,
        }),
      ),
    getNextPageParam: (page, _pages, _last, cursors) =>
      page.nextCursor && !cursors.includes(page.nextCursor)
        ? page.nextCursor
        : undefined,
  });
  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: batchKeys.entries(id) });
  }, [id, version, queryClient]);
  return query;
}
export function useBatchTargets(ids: string[]) {
  return useQueries({
    queries: [...new Set(ids)].map((id) => ({
      queryKey: [...batchKeys.all, "target", id],
      staleTime: 0,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        batchRequest(getRepository({ path: { repositoryId: id }, signal })),
    })),
  });
}

/** Commands are deliberate POSTs. A failed identical attempt keeps its key until this form closes. */
export function useBatchCommand() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const pending = useRef(false);
  const alive = useRef(true);
  const request = useRef({ signature: "", key: "" });
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const run = async <T>(
    signature: unknown,
    send: (key: string) => Promise<T>,
    accept: (value: T) => Promise<void> | void,
    reconcile?: () => Promise<void>,
  ) => {
    if (pending.current) return;
    const json = JSON.stringify(signature);
    if (json !== request.current.signature)
      request.current = { signature: json, key: crypto.randomUUID() };
    pending.current = true;
    setBusy(true);
    setError(null);
    try {
      const value = await send(request.current.key);
      if (alive.current) await accept(value);
    } catch (failure) {
      if (alive.current) {
        const error = batchError(failure, true);
        setError(error);
        if (
          reconcile &&
          (error.status === 412 || error.key === "nexusBatch.unknownOutcome")
        ) {
          // Reconciliation is read-only. A failed GET preserves the original
          // write error and the query's own error; it never replays the POST.
          try {
            await reconcile();
          } catch {
            /* Query also reports its read failure. */
          }
        }
      }
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  };
  return { busy, error, run };
}
