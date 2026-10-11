import type { Format } from "../client";
import { FORMAT_DESCRIPTORS } from "./descriptors";
import type { FormatDescriptor } from "./types";

export type { FormatDescriptor } from "./types";

/**
 * The descriptor for a format, or `undefined` for one this Console does not
 * know (a newer server); callers show their generic presentation then.
 */
export function formatDescriptor(
  format: string | undefined,
): FormatDescriptor | undefined {
  return format && Object.hasOwn(FORMAT_DESCRIPTORS, format)
    ? FORMAT_DESCRIPTORS[format as Format]
    : undefined;
}

/** Packages listed one row per package with version pages. */
export function hasPackageVersions(format: string | undefined): boolean {
  return formatDescriptor(format)?.packageVersions ?? false;
}

/** Whether a repository of this format and type offers a directory view. */
export function supportsDirectoryBrowse(
  format: string | undefined,
  repositoryType: string | undefined,
): boolean {
  return (formatDescriptor(format)?.directoryBrowse ?? []).some(
    (type) => type === repositoryType,
  );
}
