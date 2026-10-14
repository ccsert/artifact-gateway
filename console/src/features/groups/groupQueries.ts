import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback } from "react";
import {
  browseGroup,
  getGroupCapacity,
  getGroupResolution,
  listGroupMembers,
  listGroups,
} from "../../client";
import type { GroupBrowsePage as GroupBrowseResponse } from "../../client";
import { unwrap } from "../../lib/query/client";
import { groupKeys } from "../../lib/query/keys";

/** Groups, one cursor page at a time. */
export function useGroupPages() {
  return useInfiniteQuery({
    queryKey: groupKeys.list(),
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        listGroups({ query: { pageSize: 100, pageToken: pageParam }, signal }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextPageToken || undefined,
  });
}

/** Re-read the group list after a create, rename, member change or delete. */
export function useInvalidateGroups() {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: groupKeys.all }),
    [queryClient],
  );
}

/** Members and their matching write version, read before editing. */
export function useGroupMembers(groupId: string, enabled: boolean) {
  return useQuery({
    queryKey: groupKeys.members(groupId),
    queryFn: async ({ signal }) => {
      const result = await listGroupMembers({ path: { groupId }, signal });
      const members = await unwrap(Promise.resolve(result));
      if (!members) throw new Error("Empty Group members response");
      return {
        members,
        version: result.response?.headers.get("ETag")?.replaceAll('"', ""),
      };
    },
    staleTime: 0,
    enabled,
  });
}

/** A group's capacity, read while its dialog is open. */
export function useGroupCapacity(groupId: string, enabled: boolean) {
  return useQuery({
    queryKey: groupKeys.capacity(groupId),
    queryFn: ({ signal }) =>
      unwrap(getGroupCapacity({ path: { groupId }, signal })),
    enabled,
    staleTime: 0,
  });
}

/**
 * A group's directory root. The browse tree loads deeper pages itself and
 * writes a refreshed root back here.
 */
export function useGroupBrowseRoot(groupId: string) {
  return useQuery({
    queryKey: groupKeys.browse(groupId),
    queryFn: async ({ signal }) => {
      const page = await unwrap(
        browseGroup({ path: { groupId }, query: { pageSize: 50 }, signal }),
      );
      if (!page) throw new Error("Empty Group directory response");
      return page;
    },
    staleTime: 0,
  });
}

export function useSetGroupBrowseRoot(groupId: string) {
  const queryClient = useQueryClient();
  return useCallback(
    (page: GroupBrowseResponse) =>
      queryClient.setQueryData(groupKeys.browse(groupId), page),
    [queryClient, groupId],
  );
}

/** The order in which a group resolves its members, read while shown. */
export function useGroupResolution(groupId: string, enabled: boolean) {
  return useQuery({
    queryKey: groupKeys.resolution(groupId),
    queryFn: async ({ signal }) => {
      const plan = await unwrap(
        getGroupResolution({ path: { groupId }, signal }),
      );
      if (!plan) throw new Error("Empty resolution response");
      return plan;
    },
    enabled,
    staleTime: 0,
  });
}
