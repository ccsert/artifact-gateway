import { NpmPackageDetail } from "../../features/artifact-detail/NpmPackageDetail";
import { NpmPublishGuide } from "../../features/repository/RepositoryUsageGuides";
import type { FormatViews } from "../viewTypes";

export const npmViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    initialVersion,
    managed,
    canWrite,
    canQuarantine,
    onDeleted,
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
      canWrite={Boolean(managed && canWrite)}
      canUnpublish={Boolean(managed && canQuarantine)}
      onUnpublished={onDeleted}
      onVersionChange={onVersionChange}
    />
  ),
  PublishGuide: ({ repositoryName }) => (
    <NpmPublishGuide repoName={repositoryName} />
  ),
};
