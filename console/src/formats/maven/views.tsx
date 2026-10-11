import { MavenArtifactDetail } from "../../features/artifact-detail/ArtifactRowDetail";
import { MavenPublishWizard } from "../../features/repository/MavenPublishWizard";
import type { FormatViews } from "../viewTypes";

export const mavenViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    canQuarantine,
    onDeleted,
  }) =>
    repositoryId ? (
      <MavenArtifactDetail
        repoId={repositoryId}
        repoName={repositoryName}
        canQuarantine={canQuarantine}
        onDeleted={onDeleted}
        meta={{
          coordinate: artifact.coordinate,
          digest: artifact.digest,
          size: artifact.size,
          createdAt: artifact.createdAt,
          publisher: artifact.publisher,
          buildNumber: artifact.buildNumber,
        }}
      />
    ) : null,
  PublishGuide: ({ repositoryId, onPublished }) => (
    <MavenPublishWizard repositoryId={repositoryId} onPublished={onPublished} />
  ),
};
