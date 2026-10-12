import {
  useInfiniteQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { useState } from "react";
import type { ArtifactSearchMatch, Repository } from "../../client";
import { listArtifactPage, type ArtifactPage } from "../../formats/listers";
import {
  listProxyMavenPage,
  PROXY_CACHE_UNAVAILABLE,
} from "../../formats/maven/proxyCache";
import { repositoryKeys } from "../../lib/query/keys";
import type { ProxyMavenAssetFilter } from "./ProxyMavenCacheSection";

const PAGE_SIZE = 50;

/**
 * A repository's artifact list, one page at a time: the format's listing,
 * or a Maven proxy's cache. Keyed by everything that changes the answer, so
 * a slower answer for a previous search can never replace the current one.
 * Artifacts change on the server (publishes, pulls), so the list is always
 * re-read when the tab opens.
 */
export function useArtifactPages({
  repo,
  proxyMaven,
  query,
  match,
  assetFilter,
  artifactTarget,
  buildTarget,
  unavailableMessage,
}: {
  repo: Repository;
  proxyMaven: boolean;
  query: string;
  /** Prefix or literal contains, for formats with server keyword search. */
  match: ArtifactSearchMatch;
  assetFilter: ProxyMavenAssetFilter | "all";
  artifactTarget?: string;
  buildTarget?: number;
  /** Shown when the proxy cache answers without a page. */
  unavailableMessage: string;
}) {
  const queryClient = useQueryClient();
  const queryKey = [
    ...repositoryKeys.detail(repo.id),
    "artifacts",
    {
      format: repo.format,
      proxyMaven,
      query,
      match: proxyMaven ? undefined : match,
      assetFilter: proxyMaven ? assetFilter : undefined,
      artifactTarget,
      buildTarget,
    },
  ] as const;
  const pages = useInfiniteQuery({
    queryKey,
    queryFn: async ({
      pageParam,
      signal,
    }): Promise<ArtifactPage & { totalEstimate?: number }> => {
      const page = proxyMaven
        ? await listProxyMavenPage(
            repo.id,
            query,
            assetFilter,
            pageParam,
            signal,
          )
        : await listArtifactPage(repo.format, {
            repositoryId: repo.id,
            query,
            pageToken: pageParam,
            pageSize: PAGE_SIZE,
            match,
            signal,
            artifactTarget,
            buildTarget,
          });
      if (page.error === PROXY_CACHE_UNAVAILABLE) {
        throw new Error(unavailableMessage);
      }
      if (page.error) throw page.error;
      return page;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    staleTime: 0,
  });
  const cache = queryClient.getQueryCache();
  const currentQuery = cache.find({ queryKey, exact: true });
  const currentData =
    queryClient.getQueryData<
      InfiniteData<ArtifactPage & { totalEstimate?: number }>
    >(queryKey);
  const [displayed, setDisplayed] = useState({
    repositoryId: repo.id,
    queryClient,
    query: currentQuery,
    data: currentData,
  });
  // Keep the last successful answer through failed searches and first-page
  // reloads. Binding it to a live cache entry also drops it when the identity
  // cache is cleared; another repository or query client never inherits rows.
  const sameScope =
    displayed.repositoryId === repo.id &&
    displayed.queryClient === queryClient &&
    displayed.query !== undefined &&
    cache.get(displayed.query.queryHash) === displayed.query;
  const data = currentData ?? (sameScope ? displayed.data : undefined);
  if (
    displayed.repositoryId !== repo.id ||
    displayed.queryClient !== queryClient ||
    displayed.query !== currentQuery ||
    displayed.data !== data
  ) {
    setDisplayed({
      repositoryId: repo.id,
      queryClient,
      query: currentQuery,
      data,
    });
  }
  return {
    ...pages,
    data,
    // A previous answer's cursor cannot page the newly submitted search.
    hasNextPage: currentData !== undefined && pages.hasNextPage,
    /** Start the current search again from its first page. */
    reload: () => queryClient.resetQueries({ queryKey, exact: true }),
  };
}
