import type { Format } from "../client";
import {
  canonicalRawSearchPrefix,
  decodeRawPathForDisplay,
} from "../lib/rawPath";
import { FORMAT_DESCRIPTORS } from "./descriptors";
import type { FormatDescriptor, RepositoryFormatFeature } from "./types";

export type { FormatDescriptor, RepositoryFormatFeature } from "./types";

/**
 * The descriptor for a format, or `undefined` for one this Console does not
 * know (a newer server); callers show RepositoryFeatureUnavailable then.
 */
export function formatDescriptor(
  format: string | undefined,
): FormatDescriptor | undefined {
  return format && Object.hasOwn(FORMAT_DESCRIPTORS, format)
    ? FORMAT_DESCRIPTORS[format as Format]
    : undefined;
}

/** Format-specific tasks; the server still gates each task's operations. */
export function supportsRepositoryFeature(
  format: string | undefined,
  repositoryType: "hosted" | "proxy" | undefined,
  feature: RepositoryFormatFeature,
): boolean {
  const descriptor = formatDescriptor(format);
  if (!descriptor) return false;
  if (feature === "publish")
    return repositoryType === "hosted" && descriptor.publishGuide;
  return (descriptor.repositoryFeatures[feature] ?? []).some(
    (type) => type === repositoryType,
  );
}

/** Packages listed one row per package with version pages. */
export function hasPackageVersions(format: string | undefined): boolean {
  return formatDescriptor(format)?.packageVersions ?? false;
}

/** Whether a repository of this format and type offers a directory view. */
export function supportsDirectoryBrowse(
  format: string | undefined,
  repositoryType: "hosted" | "proxy" | "group" | (string & {}) | undefined,
): boolean {
  return (formatDescriptor(format)?.directoryBrowse ?? []).some(
    (type) => type === repositoryType,
  );
}

/** A stored coordinate as the reader sees it, for any format. */
export function artifactCoordinateForDisplay(
  format: string,
  coordinate: string,
): string {
  return formatDescriptor(format)?.encodedPaths
    ? decodeRawPathForDisplay(coordinate)
    : coordinate;
}

/** A search box value as the API's coordinate prefix, for any format. */
export function artifactSearchPrefix(format: string, query: string): string {
  return formatDescriptor(format)?.encodedPaths
    ? canonicalRawSearchPrefix(query)
    : query;
}
