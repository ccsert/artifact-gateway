import type { SetURLSearchParams } from "react-router-dom";
import {
  artifactBrowseParams,
  artifactBrowsePath,
  clearArtifactBrowseParams,
} from "../../lib/publicBrowseModel";

/** Links to an artifact version in public browse, and moving between them. */
export function useArtifactBrowseLinks(
  params: URLSearchParams,
  setParams: SetURLSearchParams,
) {
  const artifactHref = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    return artifactBrowsePath(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
  };

  const openArtifact = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    const next = artifactBrowseParams(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const clearArtifactParams = () => {
    const next = clearArtifactBrowseParams(params);
    setParams(next, { replace: true, preventScrollReset: true });
  };

  return { artifactHref, openArtifact, clearArtifactParams };
}
