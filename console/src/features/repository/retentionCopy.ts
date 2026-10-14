import type { Translate } from "../../lib/preferences";
import type { Repository, RetentionDryRun } from "../../client";

/** Retention labels and hints in each format's own vocabulary. */
export function retentionFormatCopy(
  format: Repository["format"],
  t: Translate,
) {
  switch (format) {
    case "maven":
      return {
        ageLabel: t("repository.mavenReleaseRetentionDays"),
        ageHint: t("repository.aReleaseBecomesEligibleAfterThisManyDays"),
        minimumLabel: t("repository.minimumVersionsPerMavenModule"),
        minimumHint: t(
          "repository.groupByGroupIdArtifactIdAndAlwaysProtectThe",
        ),
        maximumLabel: t("repository.maximumVersionsPerMavenModule"),
        maximumHint: t("repository.use0ForNoLimitOlderReleasesOr"),
        matchLabel: t("repository.onlyCleanMatchingMavenCoordinates"),
        matchHint: t("repository.optionalRE2CoordinateRegexLeaveEmptyToMatch"),
        protectLabel: t("repository.protectMavenCoordinates"),
        protectHint: t(
          "repository.coordinatesMatchingAnyRegexNeverBecomeEligible",
        ),
        matchPlaceholder: t("repository.eGComExample"),
        protectPlaceholder: t("repository.eGComExamplePlatform"),
        candidateName: t("repository.mavenArtifactVersions"),
        candidateType: t("repository.mavenArtifactVersion"),
      };
    case "oci":
      return {
        ageLabel: t("repository.imageVersionRetentionDays"),
        ageHint: t("repository.aManifestBecomesEligibleAfterThisManyDays"),
        minimumLabel: t("repository.minimumVersionsPerImage"),
        minimumHint: t("repository.groupByImageNameAndAlwaysProtectThese"),
        maximumLabel: t("repository.maximumVersionsPerImage"),
        maximumHint: t("repository.use0ForNoLimitOlderManifestsBeyond"),
        matchLabel: t("repository.onlyCleanMatchingImages"),
        matchHint: t("repository.matchesImageNameNameDigestOrNameTag"),
        protectLabel: t("repository.protectImageVersions"),
        protectHint: t("repository.useAnImageNameToProtectAllVersions"),
        matchPlaceholder: t("repository.eGTeamBackend"),
        protectPlaceholder: t("repository.eGTeamBackendStable"),
        candidateName: t("repository.imageVersions"),
        candidateType: t("repository.manifestVersion"),
      };
    case "conan":
      return {
        ageLabel: t("repository.recipeRevisionRetentionDays"),
        ageHint: t("repository.aRecipeRevisionBecomesEligibleAfterThisMany"),
        minimumLabel: t("repository.minimumVersionsPerReference"),
        minimumHint: t("repository.groupByFullConanReferenceAndProtectThe"),
        maximumLabel: t("repository.maximumVersionsPerReference"),
        maximumHint: t("repository.use0ForNoLimitCleaningARecipe"),
        matchLabel: t("repository.onlyCleanMatchingReferences"),
        matchHint: t(
          "repository.matchesAFullReferenceOrReferenceRecipeRevision",
        ),
        protectLabel: t("repository.protectConanVersions"),
        protectHint: t(
          "repository.aMatchingReferenceProtectsAllRevisionsAnExact",
        ),
        matchPlaceholder: t("repository.eGOpenssl3"),
        protectPlaceholder: t("repository.eGReleaseStable"),
        candidateName: "Recipe revision",
        candidateType: "Recipe revision",
      };
    case "raw":
      return {
        ageLabel: t("repository.assetInactivityRetentionDays"),
        ageHint: t("repository.aPathAssetBecomesEligibleAfterItHas"),
        minimumLabel: "",
        minimumHint: "",
        maximumLabel: "",
        maximumHint: "",
        matchLabel: t("repository.onlyCleanMatchingPaths"),
        matchHint: t("repository.optionalRE2PathRegexLeaveEmptyToMatch"),
        protectLabel: t("repository.protectPaths"),
        protectHint: t("repository.pathsMatchingAnyRegexNeverBecomeEligible"),
        matchPlaceholder: t("repository.eGReleasesNightly"),
        protectPlaceholder: t("repository.eGReleasesStable"),
        candidateName: t("repository.pathAssets"),
        candidateType: t("repository.pathAsset"),
      };
    case "npm":
      return {
        ageLabel: t("repository.npmPackageVersionRetentionDays"),
        ageHint: t("repository.aPackageVersionBecomesEligibleAfterThisMany"),
        minimumLabel: t("repository.minimumVersionsPerNpmPackage"),
        minimumHint: t("repository.groupByPackageNameAndAlwaysProtectThese"),
        maximumLabel: t("repository.maximumVersionsPerNpmPackage"),
        maximumHint: t("repository.use0ForNoLimitEarlierPublicationsBeyond"),
        matchLabel: t("repository.onlyCleanMatchingNpmPackages"),
        matchHint: t("repository.matchesAPackageNameOrPackageVersionLeave"),
        protectLabel: t("repository.protectNpmPackageVersions"),
        protectHint: t("repository.matchAPackageNameToProtectEveryVersion"),
        matchPlaceholder: t("repository.eGTeam"),
        protectPlaceholder: t("repository.eGTeamPlatform"),
        candidateName: t("repository.npmPackageVersions"),
        candidateType: t("repository.npmPackageVersion"),
      };
    case "pypi":
      return {
        ageLabel: t("repository.pyPIProjectVersionRetentionDays"),
        ageHint: t("repository.aProjectVersionBecomesEligibleAfterThisMany"),
        minimumLabel: t("repository.minimumVersionsPerPyPIProject"),
        minimumHint: t(
          "repository.groupByNormalizedProjectNameAndAlwaysProtect",
        ),
        maximumLabel: t("repository.maximumVersionsPerPyPIProject"),
        maximumHint: t("repository.use0ForNoLimitEarlierPublicationsBeyond2"),
        matchLabel: t("repository.onlyCleanMatchingPyPIProjects"),
        matchHint: t("repository.matchesAProjectNameOrProjectVersionLeave"),
        protectLabel: t("repository.protectPyPIProjectVersions"),
        protectHint: t("repository.matchAProjectNameToProtectEveryVersion"),
        matchPlaceholder: t("repository.eGInternal"),
        protectPlaceholder: t("repository.eGPlatformCore"),
        candidateName: t("repository.pyPIProjectVersions"),
        candidateType: t("repository.pyPIProjectVersion"),
      };
    case "go":
      return {
        ageLabel: t("repository.goModuleVersionRetentionDays"),
        ageHint: t("repository.aModuleVersionBecomesEligibleAfterThisMany"),
        minimumLabel: t("repository.minimumVersionsPerGoModule"),
        minimumHint: t("repository.groupByModulePathAndAlwaysProtectThese"),
        maximumLabel: t("repository.maximumVersionsPerGoModule"),
        maximumHint: t("repository.use0ForNoLimitEarlierPublicationsBeyond"),
        matchLabel: t("repository.onlyCleanMatchingGoModules"),
        matchHint: t("repository.matchesAModulePathOrModuleVersionLeave"),
        protectLabel: t("repository.protectGoModuleVersions"),
        protectHint: t("repository.matchAModulePathToProtectEveryVersion"),
        matchPlaceholder: t("repository.eGExampleComTeam"),
        protectPlaceholder: t("repository.eGExampleComTeamWidget"),
        candidateName: t("repository.goModuleVersions"),
        candidateType: t("repository.goModuleVersion"),
      };
    default:
      return {
        ageLabel: t("repository.artifactRetentionDays"),
        ageHint: t("repository.anArtifactBecomesEligibleAfterThisManyDays"),
        minimumLabel: t("repository.minimumVersionsPerGroup"),
        minimumHint: t(
          "repository.alwaysProtectTheseNewestVersionsInEachGroup",
        ),
        maximumLabel: t("repository.maximumVersionsPerGroup"),
        maximumHint: t("repository.use0ForNoLimitOlderVersionsBeyond"),
        matchLabel: t("repository.onlyCleanMatchingArtifacts"),
        matchHint: t("repository.optionalRE2RegexLeaveEmptyToMatchAll"),
        protectLabel: t("repository.protectArtifacts"),
        protectHint: t(
          "repository.artifactsMatchingAnyRegexNeverBecomeEligible",
        ),
        matchPlaceholder: t("repository.eGTeam2"),
        protectPlaceholder: t("repository.eGStable"),
        candidateName: t("repository.artifacts"),
        candidateType: t("repository.artifactVersion"),
      };
  }
}

export function retentionCandidateTypeLabel(
  versionType: RetentionDryRun["candidates"][number]["versionType"],
  versionLabel: string,
  t: Translate,
) {
  if (versionType === "snapshot") return t("repository.snapshotBuild");
  if (versionType === "release") return t("repository.releaseVersion");
  if (versionType === "asset") return t("repository.pathAsset");
  return versionLabel;
}
