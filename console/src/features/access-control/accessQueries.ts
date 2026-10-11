import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  getAnonymousAccessPolicy,
  listAuthorizationRoles,
  listAuthorizationTemplates,
  listGrants,
  listRepositories,
  listRepositoryGrants,
  listServiceAccounts,
  listUsers,
  replaceAnonymousAccessPolicy,
} from "../../client";
import type {
  AnonymousAccessPolicy,
  RepositoryGrantRecord,
  RepositoryPage,
} from "../../client";
import { unwrap } from "../../lib/query/client";
import { accessKeys } from "../../lib/query/keys";
import { useApiKeys } from "../identity/apiKeyQueries";
import type { GrantRow } from "./accessModel";

/**
 * Grants and the principal directory are written on other pages (repository
 * grants, users, service accounts) that do not invalidate these keys yet, so
 * they are re-read every time the page mounts.
 */
const ALWAYS_REVALIDATE = 0;

function toGrantRows(records: RepositoryGrantRecord[]): GrantRow[] {
  return records
    .map((record) => ({
      repositoryId: record.repositoryId,
      repositoryName: record.repositoryName,
      format: record.format,
      principal: record.principal,
      scopes: record.scopes,
      resourcePrefix: record.resourcePrefix ?? "",
    }))
    .sort(
      (a, b) =>
        a.principal.localeCompare(b.principal) ||
        a.repositoryName.localeCompare(b.repositoryName),
    );
}

const grantRecordsQuery = queryOptions({
  queryKey: accessKeys.grants(),
  queryFn: ({ signal }) => unwrap(listRepositoryGrants({ signal })),
  staleTime: ALWAYS_REVALIDATE,
});

/** Every repository grant across repositories, as the API returns them. */
export function useRepositoryGrantRecords() {
  return useQuery(grantRecordsQuery);
}

/** The same grants, flattened and sorted for the access control list. */
export function useGrantRows() {
  return useQuery({ ...grantRecordsQuery, select: toGrantRows });
}

/** One repository's grant list, read fresh whenever an editor opens on it. */
export function useRepositoryGrants(repositoryId: string) {
  return useQuery({
    queryKey: accessKeys.repositoryGrants(repositoryId),
    queryFn: ({ signal }) =>
      unwrap(listGrants({ path: { repositoryId }, signal })),
    enabled: Boolean(repositoryId),
    staleTime: ALWAYS_REVALIDATE,
  });
}

/** Repositories a grant can name, in every state; callers filter. */
function activeRepositories(data: RepositoryPage): RepositoryPage {
  return {
    ...data,
    items: data.items.filter((repository) => repository.state === "active"),
  };
}

export async function refetchAccessSources(
  requests: readonly Promise<unknown>[],
) {
  const results = await Promise.allSettled(requests);
  for (const result of results)
    if (result.status === "rejected") throw result.reason;
}

export function useAccessRepositories({ activeOnly = false } = {}) {
  return useQuery({
    queryKey: accessKeys.directory("repositories"),
    queryFn: ({ signal }) =>
      unwrap(listRepositories({ query: { pageSize: 200 }, signal })),
    staleTime: ALWAYS_REVALIDATE,
    select: activeOnly ? activeRepositories : undefined,
  });
}

export function useAnonymousPolicy() {
  return useQuery({
    queryKey: accessKeys.anonymousPolicy(),
    queryFn: ({ signal }) => unwrap(getAnonymousAccessPolicy({ signal })),
    staleTime: ALWAYS_REVALIDATE,
  });
}

export function useReplaceAnonymousPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (next: AnonymousAccessPolicy) =>
      unwrap(
        replaceAnonymousAccessPolicy({
          body: next,
          headers: { "If-Match": next.version },
        }),
      ),
    onSuccess: (saved) =>
      queryClient.setQueryData(accessKeys.anonymousPolicy(), saved),
  });
}

/**
 * Everyone a grant can name: users, API keys and service accounts. Only a
 * platform administrator can list them, so other callers pass `enabled: false`
 * instead of collecting refusals that would end the session.
 */
export function usePrincipalDirectory({ enabled = true } = {}) {
  const users = useQuery({
    queryKey: accessKeys.directory("users"),
    queryFn: ({ signal }) => unwrap(listUsers({ signal })),
    staleTime: ALWAYS_REVALIDATE,
    enabled,
  });
  const apiKeys = useApiKeys({ enabled });
  const serviceAccounts = useQuery({
    queryKey: accessKeys.directory("service-accounts"),
    queryFn: ({ signal }) =>
      unwrap(listServiceAccounts({ query: { pageSize: 200 }, signal })),
    staleTime: ALWAYS_REVALIDATE,
    enabled,
  });
  return { users, apiKeys, serviceAccounts };
}

/**
 * Who and what the evaluator and templates can name. Users, API keys and
 * repositories are required; service accounts degrade to a notice.
 */
export function useAccessDirectory() {
  const { users, apiKeys, serviceAccounts } = usePrincipalDirectory();
  const repositories = useAccessRepositories({ activeOnly: true });

  const required = [users, apiKeys, repositories];
  return {
    users: users.data?.items ?? [],
    apiKeys: apiKeys.data?.items ?? [],
    serviceAccounts: serviceAccounts.data?.items ?? [],
    repositories: repositories.data?.items ?? [],
    repositoryError: repositories.error,
    recoveryKeys: [
      users.data,
      apiKeys.data,
      serviceAccounts.data,
      repositories.data,
    ],
    loading: required.some((query) => query.isPending),
    error: required.find((query) => query.error)?.error ?? null,
    serviceAccountsError: serviceAccounts.error ?? null,
    retry: () =>
      refetchAccessSources(
        [...required, serviceAccounts].map((query) =>
          query.refetch({ throwOnError: true }),
        ),
      ),
  };
}

/** Custom roles. The roles panel is their only writer and invalidates them. */
export function useAuthorizationRoles({ enabled = true } = {}) {
  return useQuery({
    queryKey: accessKeys.roles(),
    queryFn: ({ signal }) => unwrap(listAuthorizationRoles({ signal })),
    enabled,
  });
}

/** Saved templates. The templates panel is their only writer. */
export function useAuthorizationTemplates() {
  return useQuery({
    queryKey: accessKeys.templates(),
    queryFn: ({ signal }) => unwrap(listAuthorizationTemplates({ signal })),
  });
}

/** Re-read one access list after a write. */
export function useInvalidateAccess() {
  const queryClient = useQueryClient();
  return useCallback(
    (queryKey: readonly unknown[]) =>
      queryClient.invalidateQueries({ queryKey }),
    [queryClient],
  );
}
