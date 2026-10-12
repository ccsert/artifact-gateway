import { listMavenCoordinates, searchRepositoryArtifacts } from "../../client";
import type { ArtifactSearchMatch } from "../../client";
import type { ArtifactRow } from "../../features/repository/artifactRow";
import { mavenGA, mavenVersion } from "../../lib/usage";
import type { ArtifactPage, ListArtifacts } from "../artifactListing";

async function searchMavenPage(
  repositoryId: string,
  query: string,
  pageSize: number,
  pageToken?: string,
  match?: ArtifactSearchMatch,
  signal?: AbortSignal,
): Promise<ArtifactPage> {
  const response = await searchRepositoryArtifacts({
    path: { repositoryId },
    query: { q: query, pageSize, pageToken, ...(match ? { match } : {}) },
    signal,
  });
  return {
    items: (response.data?.items ?? []).map((item) => ({
      key: `${item.coordinate}-${item.buildNumber ?? 0}`,
      coordinate: item.coordinate,
      digest: item.digest,
      createdAt: item.createdAt,
      size: item.size,
      contentType: item.contentType,
      publisher: item.publisher,
      buildNumber: item.buildNumber,
      intelligence: item.intelligence,
    })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
}

/**
 * Hosted Maven. A search lists individual builds and, for a deep link to one
 * build, pages until that build is found and shows it alone. Without a
 * search, coordinates are grouped into one row per group:artifact with the
 * version count and newest version.
 */
export const listMavenArtifacts: ListArtifacts = async ({
  repositoryId,
  query,
  pageToken,
  pageSize,
  artifactTarget,
  buildTarget,
  match,
  signal,
}) => {
  if (query) {
    const first = await searchMavenPage(
      repositoryId,
      query,
      pageSize,
      pageToken,
      match,
      signal,
    );
    if (pageToken || !artifactTarget || !buildTarget || first.error) {
      return first;
    }
    const isTarget = (item: ArtifactRow) =>
      item.coordinate === artifactTarget && item.buildNumber === buildTarget;
    let target = first.items.find(isTarget);
    let next = first.nextPageToken;
    while (!target && next) {
      const page = await searchMavenPage(
        repositoryId,
        query,
        pageSize,
        next,
        match,
        signal,
      );
      if (page.error) {
        return { items: first.items, nextPageToken: next, error: page.error };
      }
      target = page.items.find(isTarget);
      next = page.nextPageToken;
    }
    // The build was found: show it alone. Otherwise keep the first page,
    // with nothing more to load.
    return target
      ? { items: [target], nextPageToken: undefined, error: null }
      : { items: first.items, nextPageToken: next, error: null };
  }

  const response = await listMavenCoordinates({
    path: { repositoryId },
    query: { pageSize, pageToken },
    signal,
  });
  const byGA = new Map<
    string,
    {
      coordinate: string;
      digest?: string;
      createdAt?: string;
      publisher?: string;
    }[]
  >();
  for (const item of response.data?.items ?? []) {
    const ga = mavenGA(item.coordinate) ?? item.coordinate;
    if (!byGA.has(ga)) byGA.set(ga, []);
    byGA.get(ga)!.push(item);
  }
  return {
    items: Array.from(byGA.entries()).map(([ga, versions]) => {
      const latest = [...versions].sort((a, b) =>
        (b.createdAt ?? "").localeCompare(a.createdAt ?? ""),
      )[0];
      return {
        key: ga,
        coordinate: ga,
        digest: latest?.digest,
        createdAt: latest?.createdAt,
        publisher: latest?.publisher,
        versionCount: versions.length,
        latestVersion: mavenVersion(latest?.coordinate ?? "") ?? undefined,
      };
    }),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
};
