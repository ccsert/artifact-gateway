import { Card, Tag } from "antd";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ErrorBanner } from "../../components/ui/Feedback";
import { ArtifactVulnerabilityFindings } from "./ArtifactVulnerabilityFindings";
import { useArtifactIntelligence } from "./artifactDetailQueries";

export function ArtifactIntelligencePanel({
  repositoryId,
  format,
  coordinate,
  digest,
}: {
  repositoryId?: string;
  format: string;
  coordinate: string;
  digest?: string;
}) {
  const { locale, t } = usePreferences();
  const intelligence = useArtifactIntelligence(
    repositoryId,
    coordinate,
    digest,
  );
  const metadata = intelligence.data ?? null;
  const loading = intelligence.isPending && intelligence.fetchStatus !== "idle";
  const error = intelligence.error;

  if (loading) return null;
  if (error) {
    const message =
      (error as { message?: string })?.message ??
      t("artifactDetail.failedToLoadArtifactIntelligence");
    return (
      <div className="col-span-full">
        <ErrorBanner error={message} tone="warning" />
      </div>
    );
  }
  if (!metadata) return null;
  const vulnerability = metadata.vulnerability;
  const vulnerabilityTone =
    vulnerability?.status === "affected"
      ? "error"
      : vulnerability?.status === "clean"
        ? "success"
        : vulnerability?.status === "error"
          ? "warning"
          : "default";
  const vulnerabilityLabel = vulnerability
    ? {
        affected: t("artifactDetail.affected"),
        clean: t("artifactDetail.clean"),
        error: t("artifactDetail.scanFailed"),
        not_scanned: t("artifactDetail.notScanned"),
      }[vulnerability.status]
    : "";
  const vulnerabilitySummary = vulnerability
    ? vulnerability.status === "affected"
      ? `${vulnerability.critical} C · ${vulnerability.high} H · ${vulnerability.medium} M · ${vulnerability.low} L · ${vulnerability.unknown} U`
      : vulnerability.status === "clean"
        ? t("artifactDetail.noKnownVulnerabilities")
        : vulnerability.status === "error"
          ? t("artifactDetail.scanDidNotCompleteCheckTheJobDetails")
          : t("artifactDetail.noScanResultYet")
    : "";
  return (
    <Card
      size="small"
      title={t("artifactDetail.artifactIntelligence")}
      className="col-span-full border-line bg-surface-translucent"
      extra={<Tag>{format.toUpperCase()}</Tag>}
    >
      <div className="grid gap-4 lg:grid-cols-4">
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            {t("artifactDetail.signatures")}
          </div>
          <div className="text-sm text-fg">
            {metadata.signatures.length > 0
              ? t("artifactDetail.recordS", {
                  length: metadata.signatures.length,
                })
              : t("artifactDetail.noneLinked")}
          </div>
          {metadata.signatures.slice(0, 2).map((signature) => (
            <div
              key={`${signature.keyId}:${signature.signature}`}
              className="mt-1 truncate text-xs text-fg-tertiary"
              title={signature.identity}
            >
              {signature.verified ? "✓ " : "○ "}
              {signature.keyId} · {signature.algorithm}
            </div>
          ))}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            SBOM
          </div>
          <div className="text-sm text-fg">
            {metadata.sboms.length > 0
              ? t("artifactDetail.documentS", { length: metadata.sboms.length })
              : t("artifactDetail.noneLinked")}
          </div>
          {metadata.sboms.slice(0, 2).map((sbom) => (
            <div
              key={sbom.digest}
              className="mt-1 truncate font-mono text-xs text-fg-tertiary"
              title={sbom.digest}
            >
              {sbom.mediaType}
            </div>
          ))}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            {t("artifactDetail.provenance")}
          </div>
          {metadata.provenance ? (
            <>
              <div
                className="truncate text-sm text-fg"
                title={metadata.provenance.builder}
              >
                {metadata.provenance.builder}
              </div>
              <div
                className="mt-1 truncate font-mono text-xs text-fg-tertiary"
                title={metadata.provenance.sourceCommit}
              >
                {metadata.provenance.sourceCommit ||
                  metadata.provenance.buildId}
              </div>
            </>
          ) : (
            <div className="text-sm text-fg-tertiary">
              {t("artifactDetail.noneLinked")}
            </div>
          )}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            {t("artifactDetail.vulnerabilities")}
          </div>
          {vulnerability ? (
            <>
              <Tag color={vulnerabilityTone}>{vulnerabilityLabel}</Tag>
              <div className="mt-2 text-sm text-fg">{vulnerabilitySummary}</div>
              {vulnerability.scannedAt && (
                <div className="mt-1 text-xs text-fg-tertiary">
                  {formatDate(vulnerability.scannedAt, locale)}
                </div>
              )}
            </>
          ) : (
            <div className="text-sm text-fg-tertiary">
              {t("artifactDetail.notScanned")}
            </div>
          )}
        </div>
      </div>
      {vulnerability?.findings && vulnerability.findings.length > 0 && (
        <ArtifactVulnerabilityFindings findings={vulnerability.findings} />
      )}
      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-3 text-xs text-fg-tertiary">
        <span>{t("artifactDetail.licenses")}:</span>
        {metadata.licenses.length > 0 ? (
          metadata.licenses.map((license) => (
            <Tag key={license.spdxId}>{license.spdxId}</Tag>
          ))
        ) : (
          <span>{t("artifactDetail.noneLinked")}</span>
        )}
        <span className="ml-auto">
          {t("artifactDetail.updated")} {formatDate(metadata.updatedAt, locale)}{" "}
          · {metadata.updatedBy}
        </span>
      </div>
    </Card>
  );
}
