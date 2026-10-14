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
  const { t } = usePreferences();
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
          ? t("operations.editWebhookSubscription")
          : t("operations.newWebhookSubscription")
      }
      onClose={onClose}
      busy={saving}
      footer={
        <Space>
          <Button
            aria-label={t("operations.cancel")}
            onClick={onClose}
            disabled={saving}
          >
            {t("operations.cancel")}
          </Button>
          <Button
            type="primary"
            aria-label={editing ? t("operations.save") : t("operations.create")}
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
        <Field label={t("operations.subscriptionName")}>
          <Input
            value={form.name}
            maxLength={100}
            placeholder={t("operations.eGSecurityAutomation")}
            onChange={(event) =>
              setForm((current) => ({ ...current, name: event.target.value }))
            }
          />
        </Field>
        <Field
          label={t("operations.hTTPSEndpoint")}
          hint={t(
            "operations.productionDeliveryRejectsPrivateLoopbackAndRedirectTargets",
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
          label={t("operations.events")}
          hint={t(
            "operations.supportsArtifactQuarantineAndReleasePlusFinalCargo",
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
          label={t("operations.hMACSigningSecret")}
          hint={
            editing
              ? t("operations.leaveBlankToKeepTheCurrentSecretEntering")
              : t("operations.atLeast32CharactersItIsNeverShown")
          }
        >
          <Input.Password
            value={form.secret}
            autoComplete="new-password"
            placeholder={t("operations.atLeast32Characters")}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                secret: event.target.value,
              }))
            }
          />
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
                ? t("operations.receiveNewEvents")
                : t("operations.pauseNewDeliveries")}
            </span>
          </div>
        </Field>
      </div>
    </Modal>
  );
}
