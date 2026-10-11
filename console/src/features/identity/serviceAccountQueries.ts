import {
  useInfiniteQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  listServiceAccountCredentials,
  listServiceAccounts,
  type ServiceAccountCredentialList,
} from "../../client";
import { unwrap } from "../../lib/query/client";
import { identityKeys } from "../../lib/query/keys";

const PAGE_SIZE = 200;

function validateCredentialPage(page: ServiceAccountCredentialList) {
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.some(
      (credential) =>
        !credential ||
        typeof credential.id !== "string" ||
        typeof credential.name !== "string",
    )
  ) {
    throw new Error("Invalid service account credentials response");
  }
  return page;
}

export function flattenPages<T>(
  data: InfiniteData<{ items: T[] }> | undefined,
): T[] | null {
  return data ? data.pages.flatMap((page) => page.items) : null;
}

/** Service accounts, one cursor page at a time. */
export function useServiceAccountPages() {
  return useInfiniteQuery({
    queryKey: identityKeys.serviceAccounts(),
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        listServiceAccounts({
          query: { pageSize: PAGE_SIZE, pageToken: pageParam },
          signal,
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
  });
}

/**
 * Credentials of one account. Keyed by account, so switching accounts can
 * never show a slower answer for the previous one.
 */
export function useCredentialPages(serviceAccountId: string) {
  return useInfiniteQuery({
    queryKey: identityKeys.credentials(serviceAccountId),
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        listServiceAccountCredentials({
          path: { serviceAccountId },
          query: { pageSize: PAGE_SIZE, pageToken: pageParam },
          signal,
        }),
      ).then(validateCredentialPage),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    enabled: Boolean(serviceAccountId),
  });
}

export function useInvalidateServiceAccounts() {
  const queryClient = useQueryClient();
  return useCallback(
    () =>
      queryClient.invalidateQueries({
        queryKey: identityKeys.serviceAccounts(),
        exact: true,
      }),
    [queryClient],
  );
}

export function useInvalidateCredentials() {
  const queryClient = useQueryClient();
  return useCallback(
    (serviceAccountId: string) =>
      queryClient.invalidateQueries({
        queryKey: identityKeys.credentials(serviceAccountId),
      }),
    [queryClient],
  );
}
