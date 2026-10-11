import { APTAssetDetail } from "../../features/artifact-detail/APTAssetDetail";
import type { FormatViews } from "../viewTypes";

export const aptViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    repositoryType,
    artifact,
    canQuarantine,
  }) => (
    <APTAssetDetail
      repositoryId={repositoryId}
      repoName={repositoryName}
      canQuarantine={canQuarantine}
      // Hosted APT assets are the repository's own published files.
      published={repositoryType === "hosted"}
      meta={{
        coordinate: artifact.coordinate,
        digest: artifact.digest,
        size: artifact.size,
        contentType: artifact.contentType,
        createdAt: artifact.createdAt,
        cachedAt: artifact.cachedAt,
        sourceUrl: artifact.sourceUrl,
      }}
    />
  ),
};
