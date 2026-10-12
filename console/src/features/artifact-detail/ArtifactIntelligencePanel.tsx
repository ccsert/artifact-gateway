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
  const { locale, text } = usePreferences();
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
      text("读取制品情报失败", "Failed to load artifact intelligence");
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
        affected: text("受影响", "Affected"),
        clean: text("未发现", "Clean"),
        error: text("扫描失败", "Scan failed"),
        not_scanned: text("未扫描", "Not scanned"),
      }[vulnerability.status]
    : "";
  const vulnerabilitySummary = vulnerability
    ? vulnerability.status === "affected"
      ? `${vulnerability.critical} C · ${vulnerability.high} H · ${vulnerability.medium} M · ${vulnerability.low} L · ${vulnerability.unknown} U`
      : vulnerability.status === "clean"
        ? text("未发现已知漏洞", "No known vulnerabilities")
        : vulnerability.status === "error"
          ? text(
              "扫描未成功，请检查任务详情",
              "Scan did not complete; check the job details",
            )
          : text("尚无扫描结果", "No scan result yet")
    : "";
  return (
    <Card
      size="small"
      title={text("制品情报", "Artifact intelligence")}
      className="col-span-full border-line bg-surface-translucent"
      extra={<Tag>{format.toUpperCase()}</Tag>}
    >
      <div className="grid gap-4 lg:grid-cols-4">
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            {text("签名", "Signatures")}
          </div>
          <div className="text-sm text-fg">
            {metadata.signatures.length > 0
              ? text(
                  `${metadata.signatures.length} 条记录`,
                  `${metadata.signatures.length} record(s)`,
                )
              : text("未关联", "None linked")}
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
              ? text(
                  `${metadata.sboms.length} 份清单`,
                  `${metadata.sboms.length} document(s)`,
                )
              : text("未关联", "None linked")}
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
            {text("构建来源", "Provenance")}
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
              {text("未关联", "None linked")}
            </div>
          )}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-fg-tertiary">
            {text("漏洞摘要", "Vulnerabilities")}
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
              {text("未扫描", "Not scanned")}
            </div>
          )}
        </div>
      </div>
      {vulnerability?.findings && vulnerability.findings.length > 0 && (
        <ArtifactVulnerabilityFindings findings={vulnerability.findings} />
      )}
      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-3 text-xs text-fg-tertiary">
        <span>{text("许可证", "Licenses")}:</span>
        {metadata.licenses.length > 0 ? (
          metadata.licenses.map((license) => (
            <Tag key={license.spdxId}>{license.spdxId}</Tag>
          ))
        ) : (
          <span>{text("未关联", "None linked")}</span>
        )}
        <span className="ml-auto">
          {text("更新于", "Updated")} {formatDate(metadata.updatedAt, locale)} ·{" "}
          {metadata.updatedBy}
        </span>
      </div>
    </Card>
  );
}
