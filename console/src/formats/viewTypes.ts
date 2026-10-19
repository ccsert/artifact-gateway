import type { ComponentType } from "react";

/** The fields an artifact list row can carry, whichever page lists it. */
export interface DetailArtifact {
  coordinate: string;
  digest?: string;
  size?: number;
  contentType?: string;
  createdAt?: string;
  cachedAt?: string;
  sourceUrl?: string;
  publisher?: string;
  buildNumber?: number;
  /** The newest version, for formats that open on a version. */
  latestVersion?: string;
}

/**
 * One artifact's detail, as the repository tab, global search and public
 * browse show it. `repositoryId` is absent on anonymous public pages.
 */
export interface ArtifactDetailProps {
  repositoryName: string;
  repositoryId?: string;
  repositoryType?: "hosted" | "proxy";
  artifact: DetailArtifact;
  /** Version to open first, for formats with version pages. */
  initialVersion?: string;
  /** Tag or digest to open first, for image formats. */
  initialReference?: string;
  /** The repository's own content, which its administrators manage. */
  managed?: boolean;
  canWrite?: boolean;
  canDelete?: boolean;
  canQuarantine?: boolean;
  onDeleted?: () => void;
  onVersionChange?: (version: string) => void;
}

export interface PublishGuideProps {
  repositoryId: string;
  repositoryName: string;
  onPublished: () => void;
}

/** The format-specific pieces pages render; every slot is optional. */
export interface FormatViews {
  ArtifactDetail?: ComponentType<ArtifactDetailProps>;
  /** How to publish into a hosted repository of this format. */
  PublishGuide?: ComponentType<PublishGuideProps>;
}
