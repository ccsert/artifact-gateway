import { useEffect, useState } from "react";
import { Alert, Card, Tag } from "antd";
import { getArtifactIntelligence } from "../client";
import type { ArtifactIntelligence } from "../client";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { isNotFound } from "./Feedback";
import { ArtifactVulnerabilityFindings } from "./ArtifactVulnerabilityFindings";

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
  const [metadata, setMetadata] = useState<ArtifactIntelligence | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!repositoryId || !digest || !coordinate) {
      setMetadata(null);
      setError(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    void getArtifactIntelligence({
      path: { repositoryId },
      query: { coordinate, digest },
    }).then((response) => {
      if (cancelled) return;
      setLoading(false);
      if (response.error || !response.data) {
        setMetadata(null);
        if (response.error && !isNotFound(response.error))
          setError(response.error);
        return;
      }
      setMetadata(response.data);
    });
    return () => {
      cancelled = true;
    };
  }, [coordinate, digest, repositoryId]);

  if (loading) return null;
  if (error) {
    const message =
      (error as { message?: string })?.message ??
      text("读取制品情报失败", "Failed to load artifact intelligence");
    return (
      <Alert
        type="warning"
        showIcon
        title={message}
        className="col-span-full"
      />
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
      className="col-span-full border-zinc-800/90 bg-zinc-950/20"
      extra={<Tag>{format.toUpperCase()}</Tag>}
    >
      <div className="grid gap-4 lg:grid-cols-4">
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
            {text("签名", "Signatures")}
          </div>
          <div className="text-sm text-zinc-200">
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
              className="mt-1 truncate text-xs text-zinc-500"
              title={signature.identity}
            >
              {signature.verified ? "✓ " : "○ "}
              {signature.keyId} · {signature.algorithm}
            </div>
          ))}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
            SBOM
          </div>
          <div className="text-sm text-zinc-200">
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
              className="mt-1 truncate font-mono text-xs text-zinc-500"
              title={sbom.digest}
            >
              {sbom.mediaType}
            </div>
          ))}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
            {text("构建来源", "Provenance")}
          </div>
          {metadata.provenance ? (
            <>
              <div
                className="truncate text-sm text-zinc-200"
                title={metadata.provenance.builder}
              >
                {metadata.provenance.builder}
              </div>
              <div
                className="mt-1 truncate font-mono text-xs text-zinc-500"
                title={metadata.provenance.sourceCommit}
              >
                {metadata.provenance.sourceCommit ||
                  metadata.provenance.buildId}
              </div>
            </>
          ) : (
            <div className="text-sm text-zinc-500">
              {text("未关联", "None linked")}
            </div>
          )}
        </div>
        <div>
          <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
            {text("漏洞摘要", "Vulnerabilities")}
          </div>
          {vulnerability ? (
            <>
              <Tag color={vulnerabilityTone}>{vulnerabilityLabel}</Tag>
              <div className="mt-2 text-sm text-zinc-200">
                {vulnerabilitySummary}
              </div>
              {vulnerability.scannedAt && (
                <div className="mt-1 text-xs text-zinc-500">
                  {formatDate(vulnerability.scannedAt, locale)}
                </div>
              )}
            </>
          ) : (
            <div className="text-sm text-zinc-500">
              {text("未扫描", "Not scanned")}
            </div>
          )}
        </div>
      </div>
      {vulnerability?.findings && vulnerability.findings.length > 0 && (
        <ArtifactVulnerabilityFindings findings={vulnerability.findings} />
      )}
      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-zinc-800/80 pt-3 text-xs text-zinc-500">
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
