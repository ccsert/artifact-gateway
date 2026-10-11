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

/** The anonymous catalog; not part of the management SDK. */
export function usePublicCatalog() {
  return useQuery({
    queryKey: publicKeys.catalog(),
    queryFn: async ({ signal }) => {
      const response = await fetch("/api/v2/public/repositories", { signal });
      if (!response.ok)
        throw new Error(`读取公开仓库失败 (${response.status})`);
      const data = (await response.json()) as PublicRepositoryCatalogResponse;
      return {
        anonymousEnabled: data.enabled !== false,
        repositories: data.items ?? [],
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
      ),
    enabled: Boolean(repository),
    meta: publicMeta,
  });
  const found = search.data?.items;
  const missing = found
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
      ),
    enabled: Boolean(repository && missing),
    meta: publicMeta,
  });

  let items: ArtifactSummary[] | null = found ?? null;
  if (items && missing && lookup.data) {
    const current = items;
    const matches = (lookup.data.items ?? []).filter(
      (item) => item.coordinate === missing,
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
  return { items, error: search.error ?? lookup.error ?? null };
}
