import { HistoryOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import type { ScheduledTask, ScheduledTaskRun } from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner } from "../../components/ui/Feedback";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { useTaskRuns } from "./scheduledTaskQueries";

/** One task's dispatch history, read when its row is expanded. */
export function TaskRunHistory({ task }: { task: ScheduledTask }) {
  const { locale, t } = usePreferences();
  const runsQuery = useTaskRuns(task.id);
  const runs = runsQuery.data ?? [];
  const loading = runsQuery.isFetching;
  const columns: ColumnsType<ScheduledTaskRun> = [
    {
      title: t("operations.trigger"),
      dataIndex: "trigger",
      width: 120,
      render: (value: ScheduledTaskRun["trigger"]) => (
        <span className="text-xs text-fg-secondary">
          {value === "manual"
            ? t("operations.manual")
            : t("operations.scheduled")}
        </span>
      ),
    },
    {
      title: t("operations.dispatchStatus"),
      dataIndex: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: t("operations.triggered"),
      dataIndex: "scheduledAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-secondary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("operations.downstreamJob"),
      key: "target",
      width: 300,
      render: (_, run) => (
        <div className="min-w-0 font-mono text-xs text-fg-tertiary">
          <div>{run.targetKind ?? "—"}</div>
          {run.targetId && (
            <div className="mt-1 truncate" title={run.targetId}>
              {run.targetId}
            </div>
          )}
        </div>
      ),
    },
    {
      title: t("operations.failureReason"),
      dataIndex: "lastError",
      render: (value?: string) => (
        <span
          className="block truncate text-xs text-[var(--ag-status-danger)]"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
    },
  ];
  return (
    <div className="px-4 py-3">
      {runsQuery.error ? (
        <div className="mb-3">
          <ErrorBanner
            error={runsQuery.error}
            onRetry={() => void runsQuery.refetch()}
          />
        </div>
      ) : null}
      <div className="mb-3 flex items-center gap-2">
        <HistoryOutlined className="text-fg-tertiary!" />
        <span className="text-xs font-medium text-fg-secondary">
          {t("operations.dispatchHistory")} · {task.name}
        </span>
      </div>
      <ConsoleTable<ScheduledTaskRun>
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={runs}
        columns={columns}
        pagination={false}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={t("operations.noDispatchHistory")}
              hint={t("operations.eachDispatchIsRecordedHereOnceTheSchedule")}
            />
          ),
        }}
        scroll={{ x: 980, y: 280 }}
      />
    </div>
  );
}
