import {
  queryOptions,
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { getMigrationSource, listMigrationSources } from "../../client";
import { migrationError, migrationRequest } from "./migrationModel";
import { migrationReadSnapshot, useMigrationAccess } from "./migrationAccess";
import type { QueryClient } from "@tanstack/react-query";

export const migrationKeys = {
  all: ["migration-sources"] as const,
  list: () => [...migrationKeys.all, "list"] as const,
  detail: (id: string) => [...migrationKeys.all, "detail", id] as const,
};

export function useMigrationSources() {
  const client = useQueryClient();
  const access = useMigrationAccess();
  const query = useInfiniteQuery({
    queryKey: migrationKeys.list(),
    enabled: !access.error,
    initialPageParam: "",
    queryFn: ({ signal, pageParam }) =>
      migrationReadSnapshot(client, () =>
        migrationRequest(
          listMigrationSources({
            query: { limit: 100, cursor: pageParam || undefined },
            signal,
          }),
        ),
      ),
    getNextPageParam: (page) => page.value.nextCursor || undefined,
    staleTime: 0,
  });
  const data =
    !access.error &&
    query.data?.pages.every((page) => page.epoch === access.epoch)
      ? { ...query.data, pages: query.data.pages.map((page) => page.value) }
      : undefined;
  return { ...query, data };
}

export function migrationSourceOptions(id: string, client: QueryClient) {
  return queryOptions({
    queryKey: migrationKeys.detail(id),
    queryFn: ({ signal }) =>
      migrationReadSnapshot(client, () =>
        migrationRequest(
          getMigrationSource({ path: { sourceId: id }, signal }),
        ),
      ),
    enabled: Boolean(id),
    staleTime: 0,
  });
}

export function useMigrationSource(id: string) {
  const client = useQueryClient();
  const access = useMigrationAccess();
  const query = useQuery({
    ...migrationSourceOptions(id, client),
    enabled: Boolean(id) && !access.error,
  });
  return {
    ...query,
    data:
      !access.error && query.data?.epoch === access.epoch
        ? query.data.value
        : undefined,
  };
}

/** Read-only POST observations keep old data on failure and abort stale work. */
export function useMigrationRead<T>() {
  const client = useQueryClient();
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const request = useRef<{ id: number; controller?: AbortController }>({
    id: 0,
  });
  useEffect(
    () => () => {
      request.current.id += 1;
      request.current.controller?.abort();
    },
    [],
  );
  const run = async (read: (signal: AbortSignal) => Promise<T>) => {
    request.current.controller?.abort();
    const controller = new AbortController();
    const id = ++request.current.id;
    request.current.controller = controller;
    setBusy(true);
    setError(null);
    try {
      const result = await migrationReadSnapshot(client, () =>
        read(controller.signal),
      );
      if (id === request.current.id) {
        setData(result.value);
        setRevision((current) => current + 1);
      }
    } catch (failure) {
      if (id === request.current.id && !controller.signal.aborted) {
        const error = migrationError(failure);
        // Permission/session refusals must not leave a prior observation open.
        if (error.status === 401 || error.status === 403) setData(null);
        setError(error);
      }
    } finally {
      if (id === request.current.id) setBusy(false);
    }
  };
  const reset = () => {
    request.current.id += 1;
    request.current.controller?.abort();
    setData(null);
    setError(null);
    setBusy(false);
  };
  return { data, error, busy, revision, run, reset };
}
