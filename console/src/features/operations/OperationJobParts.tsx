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
import {
  type JobAction,
  type Localize,
  type OperationRow,
} from "./operationModel";

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
  text,
}: {
  row: OperationRow;
  kindLabel: (kind: string) => string;
  text: Localize;
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
        title={row.repository ?? text("全局", "Global")}
      >
        {row.repository ?? text("全局", "Global")}
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
  text,
}: {
  row: OperationRow;
  text: Localize;
}) {
  const hasProgress = row.progressTotal !== undefined && row.progressTotal > 0;
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state={row.state} />
        <span className="text-xs text-fg-tertiary">
          {row.attempts === undefined
            ? text("未报告尝试次数", "Attempts not reported")
            : text(
                `${row.attempts} / ${row.maxAttempts} 次尝试`,
                `${row.attempts} / ${row.maxAttempts} attempts`,
              )}
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
  text,
}: {
  row: OperationRow;
  locale: string;
  text: Localize;
}) {
  const primaryLabel = row.nextAttemptAt
    ? text("下次执行", "Next run")
    : row.completedAt
      ? text("完成", "Completed")
      : text("创建", "Created");
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
          <span className="text-fg-disabled">{text("开始", "Started")}</span>
          <span className="ml-2 whitespace-nowrap">
            {formatDate(row.startedAt, locale)}
          </span>
        </div>
      )}
      {primaryTime !== row.createdAt && (
        <div>
          <span className="text-fg-disabled">{text("创建", "Created")}</span>
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
  text,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  acting: boolean;
  detailsOpen: boolean;
  text: Localize;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <Space size="small">
      <Tooltip
        title={text(
          detailsOpen ? "收起任务详情" : "查看任务详情",
          detailsOpen ? "Hide job details" : "View job details",
        )}
      >
        <Button
          aria-label={text("查看任务详情", "View job details")}
          aria-expanded={detailsOpen}
          type={detailsOpen ? "primary" : "default"}
          size="small"
          icon={<InfoCircleOutlined />}
          onClick={() => onInspect(row)}
        />
      </Tooltip>
      {row.repositoryId &&
        (row.state === "pending" || row.state === "retrying") && (
          <Tooltip
            title={text("立即加入执行队列", "Queue for immediate execution")}
          >
            <Button
              aria-label={text(
                "立即加入执行队列",
                "Queue for immediate execution",
              )}
              size="small"
              icon={<PlayCircleOutlined />}
              loading={acting}
              onClick={() => onAction(row, "run")}
            />
          </Tooltip>
        )}
      {row.repositoryId &&
        (row.state === "failed" || row.state === "cancelled") && (
          <Tooltip title={text("重新执行任务", "Retry job")}>
            <Button
              aria-label={text("重新执行任务", "Retry job")}
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
            title={text("取消此任务？", "Cancel this job?")}
            description={text(
              "取消后仍可从任务中心重新执行。",
              "You can retry it later from Operations.",
            )}
            okText={text("取消任务", "Cancel job")}
            cancelText={text("返回", "Back")}
            onConfirm={() => onAction(row, "cancel")}
          >
            <Tooltip title={text("取消任务", "Cancel job")}>
              <Button
                aria-label={text("取消任务", "Cancel job")}
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
  text,
}: {
  row: OperationRow;
  text: Localize;
}) {
  return (
    <div className="ag-operation-details">
      <dl className="ag-operation-details-meta">
        <div>
          <dt>{text("任务 ID", "Job ID")}</dt>
          <dd className="font-mono">{row.id}</dd>
        </div>
        <div>
          <dt>{text("仓库", "Repository")}</dt>
          <dd>{row.repository ?? text("全局任务", "Global job")}</dd>
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
          {text(
            "此任务没有报告额外的执行详情。",
            "This job did not report additional execution details.",
          )}
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
  text,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  locale: string;
  kindLabel: (kind: string) => string;
  acting: boolean;
  detailsOpen: boolean;
  text: Localize;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <article className="ag-operation-mobile-card">
      <OperationIdentity row={row} kindLabel={kindLabel} text={text} />
      <div className="mt-4 border-t border-line pt-4">
        <OperationStatus row={row} text={text} />
      </div>
      <div className="mt-4 border-t border-line pt-4">
        <OperationTimeline row={row} locale={locale} text={text} />
      </div>
      <div className="mt-4 flex justify-end border-t border-line pt-3">
        <OperationActions
          row={row}
          acting={acting}
          detailsOpen={detailsOpen}
          text={text}
          onInspect={onInspect}
          onAction={onAction}
        />
      </div>
      {detailsOpen && (
        <div className="mt-4 border-t border-line pt-4">
          <OperationDetailsPanel row={row} text={text} />
        </div>
      )}
    </article>
  );
}
