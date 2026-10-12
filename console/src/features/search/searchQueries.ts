import { useInfiniteQuery } from "@tanstack/react-query";
import { searchArtifacts } from "../../client";
import type { ArtifactSearchMatch } from "../../client";
import { unwrap } from "../../lib/query/client";
import { searchKeys } from "../../lib/query/keys";
import { SEARCH_PAGE_SIZE } from "./searchModel";

/**
 * Global artifact search, one page at a time. Keyed by match mode and query,
 * so a new search starts empty while a retry keeps its rows; an empty query
 * reads nothing.
 */
export function useArtifactSearch(
  q: string,
  match: ArtifactSearchMatch,
  /** Shown when the Gateway answers without a page. */
  emptyMessage: string,
) {
  return useInfiniteQuery({
    queryKey: searchKeys.artifacts(match, q),
    queryFn: async ({ pageParam, signal }) => {
      const page = await unwrap(
        searchArtifacts({
          query: { q, match, pageSize: SEARCH_PAGE_SIZE, pageToken: pageParam },
          signal,
        }),
      );
      if (!page) throw new Error(emptyMessage);
      return page;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    enabled: Boolean(q),
    staleTime: 0,
  });
}
