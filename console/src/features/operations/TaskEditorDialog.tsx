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
  const { t } = usePreferences();
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
          ? t("operations.editScheduledTask")
          : t("operations.newScheduledTask")
      }
      onClose={onClose}
      busy={saving}
      footer={
        <Space>
          <Button onClick={onClose} disabled={saving}>
            {t("operations.cancel")}
          </Button>
          <Button
            type="primary"
            disabled={!valid}
            loading={saving}
            onClick={() => void save()}
          >
            {editing ? t("operations.save") : t("operations.create")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {saveError ? <ErrorBanner error={saveError} /> : null}
        <Field label={t("operations.taskName")}>
          <Input
            maxLength={100}
            value={form.name}
            placeholder={t("operations.eGDailySnapshotCleanup")}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                name: event.target.value,
              }))
            }
          />
        </Field>
        <Field label={t("operations.taskAction")}>
          <Select<TaskForm["kind"]>
            className="w-full"
            value={form.kind}
            options={[
              {
                value: "repository-retention",
                label: t("operations.runRepositoryRetention"),
              },
              {
                value: "audit-retention",
                label: t("operations.runAuditRetention"),
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
            label={t("operations.targetRepository")}
            hint={t(
              "operations.onlyActiveHostedRepositoriesWithRetentionSupportAre",
            )}
          >
            <Select
              className="w-full"
              value={form.repositoryId}
              placeholder={t("operations.selectARepository")}
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
            label={t("operations.runInterval")}
            hint={t("operations.minimum15MinutesOnlyOneDispatchIsRecovered")}
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
              <Space.Addon>{t("operations.minutes")}</Space.Addon>
            </Space.Compact>
          </Field>
          <Field label={t("operations.status")}>
            <div className="flex h-8 items-center gap-2">
              <Switch
                value={form.enabled}
                onChange={(enabled) =>
                  setForm((current) => ({ ...current, enabled }))
                }
              />
              <span className="text-xs text-fg-secondary">
                {form.enabled
                  ? t("operations.enabled")
                  : t("operations.disabled")}
              </span>
            </div>
          </Field>
        </div>
        <Field label={t("operations.description")}>
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
