import { useCallback, useEffect, useState } from "react";
import { listConanRecipeRevisions, listOciManifests } from "../../client";
import {
  nextPublicOciTagCursor,
  readPublicOciManifestDetail,
  type PublicOciManifestDetail,
} from "../../lib/publicOci";
import type { ConanRevisionPage } from "./ConanGroupTable";
import { VERSION_PAGE_SIZE } from "./constants";
import type { PublicRepository } from "./types";

/**
 * Per-artifact version pages for the public browser: OCI tags with their
 * manifest details, and Conan recipe revisions. Pages accumulate as the
 * visitor asks for more, keyed by artifact coordinate, and reset with the
 * repository.
 */

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
  const [ociTagPages, setOciTagPages] = useState<Record<string, OciTagPage>>(
    {},
  );
  const [ociManifestDetails, setOciManifestDetails] = useState<
    Record<string, OciManifestDetail>
  >({});
  const [conanRevisionPages, setConanRevisionPages] = useState<
    Record<string, ConanRevisionPage>
  >({});

  useEffect(() => {
    setOciTagPages({});
    setOciManifestDetails({});
    setConanRevisionPages({});
  }, [repositoryId]);

  const loadOciManifest = useCallback(
    async (coordinate: string, tag: string) => {
      if (selectedRepository?.format !== "oci" || !tag) return;
      const key = `${coordinate}\x00${tag}`;
      setOciManifestDetails((current) => ({
        ...current,
        [key]: { ...(current[key] ?? {}), loading: true, error: undefined },
      }));
      try {
        const detail = await readPublicOciManifestDetail({
          repositoryName: selectedRepository.name,
          imageName: coordinate,
          reference: tag,
        });
        setOciManifestDetails((current) => ({
          ...current,
          [key]: { ...detail, loading: false },
        }));
      } catch (requestError) {
        setOciManifestDetails((current) => ({
          ...current,
          [key]: {
            ...(current[key] ?? {}),
            loading: false,
            error:
              requestError instanceof Error
                ? requestError.message
                : "读取 OCI manifest 失败",
          },
        }));
      }
    },
    [selectedRepository],
  );

  const loadOciTags = useCallback(
    async (coordinate: string, after = "") => {
      if (selectedRepository?.format !== "oci") return;
      setOciTagPages((current) => ({
        ...current,
        [coordinate]: {
          items: after ? (current[coordinate]?.items ?? []) : [],
          loaded: current[coordinate]?.loaded ?? false,
          loading: true,
        },
      }));
      const imagePath = coordinate.split("/").map(encodeURIComponent).join("/");
      try {
        if (selectedRepository.type !== "group") {
          const { data, error: requestError } = await listOciManifests({
            path: { repositoryId: selectedRepository.id },
            query: {
              name: coordinate,
              pageSize: VERSION_PAGE_SIZE,
              pageToken: after || undefined,
            },
          });
          if (requestError || !data) throw new Error("读取 OCI 版本失败");
          const page = data.items.flatMap((manifest) =>
            manifest.tags.length > 0 ? manifest.tags : [manifest.digest],
          );
          setOciTagPages((current) => ({
            ...current,
            [coordinate]: {
              items: after
                ? [...new Set([...(current[coordinate]?.items ?? []), ...page])]
                : page,
              nextCursor: data.nextPageToken,
              loaded: true,
              loading: false,
            },
          }));
          const selectedVersion =
            tagParam && artifactParam === coordinate ? tagParam : page[0];
          if (!after && selectedVersion)
            void loadOciManifest(coordinate, selectedVersion);
          return;
        }
        const query = new URLSearchParams({ n: String(VERSION_PAGE_SIZE) });
        if (after) query.set("last", after);
        const response = await fetch(
          `/v2/${encodeURIComponent(selectedRepository.name)}/${imagePath}/tags/list?${query}`,
        );
        if (!response.ok)
          throw new Error(`读取 OCI 标签失败 (${response.status})`);
        const data = (await response.json()) as { tags?: string[] };
        const page = data.tags ?? [];
        setOciTagPages((current) => ({
          ...current,
          [coordinate]: {
            items: after
              ? [...new Set([...(current[coordinate]?.items ?? []), ...page])]
              : page,
            nextCursor: nextPublicOciTagCursor(
              response,
              page,
              VERSION_PAGE_SIZE,
              window.location.origin,
            ),
            loaded: true,
            loading: false,
          },
        }));
        const selectedTag =
          tagParam && artifactParam === coordinate ? tagParam : page[0];
        if (!after && selectedTag)
          void loadOciManifest(coordinate, selectedTag);
      } catch (requestError) {
        setOciTagPages((current) => ({
          ...current,
          [coordinate]: {
            items: current[coordinate]?.items ?? [],
            loaded: true,
            loading: false,
            error:
              requestError instanceof Error
                ? requestError.message
                : "读取 OCI 标签失败",
          },
        }));
      }
    },
    [artifactParam, loadOciManifest, selectedRepository, tagParam],
  );

  const loadConanRevisions = useCallback(
    async (coordinate: string, query = "", pageToken = "") => {
      if (selectedRepository?.format !== "conan") return;
      const normalizedQuery = query.trim();
      setConanRevisionPages((current) => ({
        ...current,
        [coordinate]: {
          items:
            pageToken && current[coordinate]?.query === normalizedQuery
              ? (current[coordinate]?.items ?? [])
              : [],
          query: normalizedQuery,
          loaded: current[coordinate]?.loaded ?? false,
          loading: true,
        },
      }));
      const { data, error: requestError } = await listConanRecipeRevisions({
        path: { repositoryId: selectedRepository.id },
        query: {
          reference: coordinate,
          q: normalizedQuery || undefined,
          pageSize: VERSION_PAGE_SIZE,
          pageToken: pageToken || undefined,
        },
      });
      if (requestError || !data) {
        setConanRevisionPages((current) => ({
          ...current,
          [coordinate]: {
            items: current[coordinate]?.items ?? [],
            query: normalizedQuery,
            loaded: true,
            loading: false,
            error: "读取 Conan revisions 失败",
          },
        }));
        return;
      }
      setConanRevisionPages((current) => ({
        ...current,
        [coordinate]: {
          items:
            pageToken && current[coordinate]?.query === normalizedQuery
              ? [...current[coordinate].items, ...data.items]
              : data.items,
          nextPageToken: data.nextPageToken,
          query: normalizedQuery,
          loaded: true,
          loading: false,
        },
      }));
    },
    [selectedRepository],
  );

  return {
    ociTagPages,
    ociManifestDetails,
    conanRevisionPages,
    loadOciTags,
    loadOciManifest,
    loadConanRevisions,
  };
}
