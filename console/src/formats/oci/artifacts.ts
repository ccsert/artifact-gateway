import { listOciImages } from "../../client";
import type { ListArtifacts } from "../artifactListing";

/** Images by name prefix; tags and manifests load in the image detail. */
export const listOciArtifacts: ListArtifacts = async ({
  repositoryId,
  query,
  pageToken,
  pageSize,
}) => {
  const response = await listOciImages({
    path: { repositoryId },
    query: { q: query || undefined, pageSize, pageToken },
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
