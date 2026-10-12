import { listConanReferences } from "../../client";
import type { ListArtifacts } from "../artifactListing";

/**
 * Recipe references. The endpoint has no search parameter, so the query
 * filters each page by substring.
 */
export const listConanArtifacts: ListArtifacts = async ({
  repositoryId,
  query,
  pageToken,
  pageSize,
  signal,
}) => {
  const response = await listConanReferences({
    path: { repositoryId },
    query: { pageSize, pageToken },
    signal,
  });
  return {
    items: (response.data?.items ?? [])
      .filter((reference) => !query || reference.reference.includes(query))
      .map((reference) => ({
        key: reference.reference,
        coordinate: reference.reference,
        publisher: reference.publisher,
      })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
};
