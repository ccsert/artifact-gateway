import type { ArtifactSearchMatch } from "../client";
import { searchRepositoryArtifacts } from "../client";
import type { ArtifactRow } from "../features/repository/artifactRow";
import { artifactSearchPrefix } from "./index";

export interface ArtifactPageRequest {
  repositoryId: string;
  /** The search box text, in its readable form. */
  query: string;
  pageToken?: string;
  pageSize: number;
  match?: ArtifactSearchMatch;
  signal?: AbortSignal;
  /** A deep-linked artifact the first page should contain. */
  artifactTarget?: string;
  buildTarget?: number;
}

/** One page of a repository's artifact list. Errors are returned, not thrown. */
export interface ArtifactPage {
  items: ArtifactRow[];
  nextPageToken?: string;
  error: unknown;
}

export type ListArtifacts = (
  request: ArtifactPageRequest,
) => Promise<ArtifactPage>;

/** Package formats: one row per package, with its version count and newest version. */
export const listPackageArtifacts: ListArtifacts = async ({
  repositoryId,
  query,
  pageToken,
  pageSize,
  match,
  signal,
}) => {
  const response = await searchRepositoryArtifacts({
    path: { repositoryId },
    query: {
      q: query || undefined,
      pageSize,
      pageToken,
      ...(query && match ? { match } : {}),
    },
    signal,
  });
  return {
    items: (response.data?.items ?? []).map((item) => ({
      key: item.coordinate,
      coordinate: item.coordinate,
      digest: item.digest,
      createdAt: item.createdAt,
      size: item.size,
      publisher: item.publisher,
      intelligence: item.intelligence,
      versionCount: item.versionCount,
      latestVersion: item.version,
    })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
};

const pathListers = new Map<string, ListArtifacts>();

/**
 * Path formats: one row per stored file, searched by path prefix. One lister
 * per format, so its identity is stable for hooks that depend on it.
 */
export function pathArtifactLister(format: string): ListArtifacts {
  const cached = pathListers.get(format);
  if (cached) return cached;
  const lister: ListArtifacts = async ({
    repositoryId,
    query,
    pageToken,
    pageSize,
    match,
    signal,
  }) => {
    const response = await searchRepositoryArtifacts({
      path: { repositoryId },
      query: {
        q: artifactSearchPrefix(format, query) || undefined,
        pageSize,
        pageToken,
        ...(query && match ? { match } : {}),
      },
      signal,
    });
    return {
      items: (response.data?.items ?? []).map((item, index) => ({
        key: `${item.coordinate}-${index}`,
        coordinate: item.coordinate,
        digest: item.digest,
        createdAt: item.createdAt,
        size: item.size,
        contentType: item.contentType,
        cachedAt: item.cachedAt,
        sourceUrl: item.sourceUrl,
        intelligence: item.intelligence,
      })),
      nextPageToken: response.data?.nextPageToken,
      error: response.error,
    };
  };
  pathListers.set(format, lister);
  return lister;
}
