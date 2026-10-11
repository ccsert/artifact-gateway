import { useQuery } from "@tanstack/react-query";
import { searchRepositoryArtifacts } from "../../client";
import type { ArtifactSummary } from "../../client";
import { missingDeepLinkedArtifact } from "../../lib/publicBrowseModel";
import { unwrap } from "../../lib/query/client";
import { publicKeys } from "../../lib/query/keys";
import { canonicalRawSearchPrefix } from "../../lib/rawPath";
import type { PublicRepository } from "./types";

interface PublicRepositoryCatalogResponse {
  enabled?: boolean;
  items?: PublicRepository[];
}

/** Anonymous reads report a refusal on the page and never touch the session. */
const publicMeta = { authFailure: "ignore" } as const;

/** Retry stays pending until every source settles, even if one fails first. */
export async function refetchPublicSources(
  requests: readonly Promise<unknown>[],
) {
  const results = await Promise.allSettled(requests);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
}

function publicArtifactAnswer<T extends { items: ArtifactSummary[] }>(data: T) {
  if (!data || !Array.isArray(data.items)) {
    throw new Error("Invalid public artifact search response");
  }
  return data;
}

/** The anonymous catalog; not part of the management SDK. */
export function usePublicCatalog() {
  return useQuery({
    queryKey: publicKeys.catalog(),
    queryFn: async ({ signal }) => {
      const response = await fetch("/api/v2/public/repositories", { signal });
      if (!response.ok)
        throw Object.assign(
          new Error(`读取公开仓库失败 (${response.status})`),
          { status: response.status },
        );
      const data = (await response.json()) as PublicRepositoryCatalogResponse;
      const repositories = data.items ?? [];
      if (
        !Array.isArray(repositories) ||
        repositories.some(
          (repository) =>
            !repository ||
            typeof repository.id !== "string" ||
            typeof repository.name !== "string" ||
            typeof repository.format !== "string",
        )
      ) {
        throw new Error("Invalid public repository catalog response");
      }
      return {
        anonymousEnabled: data.enabled !== false,
        repositories,
      };
    },
    meta: publicMeta,
  });
}

/**
 * Artifacts of the selected public repository for the current query. A deep
 * link to an artifact outside the first page is looked up by its exact
 * coordinate and merged in, so the link always opens what it names.
 */
export function usePublicArtifacts(
  repository: PublicRepository | undefined,
  query: string,
  artifactParam: string,
) {
  const repositoryId = repository?.id ?? "";
  const search = useQuery({
    queryKey: publicKeys.artifacts(repositoryId, query),
    queryFn: ({ signal }) =>
      unwrap(
        searchRepositoryArtifacts({
          path: { repositoryId },
          query: {
            q:
              (repository?.format === "raw"
                ? canonicalRawSearchPrefix(query)
                : query) || undefined,
            pageSize: 100,
          },
          signal,
        }),
      ).then(publicArtifactAnswer),
    enabled: Boolean(repository),
    meta: publicMeta,
  });
  const found = search.data?.items;
  const missing = found?.every(
    (item) => item && typeof item.coordinate === "string",
  )
    ? missingDeepLinkedArtifact(found, artifactParam)
    : null;
  const lookup = useQuery({
    queryKey: publicKeys.artifactLookup(repositoryId, missing ?? ""),
    queryFn: ({ signal }) =>
      unwrap(
        searchRepositoryArtifacts({
          path: { repositoryId },
          query: { q: missing ?? "", pageSize: 100 },
          signal,
        }),
      ).then(publicArtifactAnswer),
    enabled: Boolean(repository && missing),
    meta: publicMeta,
  });

  let items: ArtifactSummary[] | null = found ?? null;
  if (items && missing && lookup.data) {
    const current = items;
    const matches = (lookup.data.items ?? []).filter(
      (item) => item?.coordinate === missing,
    );
    items = [
      ...current,
      ...matches.filter(
        (match) =>
          !current.some(
            (item) =>
              item.coordinate === match.coordinate &&
              item.buildNumber === match.buildNumber,
          ),
      ),
    ];
  }
  return {
    items,
    error: search.error ?? lookup.error ?? null,
    recoveryKeys: [search.data, lookup.data],
    refetch: () =>
      refetchPublicSources([
        ...(repository ? [search.refetch({ throwOnError: true })] : []),
        ...(repository && missing
          ? [lookup.refetch({ throwOnError: true })]
          : []),
      ]),
  };
}
