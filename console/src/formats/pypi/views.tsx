import { PyPIProjectDetail } from "../../features/artifact-detail/PyPIProjectDetail";
import { PyPIPublishGuide } from "../../features/repository/RepositoryUsageGuides";
import type { FormatViews } from "../viewTypes";

export const pypiViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    initialVersion,
    canQuarantine,
    onVersionChange,
  }) => (
    <PyPIProjectDetail
      repositoryId={repositoryId}
      repoName={repositoryName}
      project={artifact.coordinate}
      initialVersion={initialVersion}
      size={artifact.size}
      publisher={artifact.publisher}
      canQuarantine={canQuarantine}
      onVersionChange={onVersionChange}
    />
  ),
  PublishGuide: ({ repositoryName }) => (
    <PyPIPublishGuide repoName={repositoryName} />
  ),
};
