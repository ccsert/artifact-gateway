import { listProxyCacheEntries } from "../../client";
import type { ArtifactRow } from "../../features/repository/artifactRow";
import {
  PROXY_MAVEN_PAGE_SIZE,
  type ProxyMavenAssetFilter,
} from "../../features/repository/ProxyMavenCacheSection";
import type { ArtifactPage } from "../artifactListing";

/** The proxy cache answered without a page; shown as its own message. */
export const PROXY_CACHE_UNAVAILABLE = Symbol("proxy-cache-unavailable");

/** One page of a Maven proxy's cache, grouped by version. */
export async function listProxyMavenPage(
  repositoryId: string,
  query: string,
  assetFilter: ProxyMavenAssetFilter | "all",
  pageToken?: string,
  signal?: AbortSignal,
): Promise<ArtifactPage & { totalEstimate?: number }> {
  try {
    const { data: pageData, error: requestError } = await listProxyCacheEntries(
      {
        path: { repositoryId },
        signal,
        query: {
          groupBy: "version",
          assetFilter,
          q: query || undefined,
          pageSize: PROXY_MAVEN_PAGE_SIZE,
          pageToken,
        },
      },
    );
    if (requestError || !pageData) {
      return { items: [], error: PROXY_CACHE_UNAVAILABLE };
    }
    return {
      items: pageData.items.map((item) => {
        const primary = item.assets?.filter((asset) => !asset.sidecar) ?? [];
        return {
          key: item.key,
          coordinate: item.coordinate,
          latestVersion: item.version,
          size: item.size,
          contentType: item.extensions?.join(", ") || item.contentType,
          publisher: item.member,
          fileCount: item.assetCount,
          primaryFiles: primary.map((asset) => asset.name),
          files: item.assets,
        } satisfies ArtifactRow;
      }),
      nextPageToken: pageData.nextPageToken,
      totalEstimate: pageData.totalEstimate,
      error: null,
    };
  } catch (error) {
    return { items: [], error };
  }
}
