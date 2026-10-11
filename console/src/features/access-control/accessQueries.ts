import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getAnonymousAccessPolicy,
  listRepositories,
  listRepositoryGrants,
  listServiceAccounts,
  listUsers,
  replaceAnonymousAccessPolicy,
} from "../../client";
import type { AnonymousAccessPolicy } from "../../client";
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

export function useGrantRows() {
  return useQuery({
    queryKey: accessKeys.grants(),
    queryFn: async ({ signal }): Promise<GrantRow[]> => {
      const data = await unwrap(listRepositoryGrants({ signal }));
      return data
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
    },
    staleTime: ALWAYS_REVALIDATE,
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
 * Who and what the evaluator and templates can name. Users, API keys and
 * repositories are required; service accounts degrade to a notice.
 */
export function useAccessDirectory() {
  const users = useQuery({
    queryKey: accessKeys.directory("users"),
    queryFn: ({ signal }) => unwrap(listUsers({ signal })),
    staleTime: ALWAYS_REVALIDATE,
  });
  const apiKeys = useApiKeys();
  const serviceAccounts = useQuery({
    queryKey: accessKeys.directory("service-accounts"),
    queryFn: ({ signal }) =>
      unwrap(listServiceAccounts({ query: { pageSize: 200 }, signal })),
    staleTime: ALWAYS_REVALIDATE,
  });
  const repositories = useQuery({
    queryKey: accessKeys.directory("repositories"),
    queryFn: ({ signal }) =>
      unwrap(listRepositories({ query: { pageSize: 200 }, signal })),
    staleTime: ALWAYS_REVALIDATE,
  });

  const required = [users, apiKeys, repositories];
  return {
    users: users.data?.items ?? [],
    apiKeys: apiKeys.data?.items ?? [],
    serviceAccounts: serviceAccounts.data?.items ?? [],
    repositories: (repositories.data?.items ?? []).filter(
      (repository) => repository.state === "active",
    ),
    loading: required.some((query) => query.isPending),
    error: required.find((query) => query.error)?.error ?? null,
    serviceAccountsError: serviceAccounts.error ?? null,
    retry: () => {
      for (const query of [...required, serviceAccounts]) void query.refetch();
    },
  };
}
