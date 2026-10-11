import {
  keepPreviousData,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  getOidcSettings,
  listUserIdentities,
  listUserSessions,
  listUsers,
} from "../../client";
import type { ListUsersData, ListUsersResponse, User } from "../../client";
import { unwrap } from "../../lib/query/client";
import { accessKeys, identityKeys } from "../../lib/query/keys";

export type UserListQuery = NonNullable<ListUsersData["query"]>;

/**
 * One page of the user list. Keyed by the filters, so a slower answer for a
 * previous search can never replace the current one; the previous page stays
 * on screen while the next loads.
 */
export function useUserPage(query: UserListQuery) {
  return useQuery({
    queryKey: [...identityKeys.users(), query] as const,
    queryFn: ({ signal }) => unwrap(listUsers({ query, signal })),
    placeholderData: keepPreviousData,
  });
}

/**
 * After a user changed: show the saved record in every cached page at once,
 * then re-read the lists (and the access directory, which names users too).
 */
export function useUserChanged() {
  const queryClient = useQueryClient();
  return useCallback(
    async (updated?: User) => {
      if (updated) {
        queryClient.setQueriesData<ListUsersResponse>(
          { queryKey: identityKeys.users() },
          (page) =>
            page && {
              ...page,
              items: page.items.map((user) =>
                user.id === updated.id ? updated : user,
              ),
            },
        );
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: identityKeys.users() }),
        queryClient.invalidateQueries({
          queryKey: accessKeys.directory("users"),
        }),
      ]);
    },
    [queryClient],
  );
}

/** Sessions change whenever the user signs in, so they are always re-read. */
export function useUserSessions(userId: string, includeInactive: boolean) {
  return useQuery({
    queryKey: [...identityKeys.userSessions(userId), includeInactive] as const,
    queryFn: ({ signal }) =>
      unwrap(
        listUserSessions({
          path: { userId },
          query: { includeInactive },
          signal,
        }),
      ),
    staleTime: 0,
  });
}

export function useUserIdentities(userId: string) {
  return useQuery({
    queryKey: identityKeys.userIdentities(userId),
    queryFn: ({ signal }) =>
      unwrap(listUserIdentities({ path: { userId }, signal })),
  });
}

/**
 * The configured OIDC issuer, used to prefill identity links. Settings are
 * edited on another page, and an unreadable setting just leaves it blank.
 */
export function useOidcIssuer() {
  return useQuery({
    queryKey: identityKeys.oidcIssuer(),
    queryFn: async ({ signal }) =>
      (await getOidcSettings({ signal })).data?.issuer ?? "",
    staleTime: 0,
  });
}
