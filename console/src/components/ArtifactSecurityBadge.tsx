import type { ArtifactIntelligenceSummary } from "../client";
import { Badge } from "./Badge";

type Localize = (zh: string, en: string) => string;

export function artifactSecurityFindings(
  summary: ArtifactIntelligenceSummary,
): number {
  return (
    (summary.critical ?? 0) +
    (summary.high ?? 0) +
    (summary.medium ?? 0) +
    (summary.low ?? 0) +
    (summary.unknown ?? 0)
  );
}

export function ArtifactSecurityBadge({
  summary,
  text,
}: {
  summary?: ArtifactIntelligenceSummary;
  text: Localize;
}) {
  if (!summary)
    return <Badge tone="neutral">{text("未扫描", "Not scanned")}</Badge>;
  if (summary.vulnerabilityStatus === "affected") {
    return (
      <Badge tone="danger">
        {text("有风险", "Affected")} · {artifactSecurityFindings(summary)}
      </Badge>
    );
  }
  if (summary.vulnerabilityStatus === "clean") {
    return <Badge tone="success">{text("通过", "Clean")}</Badge>;
  }
  if (summary.vulnerabilityStatus === "error") {
    return <Badge tone="warning">{text("扫描错误", "Scan error")}</Badge>;
  }
  return (
    <Badge tone="info">
      {summary.signatureCount + summary.sbomCount + summary.licenseCount}{" "}
      {text("项证据", "evidence")}
    </Badge>
  );
}
