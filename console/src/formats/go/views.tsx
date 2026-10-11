import { GoModuleDetail } from "../../features/artifact-detail/GoModuleDetail";
import type { FormatViews } from "../viewTypes";

export const goViews: FormatViews = {
  ArtifactDetail: ({
    repositoryId,
    repositoryName,
    artifact,
    initialVersion,
    canQuarantine,
    onVersionChange,
  }) => (
    <GoModuleDetail
      repositoryId={repositoryId}
      repoName={repositoryName}
      modulePath={artifact.coordinate}
      initialVersion={initialVersion}
      size={artifact.size}
      publisher={artifact.publisher}
      canQuarantine={canQuarantine}
      onVersionChange={onVersionChange}
    />
  ),
};
