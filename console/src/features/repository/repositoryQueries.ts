import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
  listRepositories,
  listRepositoryCapacities,
} from "../../client";
import { loadFormatProfiles } from "../../lib/formatProfiles";
import { unwrap } from "../../lib/query/client";
import { formatKeys, repositoryKeys } from "../../lib/query/keys";

const PAGE_SIZE = 100;

/** The repository catalog, one cursor page at a time. */
export function useRepositoryPages() {
  return useInfiniteQuery({
    queryKey: repositoryKeys.list(),
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        listRepositories({
          query: { pageSize: PAGE_SIZE, pageToken: pageParam },
          signal,
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
  });
}

/** Capacity per repository; administrators only. */
export function useRepositoryCapacities(enabled: boolean) {
  return useQuery({
    queryKey: repositoryKeys.capacities(),
    queryFn: ({ signal }) => unwrap(listRepositoryCapacities({ signal })),
    enabled,
  });
}

export function useFormatProfiles(enabled: boolean) {
  return useQuery({
    queryKey: formatKeys.profiles,
    queryFn: loadFormatProfiles,
    enabled,
    staleTime: Infinity,
  });
}

export function useRepository(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.detail(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getRepository({ path: { repositoryId }, signal })),
  });
}

/**
 * The detail page's supporting reads. They wait for the repository itself, as
 * the page always has, so a missing repository is reported once.
 */
export function useRepositorySupport(repositoryId: string, enabled: boolean) {
  const path = { repositoryId };
  const capabilities = useQuery({
    queryKey: repositoryKeys.capabilities(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getRepositoryCapabilities({ path, signal })),
    enabled,
  });
  const effectiveAccess = useQuery({
    queryKey: repositoryKeys.effectiveAccess(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getRepositoryEffectiveAccess({ path, signal })),
    enabled,
  });
  const capacity = useQuery({
    queryKey: repositoryKeys.capacity(repositoryId),
    queryFn: ({ signal }) => unwrap(getRepositoryCapacity({ path, signal })),
    enabled,
  });
  return { capabilities, effectiveAccess, capacity };
}

/** Re-read everything about the catalog after a create, update or delete. */
export function useInvalidateRepositories() {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: repositoryKeys.all }),
    [queryClient],
  );
}
