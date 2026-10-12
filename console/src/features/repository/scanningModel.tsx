import type { LifecycleJob, Repository } from "../../client";
import { type RepositoryArtifactIdentity } from "./RepositoryArtifactSelect";

export type ScanForm = {
  coordinate: string;
  digest: string;
};

export function scanStatus(
  option: RepositoryArtifactIdentity,
  jobs: LifecycleJob[] | null,
  text: (zh: string, en: string) => string,
): { color?: string; label: string } {
  const job = jobs?.find(
    (candidate) =>
      candidate.details?.coordinate === option.coordinate &&
      candidate.details.digest === option.digest,
  );
  switch (job?.state) {
    case "completed":
      return { color: "success", label: text("扫描完成", "Scan completed") };
    case "pending":
    case "running":
    case "retrying":
      return { color: "processing", label: text("扫描中", "Scanning") };
    case "failed":
    case "cancelled":
      return { color: "error", label: text("扫描失败", "Scan failed") };
  }
  switch (option.intelligence?.vulnerabilityStatus) {
    case "clean":
      return { color: "success", label: text("已扫描", "Scanned") };
    case "affected":
      return { color: "warning", label: text("发现漏洞", "Affected") };
    case "error":
      return { color: "error", label: text("扫描异常", "Scan error") };
  }
  if (
    (option.intelligence?.sbomCount ?? 0) > 0 ||
    (option.intelligence?.licenseCount ?? 0) > 0
  ) {
    return { color: "processing", label: text("已有情报", "Has evidence") };
  }
  return { label: text("未扫描", "Not scanned") };
}

export function coordinatePlaceholder(
  format: Repository["format"],
  fallback: string,
): string {
  switch (format) {
    case "maven":
      return "com.example:widget:1.2.3";
    case "oci":
      return "team/widget";
    case "npm":
      return "@team/widget@1.2.3";
    case "pypi":
      return "widget-core@1.2.3";
    case "conan":
      return "widget/1.2.3@team/stable#recipe-revision";
    case "go":
      return "example.com/team/widget@v1.2.3";
    case "raw":
      return "releases/widget-1.2.3.tar.gz";
    default:
      return fallback;
  }
}

export function manualScanIdempotencyKey(repositoryId: string): string {
  const suffix =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `manual-scan:${repositoryId}:${suffix}`;
}
