import { OciImageDetail } from "../../features/artifact-detail/OciImageDetail";
import { OCIPublishGuide } from "../../features/repository/RepositoryUsageGuides";
import type { FormatViews } from "../viewTypes";
import { OCIDiscoveryNotice } from "./OCIDiscoveryNotice";

export const ociViews: FormatViews = {
  BrowserNotice: OCIDiscoveryNotice,
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    initialReference,
    canQuarantine,
    onDeleted,
  }) =>
    // Image detail reads the management API, so it needs the repository id.
    repositoryId ? (
      <OciImageDetail
        repositoryId={repositoryId}
        repository={repositoryName}
        image={artifact.coordinate}
        initialReference={initialReference}
        canQuarantine={canQuarantine}
        onDeleted={onDeleted}
      />
    ) : null,
  PublishGuide: ({ repositoryName }) => (
    <OCIPublishGuide repoName={repositoryName} />
  ),
};
