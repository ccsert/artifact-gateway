import { NpmPackageDetail } from "../../features/artifact-detail/NpmPackageDetail";
import { NpmPublishGuide } from "../../features/repository/RepositoryUsageGuides";
import type { FormatViews } from "../viewTypes";

export const npmViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    initialVersion,
    canQuarantine,
    onVersionChange,
  }) => (
    <NpmPackageDetail
      repositoryId={repositoryId}
      repoName={repositoryName}
      packageName={artifact.coordinate}
      initialVersion={initialVersion}
      size={artifact.size}
      publisher={artifact.publisher}
      canQuarantine={canQuarantine}
      onVersionChange={onVersionChange}
    />
  ),
  PublishGuide: ({ repositoryName }) => (
    <NpmPublishGuide repoName={repositoryName} />
  ),
};
