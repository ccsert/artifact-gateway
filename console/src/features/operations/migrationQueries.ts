import {
  queryOptions,
  useInfiniteQuery,
  useQuery,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { getMigrationSource, listMigrationSources } from "../../client";
import { migrationError, migrationRequest } from "./migrationModel";

export const migrationKeys = {
  all: ["migration-sources"] as const,
  list: () => [...migrationKeys.all, "list"] as const,
  detail: (id: string) => [...migrationKeys.all, "detail", id] as const,
};

export function useMigrationSources() {
  return useInfiniteQuery({
    queryKey: migrationKeys.list(),
    initialPageParam: "",
    queryFn: ({ signal, pageParam }) =>
      migrationRequest(
        listMigrationSources({
          query: { limit: 100, cursor: pageParam || undefined },
          signal,
        }),
      ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    staleTime: 0,
  });
}

export function migrationSourceOptions(id: string) {
  return queryOptions({
    queryKey: migrationKeys.detail(id),
    queryFn: ({ signal }) =>
      migrationRequest(getMigrationSource({ path: { sourceId: id }, signal })),
    enabled: Boolean(id),
    staleTime: 0,
  });
}

export function useMigrationSource(id: string) {
  return useQuery(migrationSourceOptions(id));
}

/** Read-only POST observations keep old data on failure and abort stale work. */
export function useMigrationRead<T>() {
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
      const result = await read(controller.signal);
      if (id === request.current.id) {
        setData(result);
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
