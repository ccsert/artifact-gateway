import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import type { Repository } from "../../client";
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
  assetFilter,
  artifactTarget,
  buildTarget,
  unavailableMessage,
}: {
  repo: Repository;
  proxyMaven: boolean;
  query: string;
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
      assetFilter: proxyMaven ? assetFilter : undefined,
      artifactTarget,
      buildTarget,
    },
  ] as const;
  const pages = useInfiniteQuery({
    queryKey,
    queryFn: async ({
      pageParam,
    }): Promise<ArtifactPage & { totalEstimate?: number }> => {
      const page = proxyMaven
        ? await listProxyMavenPage(repo.id, query, assetFilter, pageParam)
        : await listArtifactPage(repo.format, {
            repositoryId: repo.id,
            query,
            pageToken: pageParam,
            pageSize: PAGE_SIZE,
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
  return {
    ...pages,
    /** Start the current search again from its first page. */
    reload: () => queryClient.resetQueries({ queryKey, exact: true }),
  };
}
