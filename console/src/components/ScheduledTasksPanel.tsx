import { useCallback, useEffect, useMemo, useState } from "react";
import {
  DeleteOutlined,
  EditOutlined,
  HistoryOutlined,
  PlusOutlined,
  ReloadOutlined,
  RocketOutlined,
} from "@ant-design/icons";
import {
  Button,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createScheduledTask,
  deleteScheduledTask,
  listRepositories,
  listScheduledTaskRuns,
  listScheduledTasks,
  runScheduledTask,
  updateScheduledTask,
} from "../client";
import type {
  CreateScheduledTask,
  Repository,
  ScheduledTask,
  ScheduledTaskRun,
} from "../client";
import { Badge, StateBadge } from "./Badge";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader, Field } from "./Layout";
import { Modal, useDisclosure } from "./Modal";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";

type TaskForm = CreateScheduledTask;

const DEFAULT_FORM: TaskForm = {
  name: "",
  description: "",
  kind: "repository-retention",
  intervalMinutes: 1440,
  enabled: true,
};

const RETENTION_FORMATS = new Set([
  "maven",
  "oci",
  "conan",
  "raw",
  "npm",
  "pypi",
]);

export function ScheduledTasksPanel() {
  const { locale, text } = usePreferences();
  const dialog = useDisclosure();
  const [tasks, setTasks] = useState<ScheduledTask[] | null>(null);
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [editing, setEditing] = useState<ScheduledTask | null>(null);
  const [form, setForm] = useState<TaskForm>(DEFAULT_FORM);
  const [expandedTask, setExpandedTask] = useState<string | null>(null);
  const [runs, setRuns] = useState<Record<string, ScheduledTaskRun[]>>({});
  const [runsLoading, setRunsLoading] = useState<string | null>(null);

  const loadRepositories = useCallback(async () => {
    const items: Repository[] = [];
    let pageToken: string | undefined;
    do {
      const result = await listRepositories({
        query: { pageSize: 200, pageToken },
      });
      if (result.error) throw result.error;
      items.push(...(result.data?.items ?? []));
      pageToken = result.data?.nextPageToken;
    } while (pageToken);
    setRepositories(
      items.filter(
        (repo) =>
          repo.state === "active" &&
          (repo.type ?? "hosted") === "hosted" &&
          RETENTION_FORMATS.has(repo.format),
      ),
    );
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await listScheduledTasks();
      if (result.error) throw result.error;
      setTasks(result.data ?? []);
      await loadRepositories();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, [loadRepositories]);

  useEffect(() => {
    void load();
  }, [load]);

  const repositoryById = useMemo(
    () => new Map(repositories.map((repo) => [repo.id, repo])),
    [repositories],
  );

  const openCreate = () => {
    setEditing(null);
    setForm(DEFAULT_FORM);
    setError(null);
    dialog.show();
  };

  const openEdit = (task: ScheduledTask) => {
    setEditing(task);
    setForm({
      name: task.name,
      description: task.description,
      kind: task.kind,
      repositoryId: task.repositoryId,
      intervalMinutes: task.intervalMinutes,
      enabled: task.enabled,
    });
    setError(null);
    dialog.show();
  };

  const save = async () => {
    setBusyId(editing?.id ?? "create");
    setError(null);
    const body: CreateScheduledTask = {
      ...form,
      name: form.name.trim(),
      description: form.description?.trim(),
      repositoryId:
        form.kind === "repository-retention" ? form.repositoryId : undefined,
    };
    const result = editing
      ? await updateScheduledTask({
          path: { taskId: editing.id },
          headers: { "If-Match": editing.version },
          body,
        })
      : await createScheduledTask({ body });
    setBusyId(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    dialog.hide();
    setEditing(null);
    setForm(DEFAULT_FORM);
    await load();
  };

  const updateEnabled = async (task: ScheduledTask, enabled: boolean) => {
    setBusyId(task.id);
    setError(null);
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
    setBusyId(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    await load();
  };

  const runNow = async (task: ScheduledTask) => {
    setBusyId(task.id);
    setError(null);
    const result = await runScheduledTask({ path: { taskId: task.id } });
    setBusyId(null);
    const dispatchError = result.error;
    await load();
    if (expandedTask === task.id) await loadRuns(task.id);
    if (dispatchError) setError(dispatchError);
  };

  const remove = async (task: ScheduledTask) => {
    setBusyId(task.id);
    setError(null);
    const result = await deleteScheduledTask({ path: { taskId: task.id } });
    setBusyId(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    if (expandedTask === task.id) setExpandedTask(null);
    await load();
  };

  const loadRuns = async (taskId: string) => {
    setRunsLoading(taskId);
    const result = await listScheduledTaskRuns({
      path: { taskId },
      query: { limit: 100 },
    });
    setRunsLoading(null);
    if (result.error) {
      setError(result.error);
      return;
    }
    setRuns((current) => ({ ...current, [taskId]: result.data ?? [] }));
  };

  const toggleHistory = async (task: ScheduledTask) => {
    if (expandedTask === task.id) {
      setExpandedTask(null);
      return;
    }
    setExpandedTask(task.id);
    await loadRuns(task.id);
  };

  const columns: ColumnsType<ScheduledTask> = [
    {
      title: text("任务", "Task"),
      key: "task",
      width: 260,
      render: (_, task) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-zinc-100">
            {task.name}
          </div>
          <div
            className="mt-1 truncate text-xs text-zinc-500"
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
        <Badge tone={kind === "audit-retention" ? "violet" : "amber"}>
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
          <div className="text-xs text-zinc-400">
            {repository?.name ?? text("全局审计日志", "Global audit log")}
            {repository && (
              <div className="mt-1 font-mono text-xs text-zinc-600">
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
        <span className="whitespace-nowrap text-xs text-zinc-400">
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
        <div className="whitespace-nowrap text-xs text-zinc-400">
          {task.enabled ? formatDate(value, locale) : "—"}
        </div>
      ),
    },
    {
      title: text("最近投递", "Last dispatch"),
      key: "lastRun",
      width: 180,
      render: (_, task) => (
        <div className="text-xs text-zinc-400">
          {task.lastRunState ? (
            <StateBadge state={task.lastRunState} />
          ) : (
            <span className="text-zinc-600">—</span>
          )}
          {task.lastRunAt && (
            <div className="mt-1 whitespace-nowrap text-xs text-zinc-600">
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
              onClick={() => void toggleHistory(task)}
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

  const valid =
    form.name.trim().length > 0 &&
    form.intervalMinutes >= 15 &&
    (form.kind === "audit-retention" || Boolean(form.repositoryId));

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
        {!tasks ? (
          <Loading label={text("加载计划任务…", "Loading schedules…")} />
        ) : tasks.length === 0 ? (
          <EmptyState
            title={text("还没有计划任务", "No scheduled tasks yet")}
            hint={text(
              "创建计划，将现有的仓库或审计保留策略按固定周期投递到后台任务队列。",
              "Create a schedule to dispatch existing repository or audit retention policies on a fixed interval.",
            )}
          />
        ) : (
          <Table<ScheduledTask>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={tasks}
            columns={columns}
            pagination={false}
            scroll={{ x: 1360, y: 500 }}
            expandable={{
              showExpandColumn: false,
              expandedRowKeys: expandedTask ? [expandedTask] : [],
              expandedRowRender: (task) => (
                <TaskRunHistory
                  task={task}
                  runs={runs[task.id] ?? []}
                  loading={runsLoading === task.id}
                  locale={locale}
                  text={text}
                />
              ),
            }}
          />
        )}
      </Card>
      <Modal
        open={dialog.open}
        title={
          editing
            ? text("编辑计划任务", "Edit scheduled task")
            : text("新建计划任务", "New scheduled task")
        }
        onClose={() => {
          dialog.hide();
          setEditing(null);
        }}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busyId !== null}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              disabled={!valid}
              loading={busyId === (editing?.id ?? "create")}
              onClick={() => void save()}
            >
              {editing ? text("保存", "Save") : text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label={text("任务名称", "Task name")}>
            <Input
              maxLength={100}
              value={form.name}
              placeholder={text(
                "例如：每日清理快照",
                "e.g. Daily snapshot cleanup",
              )}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  name: event.target.value,
                }))
              }
            />
          </Field>
          <Field label={text("任务动作", "Task action")}>
            <Select<TaskForm["kind"]>
              className="w-full"
              value={form.kind}
              options={[
                {
                  value: "repository-retention",
                  label: text("执行仓库保留策略", "Run repository retention"),
                },
                {
                  value: "audit-retention",
                  label: text("执行审计保留策略", "Run audit retention"),
                },
              ]}
              onChange={(kind) =>
                setForm((current) => ({
                  ...current,
                  kind,
                  repositoryId:
                    kind === "audit-retention"
                      ? undefined
                      : current.repositoryId,
                }))
              }
            />
          </Field>
          {form.kind === "repository-retention" && (
            <Field
              label={text("目标仓库", "Target repository")}
              hint={text(
                "只列出支持保留策略的运行中 Hosted 仓库。",
                "Only active hosted repositories with retention support are listed.",
              )}
            >
              <Select
                className="w-full"
                value={form.repositoryId}
                placeholder={text("选择仓库", "Select a repository")}
                showSearch={{ optionFilterProp: ["label", "format"] }}
                options={repositories.map((repo) => ({
                  value: repo.id,
                  label: repo.name,
                  format: repo.format,
                }))}
                onChange={(repositoryId) =>
                  setForm((current) => ({ ...current, repositoryId }))
                }
              />
            </Field>
          )}
          <div className="grid grid-cols-[minmax(0,1fr)_160px] items-end gap-4">
            <Field
              label={text("执行间隔", "Run interval")}
              hint={text(
                "最短 15 分钟；服务停机恢复后只补投一次。",
                "Minimum 15 minutes; only one dispatch is recovered after downtime.",
              )}
            >
              <Space.Compact block>
                <InputNumber
                  className="w-full"
                  min={15}
                  max={525600}
                  step={15}
                  value={form.intervalMinutes}
                  onChange={(value) =>
                    setForm((current) => ({
                      ...current,
                      intervalMinutes: Number(value ?? 15),
                    }))
                  }
                />
                <Space.Addon>{text("分钟", "minutes")}</Space.Addon>
              </Space.Compact>
            </Field>
            <Field label={text("状态", "Status")}>
              <div className="flex h-8 items-center gap-2">
                <Switch
                  value={form.enabled}
                  onChange={(enabled) =>
                    setForm((current) => ({ ...current, enabled }))
                  }
                />
                <span className="text-xs text-zinc-400">
                  {form.enabled
                    ? text("创建后启用", "Enabled")
                    : text("保持停用", "Disabled")}
                </span>
              </div>
            </Field>
          </div>
          <Field label={text("备注", "Description")}>
            <Input.TextArea
              rows={3}
              maxLength={500}
              showCount
              value={form.description}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  description: event.target.value,
                }))
              }
            />
          </Field>
        </div>
      </Modal>
    </div>
  );
}

function TaskRunHistory({
  task,
  runs,
  loading,
  locale,
  text,
}: {
  task: ScheduledTask;
  runs: ScheduledTaskRun[];
  loading: boolean;
  locale: string;
  text: (zh: string, en: string) => string;
}) {
  const columns: ColumnsType<ScheduledTaskRun> = [
    {
      title: text("触发方式", "Trigger"),
      dataIndex: "trigger",
      width: 120,
      render: (value: ScheduledTaskRun["trigger"]) => (
        <span className="text-xs text-zinc-400">
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
        <span className="whitespace-nowrap text-xs text-zinc-400">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("下游任务", "Downstream job"),
      key: "target",
      width: 300,
      render: (_, run) => (
        <div className="min-w-0 font-mono text-xs text-zinc-500">
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
        <span className="block truncate text-xs text-rose-300" title={value}>
          {value ?? "—"}
        </span>
      ),
    },
  ];
  return (
    <div className="px-4 py-3">
      <div className="mb-3 flex items-center gap-2">
        <HistoryOutlined className="text-zinc-500" />
        <span className="text-xs font-medium text-zinc-300">
          {text("投递历史", "Dispatch history")} · {task.name}
        </span>
      </div>
      <Table<ScheduledTaskRun>
        className="ag-console-table"
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={runs}
        columns={columns}
        pagination={false}
        locale={{
          emptyText: text("尚无投递记录", "No dispatch history"),
        }}
        scroll={{ x: 980, y: 280 }}
      />
    </div>
  );
}

function formatTaskInterval(
  minutes: number,
  text: (zh: string, en: string) => string,
) {
  if (minutes % (24 * 60) === 0) {
    const days = minutes / (24 * 60);
    return text(`每 ${days} 天`, `Every ${days} day${days === 1 ? "" : "s"}`);
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60;
    return text(
      `每 ${hours} 小时`,
      `Every ${hours} hour${hours === 1 ? "" : "s"}`,
    );
  }
  return text(`每 ${minutes} 分钟`, `Every ${minutes} minutes`);
}
