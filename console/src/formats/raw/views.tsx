import { RawArtifactDetail } from "../../features/artifact-detail/RawArtifactDetail";
import type { FormatViews } from "../viewTypes";

export const rawViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    canDelete,
    canQuarantine,
    onDeleted,
  }) =>
    repositoryId ? (
      <RawArtifactDetail
        canDelete={canDelete}
        repositoryId={repositoryId}
        repoName={repositoryName}
        canQuarantine={canQuarantine}
        onDeleted={onDeleted}
        meta={{
          coordinate: artifact.coordinate,
          digest: artifact.digest,
          size: artifact.size,
          contentType: artifact.contentType,
          createdAt: artifact.createdAt,
          publisher: artifact.publisher,
        }}
      />
    ) : null,
};
