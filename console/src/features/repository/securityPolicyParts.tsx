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
  text: (zh: string, en: string) => string,
): { boundary: string; metadata: string } {
  switch (format) {
    case "raw":
      return {
        boundary: text("路径及校验 sidecar", "Path and checksum sidecars"),
        metadata: text("从目录列表隐藏", "Hidden from directory listings"),
      };
    case "maven":
      return {
        boundary: text("坐标内的制品资产", "Assets within the coordinate"),
        metadata: text("从生成元数据隐藏", "Hidden from generated metadata"),
      };
    case "oci":
      return {
        boundary: text(
          "manifest 与描述符闭包",
          "Manifest and descriptor closure",
        ),
        metadata: text(
          "从 tag、catalog、referrer 隐藏",
          "Hidden from tags, catalog, and referrers",
        ),
      };
    case "npm":
      return {
        boundary: "package@version",
        metadata: text(
          "从 packument 与 dist-tag 隐藏",
          "Hidden from packuments and dist-tags",
        ),
      };
    case "pypi":
      return {
        boundary: "project@version",
        metadata: text("从 Simple 元数据隐藏", "Hidden from Simple metadata"),
      };
    case "conan":
      return {
        boundary: text("recipe revision 闭包", "Recipe revision closure"),
        metadata: text(
          "从 revision 元数据隐藏",
          "Hidden from revision metadata",
        ),
      };
    case "go":
      return {
        boundary: text(
          "module@version 全部表示",
          "Every module@version representation",
        ),
        metadata: text(
          "从 @v/list 与 @latest 隐藏",
          "Hidden from @v/list and @latest",
        ),
      };
    default:
      return {
        boundary: text("制品身份", "Artifact identity"),
        metadata: text("从发现元数据隐藏", "Hidden from discovery metadata"),
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
