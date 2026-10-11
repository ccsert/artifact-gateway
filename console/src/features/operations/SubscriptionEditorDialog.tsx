import { useState } from "react";
import { Button, Input, Select, Space, Switch } from "antd";
import {
  createWebhookSubscription,
  updateWebhookSubscription,
} from "../../client";
import type {
  CreateWebhookSubscriptionWritable,
  UpdateWebhookSubscriptionWritable,
  WebhookEventType,
  WebhookSubscription,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { DEFAULT_FORM, type SubscriptionForm } from "./webhookModel";
import { useInvalidateWebhooks } from "./webhookQueries";

function formFor(subscription: WebhookSubscription | null): SubscriptionForm {
  return subscription
    ? {
        name: subscription.name,
        endpointUrl: subscription.endpointUrl,
        secret: "",
        eventTypes: subscription.eventTypes,
        enabled: subscription.enabled,
      }
    : DEFAULT_FORM;
}

/**
 * Creates or edits one subscription. Mount it with a new `key` per opening
 * so the form, including the never-shown secret, starts empty every time.
 */
export function SubscriptionEditorDialog({
  open,
  subscription: editing,
  onClose,
}: {
  open: boolean;
  subscription: WebhookSubscription | null;
  onClose: () => void;
}) {
  const { text } = usePreferences();
  const invalidate = useInvalidateWebhooks();
  const [form, setForm] = useState<SubscriptionForm>(() => formFor(editing));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    const base = {
      name: form.name.trim(),
      endpointUrl: form.endpointUrl.trim(),
      eventTypes: form.eventTypes,
      enabled: form.enabled,
    };
    try {
      const result = editing
        ? await updateWebhookSubscription({
            path: { subscriptionId: editing.id },
            headers: { "If-Match": editing.version },
            body: {
              ...base,
              secret: form.secret || undefined,
            } satisfies UpdateWebhookSubscriptionWritable,
          })
        : await createWebhookSubscription({
            body: {
              ...base,
              secret: form.secret,
            } satisfies CreateWebhookSubscriptionWritable,
          });
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
    form.endpointUrl.trim().startsWith("https://") &&
    form.eventTypes.length > 0 &&
    (Boolean(editing) || form.secret.length >= 32) &&
    (!form.secret || form.secret.length >= 32);

  return (
    <Modal
      open={open}
      title={
        editing
          ? text("编辑 Webhook 订阅", "Edit webhook subscription")
          : text("新建 Webhook 订阅", "New webhook subscription")
      }
      onClose={onClose}
      busy={saving}
      footer={
        <Space>
          <Button
            aria-label={text("取消", "Cancel")}
            onClick={onClose}
            disabled={saving}
          >
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            aria-label={editing ? text("保存", "Save") : text("创建", "Create")}
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
        <Field label={text("订阅名称", "Subscription name")}>
          <Input
            value={form.name}
            maxLength={100}
            placeholder={text("例如：安全自动化", "e.g. Security automation")}
            onChange={(event) =>
              setForm((current) => ({ ...current, name: event.target.value }))
            }
          />
        </Field>
        <Field
          label={text("HTTPS 接收地址", "HTTPS endpoint")}
          hint={text(
            "生产投递会拒绝私网、环回和重定向目标。",
            "Production delivery rejects private, loopback, and redirect targets.",
          )}
        >
          <Input
            value={form.endpointUrl}
            placeholder="https://hooks.example.com/artifacts"
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                endpointUrl: event.target.value,
              }))
            }
          />
        </Field>
        <Field
          group
          label={text("订阅事件", "Events")}
          hint={text(
            "支持制品隔离、解除隔离，以及 Cargo 晋级和复制的最终结果。",
            "Supports artifact quarantine and release, plus final Cargo promotion and replication outcomes.",
          )}
        >
          <Select<WebhookEventType[]>
            mode="multiple"
            className="w-full"
            value={form.eventTypes}
            options={[
              {
                value: "artifact.quarantined",
                label: "artifact.quarantined",
              },
              { value: "artifact.released", label: "artifact.released" },
              {
                value: "cargo.promotion.completed",
                label: "cargo.promotion.completed",
              },
              {
                value: "cargo.promotion.failed",
                label: "cargo.promotion.failed",
              },
              {
                value: "cargo.replication.completed",
                label: "cargo.replication.completed",
              },
              {
                value: "cargo.replication.failed",
                label: "cargo.replication.failed",
              },
            ]}
            onChange={(eventTypes) =>
              setForm((current) => ({ ...current, eventTypes }))
            }
          />
        </Field>
        <Field
          label={text("HMAC 签名密钥", "HMAC signing secret")}
          hint={
            editing
              ? text(
                  "留空则保留现有密钥；填写会立即轮换。",
                  "Leave blank to keep the current secret; entering one rotates it immediately.",
                )
              : text(
                  "至少 32 个字符；保存后不会再次显示。",
                  "At least 32 characters; it is never shown again after saving.",
                )
          }
        >
          <Input.Password
            value={form.secret}
            autoComplete="new-password"
            placeholder={text("至少 32 个字符", "At least 32 characters")}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                secret: event.target.value,
              }))
            }
          />
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
                ? text("接收新事件", "Receive new events")
                : text("暂停生成新投递", "Pause new deliveries")}
            </span>
          </div>
        </Field>
      </div>
    </Modal>
  );
}
