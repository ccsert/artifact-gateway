import { Button } from "antd";
import type { NexusMigrationBatch, NexusMigrationEntry } from "../../client";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { publicRepositoryUsage } from "../../lib/publicRepositoryUsage";
import { observationReport } from "./nexusBatchModel";
import { useBatchTargets } from "./nexusBatchQueries";

export function NexusBatchReport({
  batch,
  entries,
  complete,
}: {
  batch: NexusMigrationBatch;
  entries: NexusMigrationEntry[];
  complete: boolean;
}) {
  const { t } = usePreferences();
  const queries = useBatchTargets(
    batch.mappings.map((item) => item.targetRepositoryId),
  );
  const targets = queries.flatMap((query) =>
    query.data && !query.error && ["maven", "raw"].includes(query.data.format)
      ? [query.data]
      : [],
  );
  const endpoints = targets.map((target) => ({
    repositoryId: target.id,
    name: target.name,
    format: target.format,
    url: publicRepositoryUsage({
      format: target.format === "maven" ? "maven" : "raw",
      repositoryName: target.name,
      origin: window.location.origin,
      host: window.location.host,
      t,
    })[0].code,
  }));
  const download = () => {
    const report = observationReport(batch, entries, complete, endpoints);
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(report, null, 2)], { type: "application/json" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = `nexus-migration-${batch.id}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };
  return (
    <Card className="ag-nexus-report">
      <div className="ag-migration-header">
        <h2>{t("nexusBatch.report")}</h2>
        <Button onClick={download}>{t("nexusBatch.export")}</Button>
      </div>
      <div className="ag-migration-content">
        <p className="text-sm text-fg-secondary">
          {t("nexusBatch.reportHint")}
        </p>
        {!complete && (
          <Notice
            tone="warning"
            title={t("nexusBatch.inventoryIncomplete")}
            closable={false}
          />
        )}
        <dl className="ag-nexus-observations">
          <div>
            <dt>{t("nexusBatch.manifest")}</dt>
            <dd className="ag-migration-technical">
              {batch.manifestSha256 || "—"}
            </dd>
          </div>
        </dl>
        <ul className="ag-nexus-checks">
          <li>{t("nexusBatch.coldClient")}</li>
          <li>{t("nexusBatch.catchup")}</li>
        </ul>
        <section
          className="ag-migration-form"
          aria-label={t("nexusBatch.endpoints")}
        >
          <h3>{t("nexusBatch.endpoints")}</h3>
          <p className="text-sm text-fg-secondary">
            {t("nexusBatch.endpointsHint")}
          </p>
          {queries.some((query) => query.isPending) && <Loading />}
          {queries.some((query) => query.error) && (
            <ErrorBanner
              error={queries.find((query) => query.error)?.error}
              onRetry={() => {
                for (const query of queries) void query.refetch();
              }}
            />
          )}
          {targets.length !== queries.length &&
            !queries.some((query) => query.isPending) && (
              <p>{t("nexusBatch.targetUnavailable")}</p>
            )}
          {targets.map((target) => (
            <div className="ag-migration-form" key={target.id}>
              <h3 className="ag-migration-wrap">
                {target.name} · {target.format}
              </h3>
              {publicRepositoryUsage({
                format: target.format === "maven" ? "maven" : "raw",
                repositoryName: target.name,
                origin: window.location.origin,
                host: window.location.host,
                t,
              }).map((snippet) => (
                <div key={snippet.label} className="min-w-0">
                  <p className="text-xs text-fg-secondary">{snippet.label}</p>
                  <pre className="ag-nexus-snippet">{snippet.code}</pre>
                </div>
              ))}
            </div>
          ))}
        </section>
        <Notice
          tone="warning"
          title={t("nexusBatch.residual")}
          description={t("nexusBatch.residualHint")}
          closable={false}
        />
        <Notice
          tone="info"
          title={t("nexusBatch.cutover")}
          description={t("nexusBatch.cutoverHint")}
          closable={false}
        />
      </div>
    </Card>
  );
}
