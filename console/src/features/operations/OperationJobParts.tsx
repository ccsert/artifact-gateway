import type { Translate } from "../../lib/preferences";
import { useEffect, useState } from "react";
import {
  CloseOutlined,
  InfoCircleOutlined,
  PlayCircleOutlined,
  RedoOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { Button, Popconfirm, Progress, Space, Tooltip } from "antd";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { LifecycleJobDetails } from "./LifecycleJobDetails";
import { type JobAction, type OperationRow } from "./operationModel";

export function useCompactOperationLayout() {
  const [compact, setCompact] = useState(
    () => window.matchMedia("(max-width: 1120px)").matches,
  );

  useEffect(() => {
    const media = window.matchMedia("(max-width: 1120px)");
    const update = () => setCompact(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return compact;
}

export function OperationIdentity({
  row,
  kindLabel,
  t,
}: {
  row: OperationRow;
  kindLabel: (kind: string) => string;
  t: Translate;
}) {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2 text-sm font-medium text-fg">
        <SyncOutlined className="shrink-0 text-fg-tertiary!" />
        <span className="truncate" title={kindLabel(row.kind)}>
          {kindLabel(row.kind)}
        </span>
      </div>
      <div
        className="mt-1 truncate text-xs text-fg-secondary"
        title={row.repository ?? t("operations.global")}
      >
        {row.repository ?? t("operations.global")}
      </div>
      <div
        className="mt-1 truncate font-mono text-xs text-fg-disabled"
        title={row.id}
      >
        {row.id}
      </div>
    </div>
  );
}

export function OperationStatus({
  row,
  t,
}: {
  row: OperationRow;
  t: Translate;
}) {
  const hasProgress = row.progressTotal !== undefined && row.progressTotal > 0;
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state={row.state} />
        <span className="text-xs text-fg-tertiary">
          {row.attempts === undefined
            ? t("operations.attemptsNotReported")
            : t("operations.attempts", {
                attempts: row.attempts,
                maxAttempts: String(row.maxAttempts),
              })}
        </span>
      </div>
      {hasProgress ? (
        <Progress
          className="mt-2"
          percent={Math.round(
            ((row.progressCurrent ?? 0) / (row.progressTotal ?? 1)) * 100,
          )}
          size="small"
          status={
            row.state === "failed"
              ? "exception"
              : row.state === "completed"
                ? "success"
                : "normal"
          }
          format={() => `${row.progressCurrent ?? 0}/${row.progressTotal}`}
        />
      ) : row.progressMessage ? (
        <div className="mt-2 line-clamp-2 text-xs leading-5 text-fg-tertiary">
          {row.progressMessage}
        </div>
      ) : null}
      {hasProgress && row.progressMessage && (
        <div className="mt-1 line-clamp-2 text-xs leading-4 text-fg-tertiary">
          {row.progressMessage}
        </div>
      )}
      {row.lastError && (
        <div className="mt-2 line-clamp-2 break-words text-xs leading-5 text-[var(--ag-status-danger)]">
          {row.lastError}
        </div>
      )}
    </div>
  );
}

export function OperationTimeline({
  row,
  locale,
  t,
}: {
  row: OperationRow;
  locale: string;
  t: Translate;
}) {
  const primaryLabel = row.nextAttemptAt
    ? t("operations.nextRun")
    : row.completedAt
      ? t("operations.completed")
      : t("operations.created");
  const primaryTime = row.nextAttemptAt ?? row.completedAt ?? row.createdAt;
  return (
    <div className="space-y-1 text-xs text-fg-tertiary">
      <div>
        <span className="text-fg-disabled">{primaryLabel}</span>
        <span className="ml-2 whitespace-nowrap">
          {formatDate(primaryTime, locale)}
        </span>
      </div>
      {row.startedAt && (
        <div>
          <span className="text-fg-disabled">{t("operations.started")}</span>
          <span className="ml-2 whitespace-nowrap">
            {formatDate(row.startedAt, locale)}
          </span>
        </div>
      )}
      {primaryTime !== row.createdAt && (
        <div>
          <span className="text-fg-disabled">{t("operations.created")}</span>
          <span className="ml-2 whitespace-nowrap">
            {formatDate(row.createdAt, locale)}
          </span>
        </div>
      )}
    </div>
  );
}

export function OperationActions({
  row,
  acting,
  detailsOpen,
  t,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  acting: boolean;
  detailsOpen: boolean;
  t: Translate;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <Space size="small">
      <Tooltip
        title={
          detailsOpen
            ? t("operations.hideJobDetails")
            : t("operations.viewJobDetails")
        }
      >
        <Button
          aria-label={t("operations.viewJobDetails")}
          aria-expanded={detailsOpen}
          type={detailsOpen ? "primary" : "default"}
          size="small"
          icon={<InfoCircleOutlined />}
          onClick={() => onInspect(row)}
        />
      </Tooltip>
      {row.repositoryId &&
        (row.state === "pending" || row.state === "retrying") && (
          <Tooltip title={t("operations.queueForImmediateExecution")}>
            <Button
              aria-label={t("operations.queueForImmediateExecution")}
              size="small"
              icon={<PlayCircleOutlined />}
              loading={acting}
              onClick={() => onAction(row, "run")}
            />
          </Tooltip>
        )}
      {row.repositoryId &&
        (row.state === "failed" || row.state === "cancelled") && (
          <Tooltip title={t("operations.retryJob")}>
            <Button
              aria-label={t("operations.retryJob")}
              size="small"
              icon={<RedoOutlined />}
              loading={acting}
              onClick={() => onAction(row, "retry")}
            />
          </Tooltip>
        )}
      {row.repositoryId &&
        (row.state === "pending" || row.state === "retrying") && (
          <Popconfirm
            title={t("operations.cancelThisJob")}
            description={t("operations.youCanRetryItLaterFromOperations")}
            okText={t("operations.cancelJob")}
            cancelText={t("operations.back")}
            onConfirm={() => onAction(row, "cancel")}
          >
            <Tooltip title={t("operations.cancelJob")}>
              <Button
                aria-label={t("operations.cancelJob")}
                danger
                size="small"
                icon={<CloseOutlined />}
                loading={acting}
              />
            </Tooltip>
          </Popconfirm>
        )}
    </Space>
  );
}

export function OperationDetailsPanel({
  row,
  t,
}: {
  row: OperationRow;
  t: Translate;
}) {
  return (
    <div className="ag-operation-details">
      <dl className="ag-operation-details-meta">
        <div>
          <dt>{t("operations.jobID")}</dt>
          <dd className="font-mono">{row.id}</dd>
        </div>
        <div>
          <dt>{t("operations.repository")}</dt>
          <dd>{row.repository ?? t("operations.globalJob")}</dd>
        </div>
      </dl>
      {row.details && <LifecycleJobDetails details={row.details} />}
      {row.lastError && (
        <div className="break-words rounded-md border border-[var(--ag-status-danger-border)] bg-[var(--ag-status-danger-soft)] px-4 py-2 text-xs leading-5 text-[var(--ag-status-danger)]">
          {row.lastError}
        </div>
      )}
      {!row.details && !row.lastError && (
        <p className="text-xs leading-5 text-fg-tertiary">
          {t("operations.thisJobDidNotReportAdditionalExecutionDetails")}
        </p>
      )}
    </div>
  );
}

export function OperationMobileCard({
  row,
  locale,
  kindLabel,
  acting,
  detailsOpen,
  t,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  locale: string;
  kindLabel: (kind: string) => string;
  acting: boolean;
  detailsOpen: boolean;
  t: Translate;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <article className="ag-operation-mobile-card">
      <OperationIdentity row={row} kindLabel={kindLabel} t={t} />
      <div className="mt-4 border-t border-line pt-4">
        <OperationStatus row={row} t={t} />
      </div>
      <div className="mt-4 border-t border-line pt-4">
        <OperationTimeline row={row} locale={locale} t={t} />
      </div>
      <div className="mt-4 flex justify-end border-t border-line pt-3">
        <OperationActions
          row={row}
          acting={acting}
          detailsOpen={detailsOpen}
          t={t}
          onInspect={onInspect}
          onAction={onAction}
        />
      </div>
      {detailsOpen && (
        <div className="mt-4 border-t border-line pt-4">
          <OperationDetailsPanel row={row} t={t} />
        </div>
      )}
    </article>
  );
}
