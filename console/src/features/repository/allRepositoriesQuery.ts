import { useInfiniteQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { listRepositories } from "../../client";
import type { Repository } from "../../client";
import { unwrap } from "../../lib/query/client";
import { repositoryKeys } from "../../lib/query/keys";

const PAGE_SIZE = 100;

/**
 * Every repository, read page by page while `enabled` (the command palette
 * reads them all once and relies on catalog invalidation afterwards). A
 * cursor that does not advance stops the read and reports it.
 */
export function useAllRepositories(enabled: boolean) {
  const query = useInfiniteQuery({
    queryKey: [...repositoryKeys.list(), "all"],
    queryFn: async ({ pageParam, signal }) => {
      const page = await unwrap(
        listRepositories({
          query: { pageSize: PAGE_SIZE, pageToken: pageParam },
          signal,
        }),
      );
      if (!page) throw new Error("Repository response is missing");
      return page;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page, _pages, _param, params) =>
      page.nextPageToken && !params.includes(page.nextPageToken)
        ? page.nextPageToken
        : undefined,
    enabled,
    staleTime: Infinity,
  });
  const { data, hasNextPage, isFetching, isError, fetchNextPage } = query;
  useEffect(() => {
    if (enabled && hasNextPage && !isFetching && !isError)
      void fetchNextPage({ cancelRefetch: false });
  }, [enabled, hasNextPage, isFetching, isError, fetchNextPage]);
  const repositories = useMemo(() => {
    const byId = new Map<string, Repository>();
    for (const page of data?.pages ?? [])
      for (const repository of page.items) byId.set(repository.id, repository);
    return data ? [...byId.values()] : null;
  }, [data]);
  const lastPage = data?.pages.at(-1);
  const stalled = Boolean(lastPage?.nextPageToken) && !hasNextPage;
  return {
    repositories,
    loading: isFetching,
    error:
      query.error ??
      (stalled ? new Error("Repository pagination did not advance") : null),
    // A failed continuation resumes that page. A failed catalog refresh must
    // reread the cached pages; fetchNextPage can succeed without reading any.
    retry: () =>
      query.isFetchNextPageError ? fetchNextPage() : query.refetch(),
  };
}
