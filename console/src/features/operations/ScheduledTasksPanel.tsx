import { useMemo, useState } from "react";
import {
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button, Dropdown, Space, Switch } from "antd";
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
import { ConfirmDanger } from "../../components/ui/Modal";
import { unwrap } from "../../lib/query/client";
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

  const [toDelete, setToDelete] = useState<ScheduledTask | null>(null);
  const remove = async (task: ScheduledTask) => {
    setActionError(null);
    await unwrap(deleteScheduledTask({ path: { taskId: task.id } }));
    if (expandedTask === task.id) setExpandedTask(null);
    await invalidate();
  };

  const toggleHistory = (task: ScheduledTask) =>
    setExpandedTask((current) => (current === task.id ? null : task.id));

  const columns: ColumnsType<ScheduledTask> = [
    {
      title: t("operations.task"),
      key: "task",
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
      width: 120,
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
      width: 140,
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
      // How often, and when next: one column, so the row fits at 1440px.
      title: t("operations.schedule"),
      key: "schedule",
      width: 180,
      render: (_, task) => (
        <div className="whitespace-nowrap text-xs text-fg-secondary">
          {formatTaskInterval(task.intervalMinutes, t)}
          <div className="mt-1 text-fg-disabled">
            {task.enabled
              ? formatDate(task.nextRunAt, locale)
              : t("operations.paused")}
          </div>
        </div>
      ),
    },
    {
      title: t("operations.lastDispatch"),
      key: "lastRun",
      width: 160,
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
      width: 72,
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
      width: 220,
      render: (_, task) => (
        <Space size={4}>
          <Button
            size="small"
            type="text"
            loading={busyId === task.id}
            onClick={() => void runNow(task)}
          >
            {t("operations.runNow")}
          </Button>
          <Button
            size="small"
            type="text"
            aria-expanded={expandedTask === task.id}
            onClick={() => toggleHistory(task)}
          >
            {t("operations.dispatchHistory")}
          </Button>
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                {
                  key: "edit",
                  icon: <EditOutlined />,
                  label: t("operations.edit"),
                },
                { type: "divider" },
                {
                  key: "delete",
                  danger: true,
                  icon: <DeleteOutlined />,
                  label: t("operations.deleteScheduledTask"),
                },
              ],
              onClick: ({ key }) => {
                if (key === "edit") openEdit(task);
                if (key === "delete") setToDelete(task);
              },
            }}
          >
            <Button
              size="small"
              type="text"
              icon={<MoreOutlined />}
              aria-label={t("operations.moreActionsForTask", {
                name: task.name,
              })}
            />
          </Dropdown>
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
            scroll={{ x: 1080, y: 500 }}
            expandable={{
              showExpandColumn: false,
              expandedRowKeys: expandedTask ? [expandedTask] : [],
              expandedRowRender: (task) => <TaskRunHistory task={task} />,
            }}
          />
        )}
      </Card>
      <ConfirmDanger
        target={toDelete}
        title={t("operations.deleteScheduledTask")}
        message={(task) => (
          <>
            <p>
              {t("operations.deleteScheduledTaskNamed", { name: task.name })}
            </p>
            <p className="mt-2 text-xs text-fg-tertiary">
              {t("operations.dispatchHistoryIsRemovedAlreadySubmittedJobsAre")}
            </p>
          </>
        )}
        confirmLabel={t("operations.delete")}
        onConfirm={remove}
        onClose={() => setToDelete(null)}
        onError={setActionError}
      />
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
