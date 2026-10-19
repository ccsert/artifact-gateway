import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, Tag } from "antd";
import type {
  NexusMigrationBatch,
  NexusMigrationCapabilities,
  NexusMigrationEntry,
} from "../../client";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, Pagination } from "../../components/ui/Layout";
import { formatBytes, formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { LocalizedError } from "../../messages/translate";
import {
  batchReason,
  batchReadDenied,
  batchDiagnosticCode,
  batchActive,
  entryState,
  hasExceptions,
  inventoryComplete,
  scanStage,
} from "./nexusBatchModel";
import { batchOptions, useEntryPages } from "./nexusBatchQueries";
import { NexusBatchControls } from "./NexusBatchControls";
import { NexusBatchReport } from "./NexusBatchReport";

export function NexusBatchDetail({
  batch,
  binding,
  onRefresh,
  refreshing,
  onRescan,
}: {
  batch: NexusMigrationBatch;
  binding: NexusMigrationCapabilities | undefined;
  onRefresh: () => void;
  refreshing: boolean;
  onRescan: () => void;
}) {
  const { t, locale } = usePreferences();
  const client = useQueryClient();
  const pages = useEntryPages(batch.id, batch.version, batchActive(batch));
  const entries = batchReadDenied(pages.error)
    ? null
    : (pages.data?.pages.flatMap((page) => page.items) ?? null);
  const lastCursor = pages.data?.pages.at(-1)?.nextCursor ?? "";
  const complete =
    entries !== null &&
    !pages.error &&
    inventoryComplete(batch, entries, Boolean(lastCursor));
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<unknown>(null);
  const epoch = useRef(0);
  useEffect(
    () => () => {
      epoch.current += 1;
    },
    [],
  );
  const readAll = async () => {
    if (reading) return;
    const current = ++epoch.current;
    setReading(true);
    setReadError(null);
    try {
      // Each explicit next page still belongs to the query cache and AbortSignal.
      // A bounded loop refuses cycling cursors rather than calling it complete.
      for (let page = 0; page < 1000 && epoch.current === current; page++) {
        const result = await pages.fetchNextPage();
        if (result.error) throw result.error;
        const all = result.data?.pages ?? [];
        const cursor = all.at(-1)?.nextCursor;
        if (!cursor) return;
        if (!result.hasNextPage)
          throw new LocalizedError("nexusBatch.pageFailure");
      }
      if (epoch.current === current)
        throw new LocalizedError("nexusBatch.pageFailure");
    } catch (error) {
      if (epoch.current === current) setReadError(error);
    } finally {
      if (epoch.current === current) setReading(false);
    }
  };
  const countItems = [
    ["pending", "pending"],
    ["uploaded", "uploaded"],
    ["verified", "entryVerified"],
    ["committed", "committed"],
    ["skipped", "skipped"],
    ["conflict", "conflict"],
    ["failed", "entryFailed"],
    ["unsupported", "unsupported"],
    ["sourceMissing", "sourceMissing"],
    ["sourceDeleted", "sourceDeleted"],
  ] as const;
  const stats = [
    { key: "scanned", value: batch.metadataDiscovered },
    { key: "planned", value: batch.entryCount },
    {
      key: "total",
      value:
        batch.totalBytes === null
          ? t("nexusBatch.unknown")
          : formatBytes(batch.totalBytes),
      bytes: batch.totalBytes,
    },
    {
      key: "sourceVerification",
      value: formatBytes(batch.sourceVerificationBytes),
      bytes: batch.sourceVerificationBytes,
      hint: "sourceVerificationHint",
    },
    {
      key: "transferred",
      value: formatBytes(batch.transferredBytes),
      bytes: batch.transferredBytes,
      hint: "transferredHint",
    },
    {
      key: "verified",
      value: formatBytes(batch.verifiedBytes),
      bytes: batch.verifiedBytes,
    },
    { key: "visible", value: batch.visibleIdentities },
  ] as const;
  return (
    <div className="ag-page-stack" data-testid="nexus-batch-detail">
      <Card>
        <div className="ag-migration-header">
          <div className="min-w-0">
            <h2 className="ag-migration-wrap">{batch.id}</h2>
            <p>
              <Tag>{t(`nexusBatch.${batch.state}`)}</Tag>
            </p>
            <p className="ag-migration-technical">
              {t("nexusBatch.version", { version: batch.version })}
            </p>
            <p className="text-xs text-fg-secondary">
              {t("nexusBatch.updated", {
                time: formatDate(batch.updatedAt, locale),
              })}
            </p>
          </div>
          <Button
            loading={refreshing || pages.isRefetching}
            onClick={() => {
              onRefresh();
              void pages.refetch();
            }}
          >
            {t("nexusBatch.refresh")}
          </Button>
        </div>
        <div className="ag-migration-content">
          {batch.lastError && (
            <Notice
              tone="warning"
              title={batchReason(batch.lastError, t)}
              closable={false}
            />
          )}
          {hasExceptions(batch) && (
            <Notice
              tone="warning"
              title={t("nexusBatch.partial")}
              closable={false}
            />
          )}
          <dl className="ag-nexus-observations ag-nexus-stat-grid">
            {stats.map((stat) => (
              <div key={stat.key}>
                <dt>{t(`nexusBatch.${stat.key}`)}</dt>
                <dd
                  title={
                    "bytes" in stat && stat.bytes !== null
                      ? `${stat.bytes} B`
                      : undefined
                  }
                >
                  {stat.value}
                </dd>
                {"hint" in stat && (
                  <p className="text-xs text-fg-secondary">
                    {t(`nexusBatch.${stat.hint}`)}
                  </p>
                )}
              </div>
            ))}
          </dl>
          <ul className="ag-nexus-status-counts">
            {countItems.map(([key, label]) => (
              <li key={key}>
                <span>{t(`nexusBatch.${label}`)}</span>
                <strong>{batch.counts[key]}</strong>
              </li>
            ))}
          </ul>
          <NexusBatchControls
            key={`${binding?.targetId ?? ""}:${binding?.poolId ?? ""}`}
            batch={batch}
            binding={binding}
            onRescan={onRescan}
          />
        </div>
      </Card>
      <Card>
        <div className="ag-migration-header">
          <h2>{t("nexusBatch.entries")}</h2>
          <div className="ag-migration-actions">
            <span className="text-xs text-fg-secondary">
              {t("nexusBatch.loaded", {
                loaded: entries?.length ?? 0,
                total: batch.entryCount,
              })}
            </span>
            {pages.hasNextPage && (
              <Button
                loading={reading}
                disabled={pages.isFetching}
                onClick={() => {
                  void readAll();
                }}
              >
                {t("nexusBatch.allEntries")}
              </Button>
            )}
          </div>
        </div>
        {(pages.error !== null || readError !== null) && (
          <div className="ag-migration-content">
            <ErrorBanner
              error={readError ?? pages.error}
              tone={entries ? "warning" : "error"}
              onRetry={() => {
                setReadError(null);
                void pages.refetch();
              }}
            />
          </div>
        )}
        {batch.lastScanFailure && (
          <section
            className="ag-migration-form"
            aria-label={t("nexusBatch.scanFailure")}
          >
            <h3>{t("nexusBatch.scanFailure")}</h3>
            <p className="text-sm text-fg-secondary">
              {batchReason(batch.lastScanFailure.code, t)}
            </p>
            <dl className="ag-nexus-observations">
              <div>
                <dt>{t("nexusBatch.scanStageLabel")}</dt>
                <dd>{t(scanStage(batch.lastScanFailure.stage))}</dd>
              </div>
              <div>
                <dt>{t("nexusBatch.scanCode")}</dt>
                <dd className="ag-migration-technical">
                  {batchDiagnosticCode(batch.lastScanFailure.code)}
                </dd>
              </div>
              <div>
                <dt>{t("nexusBatch.scanScope")}</dt>
                <dd className="ag-migration-technical">
                  {batch.lastScanFailure.sourceRepositories.join(", ")}
                </dd>
              </div>
              <div>
                <dt>{t("migration.sourceRepository")}</dt>
                <dd className="ag-migration-technical">
                  {batch.lastScanFailure.sourceRepository || "—"}
                </dd>
              </div>
              <div>
                <dt>{t("nexusBatch.sourcePath")}</dt>
                <dd className="ag-migration-technical">
                  {batch.lastScanFailure.sourcePath || "—"}
                </dd>
              </div>
              <div>
                <dt>{t("nexusBatch.entry")}</dt>
                <dd className="ag-migration-technical">
                  {batch.lastScanFailure.identity || "—"}
                </dd>
              </div>
              <div>
                <dt>{t("nexusBatch.scanOccurred")}</dt>
                <dd>{formatDate(batch.lastScanFailure.occurredAt, locale)}</dd>
              </div>
            </dl>
          </section>
        )}
        {entries === null ? (
          !pages.error && <Loading />
        ) : entries.length === 0 ? (
          <EmptyState
            title={t("nexusBatch.noEntries")}
            hint={t("nexusBatch.noEntriesHint")}
          />
        ) : (
          <ConsoleTable<NexusMigrationEntry>
            dataSource={entries}
            rowKey="id"
            tableLayout="fixed"
            size="small"
            columns={[
              {
                title: t("nexusBatch.entry"),
                dataIndex: "identity",
                render: (identity: string, entry) => (
                  <div className="ag-migration-wrap">
                    <span className="font-mono text-xs">{identity}</span>
                    <p className="text-xs text-fg-secondary">
                      {entry.sourceRepository} · {entry.format}
                    </p>
                    <p className="text-xs text-fg-secondary">
                      {t("nexusBatch.attempts", {
                        count: entry.attempts,
                        max: binding?.maxAttempts ?? 3,
                      })}
                    </p>
                  </div>
                ),
              },
              {
                title: t("nexusBatch.state"),
                render: (_, entry) => (
                  <div className="ag-migration-wrap">
                    <span>{t(entryState(entry.state))}</span>
                    {entry.lastError && (
                      <p className="text-xs text-fg-secondary">
                        {batchReason(entry.lastError, t)}
                      </p>
                    )}
                  </div>
                ),
              },
            ]}
            expandable={{
              expandedRowRender: (entry) => (
                <div className="ag-migration-form">
                  {entry.assets.map((asset) => (
                    <dl
                      key={asset.targetPath}
                      className="ag-nexus-observations"
                    >
                      <div>
                        <dt>{t("nexusBatch.sourcePath")}</dt>
                        <dd className="ag-migration-technical">
                          {asset.sourcePath}
                        </dd>
                      </div>
                      <div>
                        <dt>{t("nexusBatch.targetPath")}</dt>
                        <dd className="ag-migration-technical">
                          {asset.targetPath}
                        </dd>
                      </div>
                      <div>
                        <dt>{t("nexusBatch.sha256")}</dt>
                        <dd className="ag-migration-technical">
                          {asset.sha256 || "—"}
                        </dd>
                      </div>
                      <div>
                        <dt>{t("migration.size")}</dt>
                        <dd>{asset.size} B</dd>
                      </div>
                      <div>
                        <dt>{t("nexusBatch.asset")}</dt>
                        <dd>
                          {t(
                            asset.verified
                              ? "nexusBatch.assetVerified"
                              : "nexusBatch.assetUnverified",
                          )}
                          {asset.uploaded && (
                            <> · {t("nexusBatch.assetUploaded")}</>
                          )}
                        </dd>
                      </div>
                    </dl>
                  ))}
                </div>
              ),
            }}
          />
        )}
        <Pagination
          hasMore={pages.hasNextPage}
          loading={pages.isFetchingNextPage}
          disabled={reading}
          onMore={() => {
            void pages.fetchNextPage();
          }}
          label={t("nexusBatch.moreEntries")}
        />
      </Card>
      <NexusBatchReport
        batch={batch}
        complete={complete}
        onRead={async () => {
          const [current, inventory] = await Promise.all([
            client.fetchQuery(batchOptions(batch.id)),
            pages.refetch(),
          ]);
          if (inventory.error) throw inventory.error;
          const entries =
            inventory.data?.pages.flatMap((page) => page.items) ?? [];
          return {
            batch: current,
            entries,
            complete: inventoryComplete(
              current,
              entries,
              Boolean(inventory.data?.pages.at(-1)?.nextCursor),
            ),
            batchObservedAt: new Date(
              client.getQueryState(batchOptions(batch.id).queryKey)
                ?.dataUpdatedAt ?? Date.now(),
            ).toISOString(),
            entriesObservedAt: new Date(inventory.dataUpdatedAt).toISOString(),
          };
        }}
      />
    </div>
  );
}
