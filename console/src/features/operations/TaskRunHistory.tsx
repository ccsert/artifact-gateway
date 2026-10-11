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
  const { locale, text } = usePreferences();
  const runsQuery = useTaskRuns(task.id);
  const runs = runsQuery.data ?? [];
  const loading = runsQuery.isFetching;
  const columns: ColumnsType<ScheduledTaskRun> = [
    {
      title: text("触发方式", "Trigger"),
      dataIndex: "trigger",
      width: 120,
      render: (value: ScheduledTaskRun["trigger"]) => (
        <span className="text-xs text-fg-secondary">
          {value === "manual"
            ? text("手动", "Manual")
            : text("计划", "Scheduled")}
        </span>
      ),
    },
    {
      title: text("投递状态", "Dispatch status"),
      dataIndex: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("触发时间", "Triggered"),
      dataIndex: "scheduledAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-secondary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("下游任务", "Downstream job"),
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
      title: text("失败原因", "Failure reason"),
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
          {text("投递历史", "Dispatch history")} · {task.name}
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
              title={text("尚无投递记录", "No dispatch history")}
              hint={text(
                "计划触发后，每次投递的结果都会记录在这里。",
                "Each dispatch is recorded here once the schedule fires.",
              )}
            />
          ),
        }}
        scroll={{ x: 980, y: 280 }}
      />
    </div>
  );
}
