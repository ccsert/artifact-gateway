import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useQuery,
} from "@tanstack/react-query";
import { useEffect } from "react";
import type { Dayjs } from "dayjs";
import {
  getAuditRetentionPolicy,
  listAuditPage,
  listAuditRetentionJobs,
} from "../../client";
import { unwrap } from "../../lib/query/client";
import { auditKeys } from "../../lib/query/keys";
import { useGroupPages } from "../groups/groupQueries";

export type AuditFilters = {
  repository: string;
  group: string;
  outcome: string;
  format: string;
  operation: string;
  actor: string;
  limit: number;
  dateRange: [Dayjs | null, Dayjs | null];
};

export const emptyAuditFilters: AuditFilters = {
  repository: "",
  group: "",
  outcome: "",
  format: "",
  operation: "",
  actor: "",
  limit: 100,
  dateRange: [null, null],
};

/** The query each filter set sends; also the cache key for its pages. */
export function auditQuery(filters: AuditFilters) {
  return {
    repository: filters.repository || undefined,
    group: filters.group || undefined,
    outcome: filters.outcome || undefined,
    format: filters.format || undefined,
    operation: filters.operation || undefined,
    actor: filters.actor || undefined,
    from: filters.dateRange[0]?.toISOString(),
    to: filters.dateRange[1]?.toISOString(),
    pageSize: filters.limit,
  };
}

/**
 * Audit records for one filter set, newest first, one cursor page at a time.
 * Pages already read stay cached, so going back does not ask again.
 */
export function auditPagesOptions(filters: AuditFilters) {
  const query = auditQuery(filters);
  return infiniteQueryOptions({
    queryKey: auditKeys.pages(query),
    queryFn: async ({ pageParam, signal }) => {
      const page = await unwrap(
        listAuditPage({ query: { ...query, pageToken: pageParam }, signal }),
      );
      return { items: page?.items ?? [], nextPageToken: page?.nextPageToken };
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    // Audit records keep arriving; every visit and refresh reads them again.
    staleTime: 0,
    // A new filter set keeps the previous records on screen until it answers.
    placeholderData: (previous) => previous,
  });
}

export function useAuditPages(filters: AuditFilters) {
  return useInfiniteQuery(auditPagesOptions(filters));
}

/** Keep the original 200-group filter window, including later cursor pages. */
export function useAuditGroupOptions() {
  const query = useGroupPages();
  const groups = query.data?.pages.flatMap((page) => page.items) ?? [];
  const { fetchNextPage, hasNextPage, isFetching, isError } = query;
  useEffect(() => {
    if (groups.length < 200 && hasNextPage && !isFetching && !isError) {
      void fetchNextPage({ cancelRefetch: false });
    }
  }, [groups.length, hasNextPage, isFetching, isError, fetchNextPage]);
  return groups.slice(0, 200).map((group) => group.name);
}

export function useAuditRetentionPolicy() {
  return useQuery({
    queryKey: auditKeys.retentionPolicy(),
    queryFn: ({ signal }) => unwrap(getAuditRetentionPolicy({ signal })),
  });
}

const activeCleanupStates = new Set(["pending", "running"]);

/** Cleanup jobs, polled while one is pending or running. */
export function useAuditRetentionJobs() {
  return useQuery({
    queryKey: auditKeys.retentionJobs(),
    queryFn: async ({ signal }) =>
      (await unwrap(listAuditRetentionJobs({ signal }))) ?? [],
    staleTime: 0,
    refetchInterval: (query) =>
      query.state.data?.some((job) => activeCleanupStates.has(job.state))
        ? 2000
        : false,
  });
}
