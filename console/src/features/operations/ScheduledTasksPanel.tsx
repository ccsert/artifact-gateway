import { useMemo, useState } from "react";
import {
  DeleteOutlined,
  EditOutlined,
  HistoryOutlined,
  PlusOutlined,
  ReloadOutlined,
  RocketOutlined,
} from "@ant-design/icons";
import { Button, Popconfirm, Space, Switch, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  deleteScheduledTask,
  runScheduledTask,
  updateScheduledTask,
} from "../../client";
import type { ScheduledTask } from "../../client";
import { Badge, StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { formatTaskInterval } from "./scheduledTaskModel";
import {
  useInvalidateScheduledTasks,
  useRetentionRepositories,
  useScheduledTasks,
} from "./scheduledTaskQueries";
import { TaskEditorDialog } from "./TaskEditorDialog";
import { TaskRunHistory } from "./TaskRunHistory";

/** Recurring retention dispatches, with each task's dispatch history. */
export function ScheduledTasksPanel() {
  const { locale, t } = usePreferences();
  const tasksQuery = useScheduledTasks();
  const repositoriesQuery = useRetentionRepositories();
  const tasks = tasksQuery.data ?? null;
  const repositories = useMemo(
    () => repositoriesQuery.data ?? [],
    [repositoriesQuery.data],
  );
  const loading = tasksQuery.isFetching || repositoriesQuery.isFetching;
  const invalidate = useInvalidateScheduledTasks();
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? tasksQuery.error ?? repositoriesQuery.error;
  const [busyId, setBusyId] = useState<string | null>(null);
  const [expandedTask, setExpandedTask] = useState<string | null>(null);
  // Each opening gets a new session so the dialog starts from its task while
  // a closing dialog keeps its content through the exit animation.
  const [editor, setEditor] = useState<{
    session: number;
    open: boolean;
    task: ScheduledTask | null;
  } | null>(null);

  const load = () => {
    setActionError(null);
    return Promise.all([tasksQuery.refetch(), repositoriesQuery.refetch()]);
  };

  const repositoryById = useMemo(
    () => new Map(repositories.map((repo) => [repo.id, repo])),
    [repositories],
  );

  const openEditor = (task: ScheduledTask | null) => {
    setActionError(null);
    setEditor((current) => ({
      session: (current?.session ?? 0) + 1,
      open: true,
      task,
    }));
  };
  const openCreate = () => openEditor(null);
  const openEdit = (task: ScheduledTask) => openEditor(task);

  const updateEnabled = async (task: ScheduledTask, enabled: boolean) => {
    setBusyId(task.id);
    setActionError(null);
    try {
      const result = await updateScheduledTask({
        path: { taskId: task.id },
        headers: { "If-Match": task.version },
        body: {
          name: task.name,
          description: task.description,
          kind: task.kind,
          repositoryId: task.repositoryId,
          intervalMinutes: task.intervalMinutes,
          enabled,
        },
      });
      if (result.error) {
        setActionError(result.error);
        return;
      }
      await invalidate();
    } catch (nextError) {
      setActionError(nextError);
    } finally {
      setBusyId(null);
    }
  };

  const runNow = async (task: ScheduledTask) => {
    setBusyId(task.id);
    setActionError(null);
    try {
      const result = await runScheduledTask({ path: { taskId: task.id } });
      const dispatchError = result.error;
      await invalidate(task.id);
      if (dispatchError) setActionError(dispatchError);
    } catch (nextError) {
      setActionError(nextError);
    } finally {
      setBusyId(null);
    }
  };

  const remove = async (task: ScheduledTask) => {
    setBusyId(task.id);
    setActionError(null);
    try {
      const result = await deleteScheduledTask({ path: { taskId: task.id } });
      if (result.error) {
        setActionError(result.error);
        return;
      }
      if (expandedTask === task.id) setExpandedTask(null);
      await invalidate();
    } catch (nextError) {
      setActionError(nextError);
    } finally {
      setBusyId(null);
    }
  };

  const toggleHistory = (task: ScheduledTask) =>
    setExpandedTask((current) => (current === task.id ? null : task.id));

  const columns: ColumnsType<ScheduledTask> = [
    {
      title: t("operations.task"),
      key: "task",
      width: 260,
      render: (_, task) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-fg-strong">
            {task.name}
          </div>
          <div
            className="mt-1 truncate text-xs text-fg-tertiary"
            title={task.description}
          >
            {task.description || t("operations.noDescription")}
          </div>
        </div>
      ),
    },
    {
      title: t("operations.action"),
      dataIndex: "kind",
      key: "kind",
      width: 170,
      render: (kind: ScheduledTask["kind"]) => (
        <Badge
          tone={
            kind === "audit-retention" ? "visualization-6" : "visualization-2"
          }
        >
          {kind === "audit-retention"
            ? t("operations.auditRetention")
            : t("operations.repositoryRetention")}
        </Badge>
      ),
    },
    {
      title: t("operations.target"),
      key: "target",
      width: 180,
      render: (_, task) => {
        const repository = task.repositoryId
          ? repositoryById.get(task.repositoryId)
          : undefined;
        return (
          <div className="text-xs text-fg-secondary">
            {repository?.name ?? t("operations.globalAuditLog")}
            {repository && (
              <div className="mt-1 font-mono text-xs text-fg-disabled">
                {repository.format}
              </div>
            )}
          </div>
        );
      },
    },
    {
      title: t("operations.interval"),
      dataIndex: "intervalMinutes",
      key: "interval",
      width: 130,
      render: (minutes: number) => (
        <span className="whitespace-nowrap text-xs text-fg-secondary">
          {formatTaskInterval(minutes, t)}
        </span>
      ),
    },
    {
      title: t("operations.nextRun"),
      dataIndex: "nextRunAt",
      key: "nextRunAt",
      width: 180,
      render: (value: string, task) => (
        <div className="whitespace-nowrap text-xs text-fg-secondary">
          {task.enabled ? formatDate(value, locale) : "—"}
        </div>
      ),
    },
    {
      title: t("operations.lastDispatch"),
      key: "lastRun",
      width: 180,
      render: (_, task) => (
        <div className="text-xs text-fg-secondary">
          {task.lastRunState ? (
            <StateBadge state={task.lastRunState} />
          ) : (
            <span className="text-fg-disabled">—</span>
          )}
          {task.lastRunAt && (
            <div className="mt-1 whitespace-nowrap text-xs text-fg-disabled">
              {formatDate(task.lastRunAt, locale)}
            </div>
          )}
        </div>
      ),
    },
    {
      title: t("operations.enabled2"),
      dataIndex: "enabled",
      key: "enabled",
      width: 90,
      render: (enabled: boolean, task) => (
        <Switch
          size="small"
          value={enabled}
          loading={busyId === task.id}
          aria-label={t("operations.enableScheduledTask")}
          onChange={(value) => void updateEnabled(task, value)}
        />
      ),
    },
    {
      title: t("operations.actions"),
      key: "actions",
      fixed: "right",
      width: 170,
      render: (_, task) => (
        <Space size="small">
          <Tooltip title={t("operations.runNow")}>
            <Button
              size="small"
              icon={<RocketOutlined />}
              aria-label={t("operations.runNow")}
              loading={busyId === task.id}
              onClick={() => void runNow(task)}
            />
          </Tooltip>
          <Tooltip title={t("operations.dispatchHistory")}>
            <Button
              size="small"
              type={expandedTask === task.id ? "primary" : "default"}
              icon={<HistoryOutlined />}
              aria-label={t("operations.dispatchHistory")}
              onClick={() => toggleHistory(task)}
            />
          </Tooltip>
          <Tooltip title={t("operations.edit")}>
            <Button
              size="small"
              icon={<EditOutlined />}
              aria-label={t("operations.edit")}
              onClick={() => openEdit(task)}
            />
          </Tooltip>
          <Popconfirm
            title={t("operations.deleteThisScheduledTask")}
            description={t(
              "operations.dispatchHistoryIsRemovedAlreadySubmittedJobsAre",
            )}
            okText={t("operations.delete")}
            cancelText={t("operations.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => remove(task)}
          >
            <Tooltip title={t("operations.delete")}>
              <Button
                size="small"
                danger
                icon={<DeleteOutlined />}
                aria-label={t("operations.delete")}
                loading={busyId === task.id}
              />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      {error ? <ErrorBanner error={error} onRetry={load} /> : null}
      <Card>
        <CardHeader
          title={t("operations.scheduledTasks")}
          extra={
            <Space>
              <Button
                icon={<ReloadOutlined />}
                loading={loading}
                onClick={() => void load()}
              >
                {t("operations.refresh")}
              </Button>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                onClick={openCreate}
              >
                {t("operations.newSchedule")}
              </Button>
            </Space>
          }
        />
        {tasks === null ? (
          // A first load in flight is the only case that shows a placeholder:
          // once it fails, the banner above is the whole answer.
          error ? null : (
            <Loading label={t("operations.loadingSchedules")} />
          )
        ) : tasks.length === 0 ? (
          <EmptyState
            title={t("operations.noScheduledTasksYet")}
            hint={t("operations.createAScheduleToDispatchExistingRepositoryOr")}
          />
        ) : (
          <ConsoleTable<ScheduledTask>
            rowKey="id"
            dataSource={tasks}
            columns={columns}
            pagination={false}
            scroll={{ x: 1360, y: 500 }}
            expandable={{
              showExpandColumn: false,
              expandedRowKeys: expandedTask ? [expandedTask] : [],
              expandedRowRender: (task) => <TaskRunHistory task={task} />,
            }}
          />
        )}
      </Card>
      {editor && (
        <TaskEditorDialog
          key={editor.session}
          open={editor.open}
          task={editor.task}
          repositories={repositories}
          onClose={() =>
            setEditor((current) => current && { ...current, open: false })
          }
        />
      )}
    </div>
  );
}
