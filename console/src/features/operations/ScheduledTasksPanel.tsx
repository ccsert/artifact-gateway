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
  const { locale, text } = usePreferences();
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
      title: text("任务", "Task"),
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
            {task.description || text("无备注", "No description")}
          </div>
        </div>
      ),
    },
    {
      title: text("动作", "Action"),
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
            ? text("审计保留", "Audit retention")
            : text("仓库保留", "Repository retention")}
        </Badge>
      ),
    },
    {
      title: text("目标", "Target"),
      key: "target",
      width: 180,
      render: (_, task) => {
        const repository = task.repositoryId
          ? repositoryById.get(task.repositoryId)
          : undefined;
        return (
          <div className="text-xs text-fg-secondary">
            {repository?.name ?? text("全局审计日志", "Global audit log")}
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
      title: text("周期", "Interval"),
      dataIndex: "intervalMinutes",
      key: "interval",
      width: 130,
      render: (minutes: number) => (
        <span className="whitespace-nowrap text-xs text-fg-secondary">
          {formatTaskInterval(minutes, text)}
        </span>
      ),
    },
    {
      title: text("下次执行", "Next run"),
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
      title: text("最近投递", "Last dispatch"),
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
      title: text("启用", "Enabled"),
      dataIndex: "enabled",
      key: "enabled",
      width: 90,
      render: (enabled: boolean, task) => (
        <Switch
          size="small"
          value={enabled}
          loading={busyId === task.id}
          aria-label={text("启用计划任务", "Enable scheduled task")}
          onChange={(value) => void updateEnabled(task, value)}
        />
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 170,
      render: (_, task) => (
        <Space size="small">
          <Tooltip title={text("立即执行", "Run now")}>
            <Button
              size="small"
              icon={<RocketOutlined />}
              aria-label={text("立即执行", "Run now")}
              loading={busyId === task.id}
              onClick={() => void runNow(task)}
            />
          </Tooltip>
          <Tooltip title={text("投递历史", "Dispatch history")}>
            <Button
              size="small"
              type={expandedTask === task.id ? "primary" : "default"}
              icon={<HistoryOutlined />}
              aria-label={text("投递历史", "Dispatch history")}
              onClick={() => toggleHistory(task)}
            />
          </Tooltip>
          <Tooltip title={text("编辑", "Edit")}>
            <Button
              size="small"
              icon={<EditOutlined />}
              aria-label={text("编辑", "Edit")}
              onClick={() => openEdit(task)}
            />
          </Tooltip>
          <Popconfirm
            title={text("删除此计划任务？", "Delete this scheduled task?")}
            description={text(
              "投递历史会一并删除，已经生成的后台任务不受影响。",
              "Dispatch history is removed; already submitted jobs are unaffected.",
            )}
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => remove(task)}
          >
            <Tooltip title={text("删除", "Delete")}>
              <Button
                size="small"
                danger
                icon={<DeleteOutlined />}
                aria-label={text("删除", "Delete")}
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
          title={text("计划任务", "Scheduled tasks")}
          extra={
            <Space>
              <Button
                icon={<ReloadOutlined />}
                loading={loading}
                onClick={() => void load()}
              >
                {text("刷新", "Refresh")}
              </Button>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                onClick={openCreate}
              >
                {text("新建计划", "New schedule")}
              </Button>
            </Space>
          }
        />
        {tasks === null ? (
          // A first load in flight is the only case that shows a placeholder:
          // once it fails, the banner above is the whole answer.
          error ? null : (
            <Loading label={text("加载计划任务…", "Loading schedules…")} />
          )
        ) : tasks.length === 0 ? (
          <EmptyState
            title={text("还没有计划任务", "No scheduled tasks yet")}
            hint={text(
              "创建计划，将现有的仓库或审计保留策略按固定周期投递到后台任务队列。",
              "Create a schedule to dispatch existing repository or audit retention policies on a fixed interval.",
            )}
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
