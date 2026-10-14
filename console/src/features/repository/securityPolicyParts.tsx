import type { Translate } from "../../lib/preferences";
import { type ReactNode } from "react";
import { Switch } from "antd";
import type { Repository, SecurityPolicy } from "../../client";

export type SecurityPolicyDraft = Required<
  Pick<
    SecurityPolicy,
    | "enabled"
    | "autoScanOnPublish"
    | "requireSignature"
    | "requireVerifiedSignature"
    | "requireSbom"
    | "requireProvenance"
    | "requireVulnerabilityScan"
    | "maxAllowedSeverity"
    | "failOnScanError"
    | "allowedLicenses"
  >
>;

export const DEFAULT_DRAFT: SecurityPolicyDraft = {
  enabled: false,
  autoScanOnPublish: false,
  requireSignature: false,
  requireVerifiedSignature: false,
  requireSbom: false,
  requireProvenance: false,
  requireVulnerabilityScan: false,
  maxAllowedSeverity: "critical",
  failOnScanError: true,
  allowedLicenses: [],
};

export function draftFromPolicy(policy: SecurityPolicy): SecurityPolicyDraft {
  return {
    enabled: policy.enabled ?? false,
    autoScanOnPublish: policy.autoScanOnPublish ?? false,
    requireSignature: policy.requireSignature ?? false,
    requireVerifiedSignature: policy.requireVerifiedSignature ?? false,
    requireSbom: policy.requireSbom ?? false,
    requireProvenance: policy.requireProvenance ?? false,
    requireVulnerabilityScan: policy.requireVulnerabilityScan ?? false,
    maxAllowedSeverity: policy.maxAllowedSeverity ?? "critical",
    failOnScanError: policy.failOnScanError ?? true,
    allowedLicenses: policy.allowedLicenses ?? [],
  };
}

export function quarantineReadScope(
  format: Repository["format"],
  t: Translate,
): { boundary: string; metadata: string } {
  switch (format) {
    case "raw":
      return {
        boundary: t("repository.pathAndChecksumSidecars"),
        metadata: t("repository.hiddenFromDirectoryListings"),
      };
    case "maven":
      return {
        boundary: t("repository.assetsWithinTheCoordinate"),
        metadata: t("repository.hiddenFromGeneratedMetadata"),
      };
    case "oci":
      return {
        boundary: t("repository.manifestAndDescriptorClosure"),
        metadata: t("repository.hiddenFromTagsCatalogAndReferrers"),
      };
    case "npm":
      return {
        boundary: "package@version",
        metadata: t("repository.hiddenFromPackumentsAndDistTags"),
      };
    case "pypi":
      return {
        boundary: "project@version",
        metadata: t("repository.hiddenFromSimpleMetadata"),
      };
    case "conan":
      return {
        boundary: t("repository.recipeRevisionClosure"),
        metadata: t("repository.hiddenFromRevisionMetadata"),
      };
    case "go":
      return {
        boundary: t("repository.everyModuleVersionRepresentation"),
        metadata: t("repository.hiddenFromVListAndLatest"),
      };
    default:
      return {
        boundary: t("repository.artifactIdentity"),
        metadata: t("repository.hiddenFromDiscoveryMetadata"),
      };
  }
}

export function SettingRow({
  label,
  hint,
  meta,
  children,
}: {
  label: string;
  hint: string;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-[var(--ag-surface-table-header)] px-4 py-3.5">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-fg">{label}</span>
          {meta}
        </div>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">{hint}</p>
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

export function PolicySwitch({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div
      className={`flex min-h-24 items-start justify-between gap-5 rounded-lg border px-4 py-3.5 transition-colors ${
        checked
          ? "border-[var(--ag-border-default)] bg-[var(--ag-navigation-selected-start)]"
          : "border-line bg-[var(--ag-surface-table-header)]"
      }`}
    >
      <div className="min-w-0">
        <div className="text-sm font-medium text-fg">{label}</div>
        <div className="mt-1 text-xs leading-5 text-fg-tertiary">{hint}</div>
      </div>
      <Switch aria-label={label} checked={checked} onChange={onChange} />
    </div>
  );
}
