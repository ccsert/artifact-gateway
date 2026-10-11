import { useState } from "react";
import { Button, Input, InputNumber, Select, Space, Switch } from "antd";
import { createScheduledTask, updateScheduledTask } from "../../client";
import type {
  CreateScheduledTask,
  Repository,
  ScheduledTask,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { useInvalidateScheduledTasks } from "./scheduledTaskQueries";
import { DEFAULT_FORM, type TaskForm } from "./scheduledTaskModel";

function formFor(task: ScheduledTask | null): TaskForm {
  return task
    ? {
        name: task.name,
        description: task.description,
        kind: task.kind,
        repositoryId: task.repositoryId,
        intervalMinutes: task.intervalMinutes,
        enabled: task.enabled,
      }
    : DEFAULT_FORM;
}

/**
 * Creates or edits one schedule. Mount it with a new `key` per opening so the
 * form starts from `task` every time.
 */
export function TaskEditorDialog({
  open,
  task: editing,
  repositories,
  onClose,
}: {
  open: boolean;
  task: ScheduledTask | null;
  repositories: Repository[];
  onClose: () => void;
}) {
  const { text } = usePreferences();
  const invalidate = useInvalidateScheduledTasks();
  const [form, setForm] = useState<TaskForm>(() => formFor(editing));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    const body: CreateScheduledTask = {
      ...form,
      name: form.name.trim(),
      description: form.description?.trim(),
      repositoryId:
        form.kind === "repository-retention" ? form.repositoryId : undefined,
    };
    try {
      const result = editing
        ? await updateScheduledTask({
            path: { taskId: editing.id },
            headers: { "If-Match": editing.version },
            body,
          })
        : await createScheduledTask({ body });
      if (result.error) {
        setSaveError(result.error);
        return;
      }
      await invalidate();
      onClose();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  const valid =
    form.name.trim().length > 0 &&
    form.intervalMinutes >= 15 &&
    (form.kind === "audit-retention" || Boolean(form.repositoryId));

  return (
    <Modal
      open={open}
      title={
        editing
          ? text("编辑计划任务", "Edit scheduled task")
          : text("新建计划任务", "New scheduled task")
      }
      onClose={onClose}
      busy={saving}
      footer={
        <Space>
          <Button onClick={onClose} disabled={saving}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            disabled={!valid}
            loading={saving}
            onClick={() => void save()}
          >
            {editing ? text("保存", "Save") : text("创建", "Create")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {saveError ? <ErrorBanner error={saveError} /> : null}
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
                  kind === "audit-retention" ? undefined : current.repositoryId,
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
              <span className="text-xs text-fg-secondary">
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
  );
}
