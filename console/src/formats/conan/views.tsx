import { ConanArtifactDetail } from "../../features/artifact-detail/ConanArtifactDetail";
import type { FormatViews } from "../viewTypes";

export const conanViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    managed = false,
    canDelete = false,
    canQuarantine,
    onDeleted,
  }) =>
    repositoryId ? (
      <ConanArtifactDetail
        repoId={repositoryId}
        repoName={repositoryName}
        managed={managed}
        canDelete={canDelete}
        canQuarantine={canQuarantine}
        onDeleted={onDeleted}
        meta={{
          coordinate: artifact.coordinate,
          digest: artifact.digest,
          size: artifact.size,
          createdAt: artifact.createdAt,
          publisher: artifact.publisher,
        }}
      />
    ) : null,
};
