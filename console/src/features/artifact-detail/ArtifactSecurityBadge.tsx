import type { Translate } from "../../lib/preferences";
import type { ArtifactIntelligenceSummary } from "../../client";
import { Badge } from "../../components/ui/Badge";

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
  t,
}: {
  summary?: ArtifactIntelligenceSummary;
  t: Translate;
}) {
  if (!summary)
    return <Badge tone="neutral">{t("artifactDetail.notScanned")}</Badge>;
  if (summary.vulnerabilityStatus === "affected") {
    return (
      <Badge tone="danger">
        {t("artifactDetail.affected2")} · {artifactSecurityFindings(summary)}
      </Badge>
    );
  }
  if (summary.vulnerabilityStatus === "clean") {
    return <Badge tone="success">{t("artifactDetail.clean2")}</Badge>;
  }
  if (summary.vulnerabilityStatus === "error") {
    return <Badge tone="warning">{t("artifactDetail.scanError")}</Badge>;
  }
  return (
    <Badge tone="info">
      {summary.signatureCount + summary.sbomCount + summary.licenseCount}{" "}
      {t("artifactDetail.evidence")}
    </Badge>
  );
}
