import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { listConanRecipeRevisions } from "../../client";
import { formatDescriptor } from "../../formats";
import {
  readPublicOciManifestDetail,
  type PublicOciManifestDetail,
} from "../../lib/publicOci";
import { unwrap } from "../../lib/query/client";
import { publicKeys } from "../../lib/query/keys";
import type { ConanRevision, ConanRevisionPage } from "./conanGroupTypes";
import { VERSION_PAGE_SIZE } from "./constants";
import type { PublicRepository } from "./types";
import { readPublicOciTagPage } from "./publicVersionReaders";

/** Which version protocol public browse uses for a repository's artifacts. */
function versionSource(repository: { format?: string } | undefined) {
  return formatDescriptor(repository?.format)?.publicVersionSource;
}

export interface OciTagPage {
  items: string[];
  nextCursor?: string;
  loaded: boolean;
  loading: boolean;
  error?: string;
}

export interface OciManifestDetail extends PublicOciManifestDetail {
  loading: boolean;
  error?: string;
}

/** The version pages a visitor asked for, per artifact coordinate. */
interface Requests {
  repositoryId: string;
  /** OCI tag page cursors per image, first page "". */
  ociTags: Record<string, string[]>;
  /** Manifest details asked for explicitly (a selection or a Retry). */
  ociManifests: { coordinate: string; tag: string }[];
  /** Conan revision page tokens per reference, for one revision filter. */
  conan: Record<string, { query: string; tokens: string[] }>;
}

const noRequests = (repositoryId: string): Requests => ({
  repositoryId,
  ociTags: {},
  ociManifests: [],
  conan: {},
});

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

/**
 * Per-artifact version pages for the public browser: OCI tags with their
 * manifest details, and Conan recipe revisions. Every page is a cached,
 * cancellable query; the hook keeps which pages a visitor asked for and
 * assembles them, in order, into one record per coordinate. Pages
 * accumulate as the visitor asks for more and start over with the
 * repository.
 */
export function usePublicVersionPages({
  repositoryId,
  selectedRepository,
  artifactParam,
  tagParam,
}: {
  repositoryId: string;
  selectedRepository: PublicRepository | undefined;
  artifactParam: string;
  tagParam: string;
}) {
  const queryClient = useQueryClient();
  const [stored, setRequests] = useState<Requests>(() =>
    noRequests(repositoryId),
  );
  // Requests belong to one repository; leaving it drops them for good.
  if (stored.repositoryId !== repositoryId)
    setRequests(noRequests(repositoryId));
  const requests =
    stored.repositoryId === repositoryId ? stored : noRequests(repositoryId);
  const source = versionSource(selectedRepository);
  const repository =
    selectedRepository?.id === repositoryId ? selectedRepository : undefined;

  const tagKey = (coordinate: string, cursor: string) =>
    [...publicKeys.all, "oci-tags", repositoryId, coordinate, cursor] as const;
  const manifestKey = (coordinate: string, tag: string) =>
    [...publicKeys.all, "oci-manifest", repositoryId, coordinate, tag] as const;
  const conanKey = (coordinate: string, query: string, token: string) =>
    [
      ...publicKeys.all,
      "conan-revisions",
      repositoryId,
      coordinate,
      query,
      token,
    ] as const;

  // OCI tag pages, in the order the visitor asked for them.
  const tagRequests = Object.entries(requests.ociTags).flatMap(
    ([coordinate, cursors]) =>
      cursors.map((cursor) => ({ coordinate, cursor })),
  );
  const tagResults = useQueries({
    queries: tagRequests.map(({ coordinate, cursor }) => ({
      queryKey: tagKey(coordinate, cursor),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        readPublicOciTagPage(repository, coordinate, cursor, signal),
      enabled: Boolean(repository) && source === "ociTags",
      meta: { authFailure: "ignore" as const },
      staleTime: Infinity,
    })),
  });
  const ociTagPages: Record<string, OciTagPage> = {};
  tagRequests.forEach(({ coordinate }, index) => {
    const result = tagResults[index];
    const current = ociTagPages[coordinate] ?? {
      items: [],
      loaded: false,
      loading: false,
    };
    const first = requests.ociTags[coordinate][0] === tagRequests[index].cursor;
    ociTagPages[coordinate] = {
      items: [...new Set([...current.items, ...(result.data?.tags ?? [])])],
      // Only the last page asked for decides whether more exist.
      nextCursor: result.data ? result.data.nextCursor : current.nextCursor,
      loaded: first ? !result.isPending : current.loaded,
      loading: current.loading || result.isFetching,
      error:
        current.error ??
        (result.error
          ? errorMessage(result.error, "读取 OCI 标签失败")
          : undefined),
    };
  });

  // A manifest is read for the version a row shows once its first tag page
  // arrives (the deep-linked tag, else the first), and for any version the
  // visitor picks.
  const manifestRequests = [...requests.ociManifests];
  for (const [coordinate, cursors] of Object.entries(requests.ociTags)) {
    const firstTags =
      tagResults[
        tagRequests.findIndex(
          (request) =>
            request.coordinate === coordinate && request.cursor === cursors[0],
        )
      ]?.data?.tags;
    const shown =
      tagParam && artifactParam === coordinate ? tagParam : firstTags?.[0];
    if (firstTags && shown) manifestRequests.push({ coordinate, tag: shown });
  }
  const uniqueManifests = manifestRequests.filter(
    (request, index) =>
      manifestRequests.findIndex(
        (other) =>
          other.coordinate === request.coordinate && other.tag === request.tag,
      ) === index,
  );
  const manifestResults = useQueries({
    queries: uniqueManifests.map(({ coordinate, tag }) => ({
      queryKey: manifestKey(coordinate, tag),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        readPublicOciManifestDetail(
          {
            repositoryName: repository?.name ?? "",
            imageName: coordinate,
            reference: tag,
          },
          (input, init) => fetch(input, { ...init, signal }),
        ),
      enabled: Boolean(repository) && source === "ociTags",
      meta: { authFailure: "ignore" as const },
      staleTime: Infinity,
    })),
  });
  const ociManifestDetails: Record<string, OciManifestDetail> = {};
  uniqueManifests.forEach(({ coordinate, tag }, index) => {
    const result = manifestResults[index];
    ociManifestDetails[`${coordinate}\x00${tag}`] = {
      ...(result.data ?? {}),
      loading: result.isFetching,
      error: result.error
        ? errorMessage(result.error, "读取 OCI manifest 失败")
        : undefined,
    } as OciManifestDetail;
  });

  // Conan recipe revision pages for each reference's current filter.
  const conanRequests = Object.entries(requests.conan).flatMap(
    ([coordinate, { query, tokens }]) =>
      tokens.map((token) => ({ coordinate, query, token })),
  );
  const conanResults = useQueries({
    queries: conanRequests.map(({ coordinate, query, token }) => ({
      queryKey: conanKey(coordinate, query, token),
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const page = await unwrap(
          listConanRecipeRevisions({
            path: { repositoryId: repository?.id ?? "" },
            query: {
              reference: coordinate,
              q: query || undefined,
              pageSize: VERSION_PAGE_SIZE,
              pageToken: token || undefined,
            },
            signal,
          }),
        );
        if (!page) throw new Error("读取 Conan revisions 失败");
        return page;
      },
      enabled: Boolean(repository) && source === "conanRevisions",
      meta: { authFailure: "ignore" as const },
      staleTime: Infinity,
    })),
  });
  const conanRevisionPages: Record<string, ConanRevisionPage> = {};
  conanRequests.forEach(({ coordinate, query, token }, index) => {
    const result = conanResults[index];
    const current = conanRevisionPages[coordinate] ?? {
      items: [] as ConanRevision[],
      query,
      loaded: false,
      loading: false,
    };
    const first = requests.conan[coordinate].tokens[0] === token;
    conanRevisionPages[coordinate] = {
      items: [...current.items, ...(result.data?.items ?? [])],
      nextPageToken: result.data
        ? result.data.nextPageToken
        : current.nextPageToken,
      query,
      loaded: first ? !result.isPending : current.loaded,
      loading: current.loading || result.isFetching,
      error:
        current.error ??
        (result.error ? "读取 Conan revisions 失败" : undefined),
    };
  });

  const loadOciTags = async (coordinate: string, after = "") => {
    if (!repository || source !== "ociTags") return;
    if (!after) {
      // The panel's Retry omits the cursor. Resume a failed continuation
      // without throwing away the earlier successful pages.
      const failedCursor = requests.ociTags[coordinate]?.find(
        (cursor) =>
          queryClient.getQueryState(tagKey(coordinate, cursor))?.error,
      );
      if (failedCursor) {
        await queryClient.refetchQueries({
          queryKey: tagKey(coordinate, failedCursor),
          exact: true,
        });
        return;
      }
      // A first read or a Retry starts the image's pages over.
      setRequests((current) => ({
        ...current,
        ociTags: { ...current.ociTags, [coordinate]: [""] },
      }));
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: [...publicKeys.all, "oci-tags", repositoryId, coordinate],
        }),
        // Tags are mutable references. Restarting their pages must also
        // refresh their manifests, including on a repository's return.
        queryClient.invalidateQueries({
          queryKey: [
            ...publicKeys.all,
            "oci-manifest",
            repositoryId,
            coordinate,
          ],
        }),
      ]);
      return;
    }
    setRequests((current) => {
      const cursors = current.ociTags[coordinate] ?? [""];
      return cursors.includes(after)
        ? current
        : {
            ...current,
            ociTags: { ...current.ociTags, [coordinate]: [...cursors, after] },
          };
    });
    // Asking again for a page that failed reads it again.
    if (queryClient.getQueryState(tagKey(coordinate, after))?.error)
      await queryClient.refetchQueries({
        queryKey: tagKey(coordinate, after),
        exact: true,
      });
  };

  const loadOciManifest = async (coordinate: string, tag: string) => {
    if (!repository || source !== "ociTags" || !tag) return;
    setRequests((current) =>
      current.ociManifests.some(
        (request) => request.coordinate === coordinate && request.tag === tag,
      )
        ? current
        : {
            ...current,
            ociManifests: [...current.ociManifests, { coordinate, tag }],
          },
    );
    if (queryClient.getQueryState(manifestKey(coordinate, tag))?.error)
      await queryClient.refetchQueries({
        queryKey: manifestKey(coordinate, tag),
        exact: true,
      });
  };

  const loadConanRevisions = async (
    coordinate: string,
    query = "",
    pageToken = "",
  ) => {
    if (!repository || source !== "conanRevisions") return;
    const normalizedQuery = query.trim();
    const current = requests.conan[coordinate];
    if (pageToken && current?.query === normalizedQuery) {
      setRequests((state) => {
        const tokens = state.conan[coordinate]?.tokens ?? [""];
        return tokens.includes(pageToken)
          ? state
          : {
              ...state,
              conan: {
                ...state.conan,
                [coordinate]: {
                  query: normalizedQuery,
                  tokens: [...tokens, pageToken],
                },
              },
            };
      });
      if (
        queryClient.getQueryState(
          conanKey(coordinate, normalizedQuery, pageToken),
        )?.error
      )
        await queryClient.refetchQueries({
          queryKey: conanKey(coordinate, normalizedQuery, pageToken),
          exact: true,
        });
      return;
    }
    // A new filter, a first read or a Retry starts the reference over.
    setRequests((state) => ({
      ...state,
      conan: {
        ...state.conan,
        [coordinate]: { query: normalizedQuery, tokens: [""] },
      },
    }));
    await queryClient.invalidateQueries({
      queryKey: [
        ...publicKeys.all,
        "conan-revisions",
        repositoryId,
        coordinate,
        normalizedQuery,
      ],
    });
  };

  return {
    ociTagPages,
    ociManifestDetails,
    conanRevisionPages,
    loadOciTags,
    loadOciManifest,
    loadConanRevisions,
  };
}
