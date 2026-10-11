import type { Format, Repository } from "../client";

type Localized = readonly [chinese: string, english: string];

/** A copyable client endpoint for a group of this format. */
export interface GroupEndpoint {
  value: string;
  label: string;
}

/**
 * What the Console needs to know about one artifact format, declared once.
 * Pages ask the descriptor instead of branching on format names; the server's
 * own capability answers (FormatProfile, RepositoryCapabilities) still decide
 * which operations a repository allows.
 */
export interface FormatDescriptor {
  format: Format;
  /** Title of the artifact column in a repository's artifact table. */
  artifactColumn: Localized;
  /** Hint for the repository artifact search box. */
  searchPlaceholder: Localized;
  /**
   * Packages are listed one row per package and open on a version page
   * (npm, PyPI, Go, Cargo); links carry the selected version.
   */
  packageVersions: boolean;
  /** Artifacts are addressed by repository path (Raw, APT). */
  pathAddressed: boolean;
  /**
   * Stored coordinates are percent-encoded paths; show and search them in
   * their readable form (Raw).
   */
  encodedPaths: boolean;
  /**
   * Artifacts are listed by name and expand into versions or revisions (OCI
   * images, Conan references).
   */
  nameListing: boolean;
  /** Repository types, and groups, that offer a directory view. */
  directoryBrowse: readonly (Repository["type"] | "group")[];
  /** A proxy repository of this format needs an upstream host allow-list. */
  upstreamHostAllowList: boolean;
  /**
   * When artifact rows show the security summary: always, only for version
   * search results, or never.
   */
  securityColumn: "always" | "versionSearch" | "never";
  /** Global search opens a hit in the format's artifact detail. */
  searchDetail: boolean;
  /** Anonymous public browse opens an artifact in the format's detail. */
  publicDetail: boolean;
  /** Hosted repositories of this format have a publish guide. */
  publishGuide: boolean;
  /** The client endpoint a group of this format serves, if it has one. */
  groupEndpoint?: (groupName: string) => GroupEndpoint;
}
