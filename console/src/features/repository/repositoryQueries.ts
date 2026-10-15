import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  getAptLifecycleState,
  getAptRepositorySigningState,
  getProxyHealth,
  getQuarantineReadPolicy,
  getSecurityPolicy,
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
  getRetentionPolicy,
  listRepositories,
  listRepositoryLifecycleJobs,
  listRepositoryTombstones,
  getRepositoryReplication,
  listRepositoryReplications,
  listRepositoryCapacities,
  listRepositoryArtifactUsage,
} from "../../client";
import type { QuarantineReadPolicy, SecurityPolicy } from "../../client";
import { isNotFound } from "../../components/ui/Feedback";
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

/** One repository's capacity and quota; shared with the support probe. */
export function useRepositoryCapacity(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.capacity(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getRepositoryCapacity({ path: { repositoryId }, signal })),
    staleTime: 0,
  });
}

/** One page of a repository's download statistics. */
export function useArtifactUsage(
  repositoryId: string,
  page: number,
  pageSize: number,
  q: string,
) {
  return useQuery({
    queryKey: [
      ...repositoryKeys.detail(repositoryId),
      "usage",
      { page, pageSize, q },
    ] as const,
    queryFn: ({ signal }) =>
      unwrap(
        listRepositoryArtifactUsage({
          path: { repositoryId },
          query: {
            limit: pageSize,
            offset: (page - 1) * pageSize,
            q: q || undefined,
          },
          signal,
        }),
      ),
    // Keep the previous page on screen while the next one loads; the tab is
    // mounted per repository, so it never shows another repository's rows.
    placeholderData: (previous) => previous,
    staleTime: 0,
  });
}

/** Re-read everything about the catalog after a create, update or delete. */
export function useInvalidateRepositories() {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: repositoryKeys.all }),
    [queryClient],
  );
}

/** A repository's retention policy. Its editors invalidate it after a save. */
export function useRetentionPolicy(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.retentionPolicy(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getRetentionPolicy({ path: { repositoryId }, signal })),
  });
}

export function useInvalidateRetentionPolicy(repositoryId: string) {
  const queryClient = useQueryClient();
  return useCallback(
    () =>
      queryClient.invalidateQueries({
        queryKey: repositoryKeys.retentionPolicy(repositoryId),
      }),
    [queryClient, repositoryId],
  );
}

/**
 * One APT suite's packages and signed snapshots. Snapshots change when the
 * publication API signs a new one, so the state is always re-read.
 */
export function useAptLifecycleState(
  repositoryId: string,
  suite: string,
  /** Shown when the Gateway answers without a state. */
  emptyMessage: string,
) {
  return useQuery({
    queryKey: repositoryKeys.aptLifecycle(repositoryId, suite),
    queryFn: async ({ signal }) => {
      const state = await unwrap(
        getAptLifecycleState({
          path: { repositoryId },
          query: { suite },
          signal,
        }),
      );
      if (!state) throw new Error(emptyMessage);
      return state;
    },
    staleTime: 0,
  });
}

/**
 * Replication plans sourced from a repository. A repository without the
 * feature answers 404, which means no plans rather than a failure. Plans
 * progress in workers, so the list is always re-read.
 */
export function useReplicationPlans(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.replicationPlans(repositoryId),
    queryFn: async ({ signal }) => {
      try {
        return await unwrap(
          listRepositoryReplications({ path: { repositoryId }, signal }),
        );
      } catch (error) {
        if (isNotFound(error)) return [];
        throw error;
      }
    },
    staleTime: 0,
  });
}

/**
 * One replication plan with its checkpoints, read when its detail opens.
 * It sits under the plan list, so a cancel that refreshes plans covers it.
 */
export function useReplicationPlan(
  repositoryId: string,
  planId: string | null,
) {
  return useQuery({
    queryKey: repositoryKeys.replicationPlan(repositoryId, planId ?? ""),
    queryFn: ({ signal }) =>
      unwrap(
        getRepositoryReplication({
          path: { repositoryId, replicationPlanId: planId ?? "" },
          signal,
        }),
      ),
    enabled: Boolean(planId),
    staleTime: 0,
  });
}

/** Promotion admission rules. The admission card writes saves back here. */
export function useSecurityPolicy(repositoryId: string, enabled: boolean) {
  return useQuery({
    queryKey: repositoryKeys.securityPolicy(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getSecurityPolicy({ path: { repositoryId }, signal })),
    enabled,
  });
}

export function useSetSecurityPolicy(repositoryId: string) {
  const queryClient = useQueryClient();
  return useCallback(
    (policy: SecurityPolicy) =>
      queryClient.setQueryData(
        repositoryKeys.securityPolicy(repositoryId),
        policy,
      ),
    [queryClient, repositoryId],
  );
}

/** Whether quarantined artifacts can be read. Saves are written back here. */
export function useQuarantineReadPolicy(
  repositoryId: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: repositoryKeys.quarantineReadPolicy(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getQuarantineReadPolicy({ path: { repositoryId }, signal })),
    enabled,
  });
}

export function useSetQuarantineReadPolicy(repositoryId: string) {
  const queryClient = useQueryClient();
  return useCallback(
    (policy: QuarantineReadPolicy) =>
      queryClient.setQueryData(
        repositoryKeys.quarantineReadPolicy(repositoryId),
        policy,
      ),
    [queryClient, repositoryId],
  );
}

/** A hosted APT repository's signing keys and signer health. */
export function useAptSigningState(repositoryId: string, enabled: boolean) {
  return useQuery({
    queryKey: repositoryKeys.aptSigningState(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getAptRepositorySigningState({ path: { repositoryId }, signal })),
    enabled,
    staleTime: 0,
  });
}

/** A proxy's upstream health; it changes as the upstream answers, so re-read. */
export function useProxyHealth(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.proxyHealth(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(getProxyHealth({ path: { repositoryId }, signal })),
    staleTime: 0,
  });
}

export function useInvalidateProxyHealth(repositoryId: string) {
  const queryClient = useQueryClient();
  return useCallback(
    () =>
      queryClient.invalidateQueries({
        queryKey: repositoryKeys.proxyHealth(repositoryId),
      }),
    [queryClient, repositoryId],
  );
}

/** The repository's scan jobs, polled every 10 seconds while shown. */
export function useScanJobs(repositoryId: string, enabled: boolean) {
  return useQuery({
    queryKey: repositoryKeys.scanJobs(repositoryId),
    queryFn: async ({ signal }) =>
      (
        await unwrap(
          listRepositoryLifecycleJobs({ path: { repositoryId }, signal }),
        )
      ).filter((job) => job.kind === "scan"),
    enabled,
    refetchInterval: 10_000,
    staleTime: 0,
  });
}

/** Every lifecycle job of a repository, polled every 10 seconds while shown. */
export function useLifecycleJobs(repositoryId: string) {
  return useQuery({
    queryKey: repositoryKeys.lifecycleJobs(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(listRepositoryLifecycleJobs({ path: { repositoryId }, signal })),
    refetchInterval: 10_000,
    staleTime: 0,
  });
}

/** Deleted artifacts, one cursor page at a time. */
export function useTombstonePages(repositoryId: string) {
  const queryClient = useQueryClient();
  const queryKey = repositoryKeys.tombstones(repositoryId);
  const pages = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        listRepositoryTombstones({
          path: { repositoryId },
          query: { pageSize: 50, pageToken: pageParam },
          signal,
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    staleTime: 0,
  });
  return {
    ...pages,
    /** Start again from the first page, after a restore. */
    reload: () => queryClient.resetQueries({ queryKey, exact: true }),
  };
}
