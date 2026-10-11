import type { Format } from "../client";
import { formatDescriptor } from "./index";
import {
  listPackageArtifacts,
  pathArtifactLister,
  type ArtifactPage,
  type ArtifactPageRequest,
  type ListArtifacts,
} from "./artifactListing";
import { listConanArtifacts } from "./conan/artifacts";
import { listMavenArtifacts } from "./maven/artifacts";
import { listOciArtifacts } from "./oci/artifacts";

export type {
  ArtifactPage,
  ArtifactPageRequest,
  ListArtifacts,
} from "./artifactListing";

/** How each format lists a hosted repository's artifacts, one page at a time. */
const LISTERS: Partial<Record<Format, ListArtifacts>> = {
  oci: listOciArtifacts,
  maven: listMavenArtifacts,
  conan: listConanArtifacts,
};

/**
 * The artifact list for a format: its own lister, or the shared package or
 * path listing its descriptor places it in. Unknown formats list nothing.
 */
export function artifactLister(
  format: string | undefined,
): ListArtifacts | undefined {
  const descriptor = formatDescriptor(format);
  if (!descriptor) return undefined;
  if (LISTERS[descriptor.format]) return LISTERS[descriptor.format];
  if (descriptor.packageVersions) return listPackageArtifacts;
  if (descriptor.pathAddressed) return pathArtifactLister(descriptor.format);
  return undefined;
}

/** One page of a repository's artifacts; an unknown format lists nothing. */
export function listArtifactPage(
  format: string | undefined,
  request: ArtifactPageRequest,
): Promise<ArtifactPage> {
  const list = artifactLister(format);
  return list
    ? list(request)
    : Promise.resolve({ items: [], nextPageToken: undefined, error: null });
}
