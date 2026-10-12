import { listOciImages, searchRepositoryArtifacts } from "../../client";
import type { ListArtifacts } from "../artifactListing";

/** Images by name prefix; tags and manifests load in the image detail. */
export const listOciArtifacts: ListArtifacts = async ({
  repositoryId,
  query,
  pageToken,
  pageSize,
  match,
  signal,
}) => {
  if (query && match === "contains") {
    const response = await searchRepositoryArtifacts({
      path: { repositoryId },
      query: { q: query, match, pageSize, pageToken },
      signal,
    });
    return {
      items: (response.data?.items ?? []).map((item) => ({
        key: item.coordinate,
        coordinate: item.coordinate,
      })),
      nextPageToken: response.data?.nextPageToken,
      error: response.error,
    };
  }
  const response = await listOciImages({
    path: { repositoryId },
    query: { q: query || undefined, pageSize, pageToken },
    signal,
  });
  return {
    items: (response.data?.items ?? []).map((image) => ({
      key: image.name,
      coordinate: image.name,
    })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
};
