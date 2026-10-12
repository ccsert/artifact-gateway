import { useQuery } from "@tanstack/react-query";
import {
  getArtifactIntelligence,
  getArtifactQuarantine,
  getRepositoryArtifactScanStatus,
  listConanPackageIds,
  listConanPackageRevisions,
  listConanRecipeRevisions,
  listMavenCoordinates,
} from "../../client";
import type { MavenCoordinate } from "../../client";
import { isNotFound } from "../../components/ui/Feedback";
import { fetchOciManifest, fetchOciManifests } from "./ociImageModel";
import { unwrap } from "../../lib/query/client";
import { repositoryKeys } from "../../lib/query/keys";

/**
 * Every published coordinate of one Maven group:artifact, across all pages.
 * Keyed under the repository, so invalidating the repository refreshes it.
 */
export function useMavenCoordinates(repositoryId: string, ga: string | null) {
  return useQuery({
    queryKey: [...repositoryKeys.detail(repositoryId), "maven-ga", ga] as const,
    queryFn: async ({ signal }) => {
      const coordinates: MavenCoordinate[] = [];
      let pageToken: string | undefined;
      do {
        const page = await unwrap(
          listMavenCoordinates({
            path: { repositoryId },
            query: { q: ga ?? "", pageSize: 200, pageToken },
            signal,
          }),
        );
        coordinates.push(...(page?.items ?? []));
        pageToken = page?.nextPageToken;
      } while (pageToken);
      return coordinates;
    },
    enabled: Boolean(ga),
    staleTime: 0,
  });
}

/** The visible recipe revisions of one Conan reference, newest first. */
export function useConanRecipeRevisions(
  repositoryId: string,
  reference: string,
  managed: boolean,
) {
  return useQuery({
    queryKey: [
      ...repositoryKeys.detail(repositoryId),
      "conan-recipe",
      reference,
    ] as const,
    queryFn: async ({ signal }) => {
      const page = await unwrap(
        listConanRecipeRevisions({
          path: { repositoryId },
          query: { reference },
          signal,
        }),
      );
      return (page?.items ?? []).filter((item) => item.state === "visible");
    },
    enabled: managed,
    staleTime: 0,
  });
}

export const conanPackageRevisionsKey = (
  repositoryId: string,
  reference: string,
  recipeRevision: string,
) =>
  [
    ...repositoryKeys.detail(repositoryId),
    "conan-packages",
    reference,
    recipeRevision,
  ] as const;

/** The visible package revisions of every package under one recipe revision. */
export function useConanPackageRevisions(
  repositoryId: string,
  reference: string,
  recipeRevision: string,
  managed: boolean,
) {
  return useQuery({
    queryKey: conanPackageRevisionsKey(repositoryId, reference, recipeRevision),
    queryFn: async ({ signal }) => {
      const ids = await unwrap(
        listConanPackageIds({
          path: { repositoryId },
          query: { reference, recipeRevision },
          signal,
        }),
      );
      const pages = await Promise.all(
        (ids?.items ?? []).map((packageId) =>
          unwrap(
            listConanPackageRevisions({
              path: { repositoryId },
              query: { reference, recipeRevision, packageId },
              signal,
            }),
          ),
        ),
      );
      return pages
        .flatMap((page) => page?.items ?? [])
        .filter((item) => item.state === "visible");
    },
    enabled: managed && Boolean(recipeRevision),
    staleTime: 0,
  });
}

export const artifactQuarantineKey = (
  repositoryId: string,
  coordinate: string,
  digest: string,
) =>
  [
    ...repositoryKeys.detail(repositoryId),
    "quarantine",
    coordinate,
    digest,
  ] as const;

/** One artifact's quarantine record; null when it was never quarantined. */
export function useArtifactQuarantine(
  repositoryId: string | undefined,
  coordinate: string,
  digest: string | undefined,
) {
  return useQuery({
    queryKey: artifactQuarantineKey(
      repositoryId ?? "",
      coordinate,
      digest ?? "",
    ),
    queryFn: async ({ signal }) => {
      const response = await getArtifactQuarantine({
        path: { repositoryId: repositoryId ?? "" },
        query: { coordinate, digest: digest ?? "" },
        signal,
      });
      if (response.error) {
        if (response.response?.status === 404 || isNotFound(response.error))
          return null;
        throw response.error;
      }
      return response.data ?? null;
    },
    enabled: Boolean(repositoryId && coordinate && digest),
    staleTime: 0,
  });
}

/** One artifact's metadata and vulnerability summary; null when none exists. */
export function useArtifactIntelligence(
  repositoryId: string | undefined,
  coordinate: string,
  digest: string | undefined,
) {
  return useQuery({
    queryKey: [
      ...repositoryKeys.detail(repositoryId ?? ""),
      "intelligence",
      coordinate,
      digest,
    ] as const,
    queryFn: async ({ signal }) => {
      const response = await getArtifactIntelligence({
        path: { repositoryId: repositoryId ?? "" },
        query: { coordinate, digest: digest ?? "" },
        signal,
      });
      if (response.error) {
        if (isNotFound(response.error)) return null;
        throw response.error;
      }
      return response.data ?? null;
    },
    enabled: Boolean(repositoryId && coordinate && digest),
    staleTime: 0,
  });
}

export const artifactScanStatusKey = (
  repositoryId: string,
  coordinate: string,
  digest: string,
) =>
  [
    ...repositoryKeys.detail(repositoryId),
    "artifact-scan",
    coordinate,
    digest,
  ] as const;

const activeScanStates = new Set(["pending", "running", "retrying"]);

/** One artifact's scan status, polled while a scan is queued or running. */
export function useArtifactScanStatus(
  repositoryId: string | undefined,
  coordinate: string,
  digest: string | undefined,
) {
  return useQuery({
    queryKey: artifactScanStatusKey(
      repositoryId ?? "",
      coordinate,
      digest ?? "",
    ),
    queryFn: async ({ signal }) => {
      const status = await unwrap(
        getRepositoryArtifactScanStatus({
          path: { repositoryId: repositoryId ?? "" },
          query: { coordinate, digest: digest ?? "" },
          signal,
        }),
      );
      if (!status) throw new Error("Scan status response is empty");
      return status;
    },
    enabled: Boolean(repositoryId && coordinate && digest),
    staleTime: 0,
    refetchInterval: (query) =>
      query.state.data && activeScanStates.has(query.state.data.state)
        ? 2500
        : false,
  });
}

/** Every manifest of one OCI image, across all pages. */
export function useOciManifests(repositoryId: string, image: string) {
  return useQuery({
    queryKey: [...repositoryKeys.detail(repositoryId), "oci-manifests", image],
    queryFn: ({ signal }) => fetchOciManifests(repositoryId, image, signal),
    staleTime: 0,
  });
}

export const ociManifestKey = (
  repositoryId: string,
  name: string,
  reference: string,
) =>
  [
    ...repositoryKeys.detail(repositoryId),
    "oci-manifest",
    name,
    reference,
  ] as const;

/** One manifest and its config, read through the registry API. */
export function useOciManifest(
  repositoryId: string,
  token: string,
  name: string,
  reference: string | null,
) {
  return useQuery({
    queryKey: ociManifestKey(repositoryId, name, reference ?? ""),
    queryFn: ({ signal }) =>
      fetchOciManifest(token, name, reference ?? "", signal),
    enabled: Boolean(reference),
    staleTime: 0,
  });
}
