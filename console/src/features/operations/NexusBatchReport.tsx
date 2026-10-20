import { Button } from "antd";
import { useEffect, useRef, useState } from "react";
import type { NexusMigrationBatch, NexusMigrationEntry } from "../../client";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { publicRepositoryUsage } from "../../lib/publicRepositoryUsage";
import { batchError, observationReport } from "./nexusBatchModel";
import { useBatchTargets } from "./nexusBatchQueries";
import {
  isMigrationTargetFormat,
  migrationTargetURL,
} from "./migrationFormats";

export function NexusBatchReport({
  batch,
  complete,
  onRead,
}: {
  batch: NexusMigrationBatch;
  complete: boolean;
  onRead: () => Promise<{
    batch: NexusMigrationBatch;
    entries: NexusMigrationEntry[];
    complete: boolean;
    batchObservedAt: string;
    entriesObservedAt: string;
  }>;
}) {
  const { t } = usePreferences();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const alive = useRef(true);
  const pending = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const queries = useBatchTargets(
    batch.mappings.map((item) => item.targetRepositoryId),
  );
  const targets = queries.flatMap((query) =>
    query.data && !query.error && isMigrationTargetFormat(query.data.format)
      ? [query.data]
      : [],
  );
  const download = async () => {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError(null);
    try {
      // Export is a fresh read, not a serialization of an old checkpoint cache.
      const [current, refreshed] = await Promise.all([
        onRead(),
        Promise.all(queries.map((query) => query.refetch())),
      ]);
      for (const query of refreshed) if (query.error) throw query.error;
      const endpoints = refreshed.flatMap((query) =>
        query.data && isMigrationTargetFormat(query.data.format)
          ? [
              {
                repositoryId: query.data.id,
                name: query.data.name,
                format: query.data.format,
                url: migrationTargetURL(
                  query.data.format,
                  query.data.name,
                  window.location.origin,
                ),
              },
            ]
          : [],
      );
      if (!alive.current) return;
      const report = observationReport(
        current.batch,
        current.entries,
        current.complete,
        endpoints,
        {
          batchObservedAt: current.batchObservedAt,
          entriesObservedAt: current.entriesObservedAt,
        },
      );
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(report, null, 2)], {
          type: "application/json",
        }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = `nexus-migration-${batch.id}.json`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (failure) {
      if (alive.current) setError(batchError(failure));
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  };
  return (
    <Card className="ag-nexus-report">
      <div className="ag-migration-header">
        <h2>{t("nexusBatch.report")}</h2>
        <Button
          loading={busy}
          onClick={() => {
            void download();
          }}
        >
          {t("nexusBatch.export")}
        </Button>
      </div>
      <div className="ag-migration-content">
        {error !== null && <ErrorBanner error={error} />}
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
                format: target.format,
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
