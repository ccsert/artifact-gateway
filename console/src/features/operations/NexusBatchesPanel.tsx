import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "antd";
import { useSearchParams } from "react-router-dom";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, Pagination } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import {
  batchKeys,
  useBatch,
  useBatchCapabilities,
  useBatchPages,
} from "./nexusBatchQueries";
import { batchReadDenied, type BatchDraft } from "./nexusBatchModel";
import { NexusBatchStart } from "./NexusBatchStart";
import { NexusBatchDetail } from "./NexusBatchDetail";

export function NexusBatchesPanel({
  draft,
  onDraftConsumed,
}: {
  draft: BatchDraft | null;
  onDraftConsumed: () => void;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const [params, setParams] = useSearchParams();
  const id = params.get("batch") ?? "";
  const list = useBatchPages();
  const detail = useBatch(id);
  const capabilities = useBatchCapabilities();
  const [start, setStart] = useState<{ draft: BatchDraft | null } | null>(() =>
    draft ? { draft } : null,
  );
  const binding = !capabilities.error ? capabilities.data : undefined;
  const batches = batchReadDenied(list.error)
    ? null
    : (list.data?.pages.flatMap((page) => page.items) ?? null);
  const batch = batchReadDenied(detail.error) ? undefined : detail.data;
  const select = (batchId: string) => {
    const next = new URLSearchParams(params);
    next.set("batch", batchId);
    next.set("view", "batches");
    setParams(next);
  };
  return (
    <div
      className="ag-page-stack ag-migration-panel"
      data-testid="nexus-batches-panel"
    >
      <Notice
        tone="info"
        title={t("nexusBatch.background")}
        description={t("nexusBatch.backgroundHint")}
        closable={false}
      />
      {capabilities.error !== null && (
        <ErrorBanner
          error={capabilities.error}
          onRetry={() => {
            void capabilities.refetch();
          }}
        />
      )}
      {binding && !binding.executionEnabled && (
        <Notice
          tone="warning"
          title={t("nexusBatch.disabled")}
          description={t("nexusBatch.disabledHint")}
          closable={false}
        />
      )}
      {list.error !== null && (
        <ErrorBanner
          error={list.error}
          tone={batches ? "warning" : "error"}
          onRetry={() => {
            void list.refetch();
          }}
        />
      )}
      <Card>
        <div className="ag-migration-header">
          <h2>{t("nexusBatch.title")}</h2>
          <div className="ag-migration-actions">
            <Button
              loading={list.isFetching}
              onClick={() => {
                void list.refetch();
                void capabilities.refetch();
              }}
            >
              {t("nexusBatch.refresh")}
            </Button>
            <Button
              type="primary"
              disabled={!binding?.executionEnabled || Boolean(start)}
              onClick={() => setStart({ draft: null })}
            >
              {t("nexusBatch.new")}
            </Button>
          </div>
        </div>
        {batches === null ? (
          !list.error && <Loading />
        ) : batches.length === 0 ? (
          <EmptyState
            title={t("nexusBatch.empty")}
            hint={t("nexusBatch.emptyHint")}
          />
        ) : (
          <ul className="ag-migration-source-list">
            {batches.map((batch) => (
              <li key={batch.id}>
                <button
                  type="button"
                  className="ag-migration-source-choice"
                  aria-pressed={id === batch.id}
                  onClick={() => select(batch.id)}
                >
                  <span className="ag-migration-technical">{batch.id}</span>
                  <span className="text-xs text-fg-secondary">
                    {t(`nexusBatch.${batch.state}`)} ·{" "}
                    {batch.mappings
                      .map((item) => item.sourceRepository)
                      .join(", ")}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        <Pagination
          hasMore={list.hasNextPage}
          loading={list.isFetchingNextPage}
          label={t("nexusBatch.more")}
          onMore={() => {
            void list.fetchNextPage();
          }}
        />
      </Card>
      {start && binding?.executionEnabled && (
        <NexusBatchStart
          key={`${binding.targetId}:${binding.poolId}`}
          draft={start.draft}
          binding={binding}
          onClose={() => {
            setStart(null);
            onDraftConsumed();
          }}
          onCreated={(batch) => {
            setStart(null);
            onDraftConsumed();
            select(batch.id);
            void client.invalidateQueries({ queryKey: batchKeys.all });
          }}
        />
      )}
      {!id ? (
        <EmptyState compact title={t("nexusBatch.select")} />
      ) : (
        <>
          {detail.error !== null && (
            <ErrorBanner
              error={detail.error}
              tone={detail.data ? "warning" : "error"}
              onRetry={() => {
                void detail.refetch();
              }}
            />
          )}
          {!batch ? (
            !detail.error && <Loading />
          ) : (
            <NexusBatchDetail
              key={batch.id}
              batch={batch}
              binding={binding}
              refreshing={detail.isFetching}
              onRefresh={() => {
                void detail.refetch();
              }}
              onRescan={() =>
                setStart({
                  draft: {
                    sourceId: batch.sourceId,
                    mappings: batch.mappings,
                  },
                })
              }
            />
          )}
        </>
      )}
    </div>
  );
}
