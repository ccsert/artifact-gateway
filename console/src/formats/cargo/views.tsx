import { CargoCrateDetail } from "../../features/artifact-detail/CargoCrateDetail";
import { CargoPublishGuide } from "../../features/repository/RepositoryUsageGuides";
import type { FormatViews } from "../viewTypes";

export const cargoViews: FormatViews = {
  ArtifactDetail: ({
    repositoryName,
    artifact,
    initialVersion,
    onVersionChange,
  }) => (
    <CargoCrateDetail
      repoName={repositoryName}
      crateName={artifact.coordinate}
      // A crate always opens on a version: the requested one, else the newest.
      initialVersion={initialVersion || artifact.latestVersion}
      onVersionChange={onVersionChange}
    />
  ),
  PublishGuide: ({ repositoryName }) => (
    <CargoPublishGuide repoName={repositoryName} />
  ),
};
