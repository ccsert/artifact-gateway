import { useState } from "react";
import { Button, Input, Select, Space } from "antd";
import {
  createServiceAccount,
  createServiceAccountCredential,
} from "../../client";
import type {
  CreatedServiceAccountCredential,
  ServiceAccount,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";

/**
 * Each write owns its form, its busy state and its error: a failed save stays
 * in the dialog that started it, and the page hears only about success.
 */
export function CreateServiceAccountDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (account: ServiceAccount) => Promise<void> | void;
}) {
  const { t } = usePreferences();
  const [busy, setBusy] = useState(false);
  const [createError, setCreateError] = useState<unknown>(null);
  const [accountName, setAccountName] = useState("");
  const [accountDescription, setAccountDescription] = useState("");

  const submitAccount = async () => {
    setBusy(true);
    setCreateError(null);
    try {
      const { data, error: createError } = await createServiceAccount({
        body: {
          name: accountName.trim(),
          description: accountDescription.trim() || undefined,
        },
      });
      if (createError || !data) {
        setCreateError(createError ?? new Error(t("identity.creationFailed")));
        return;
      }
      setAccountName("");
      setAccountDescription("");
      await onCreated(data);
    } catch (nextError) {
      setCreateError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("identity.newServiceAccount")}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("identity.cancel")}
          </Button>
          <Button
            type="primary"
            loading={busy}
            disabled={!accountName.trim()}
            onClick={() => void submitAccount()}
          >
            {t("identity.create")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {createError !== null && <ErrorBanner error={createError} />}
        <Field
          label={t("identity.accountName")}
          hint={t("identity.forExamplePipeoneCiOrReleaseBot")}
        >
          <Input
            value={accountName}
            maxLength={128}
            onChange={(event) => setAccountName(event.target.value)}
          />
        </Field>
        <Field label={t("identity.purpose")}>
          <Input.TextArea
            value={accountDescription}
            maxLength={1024}
            rows={3}
            onChange={(event) => setAccountDescription(event.target.value)}
          />
        </Field>
      </div>
    </Modal>
  );
}

export function IssueCredentialDialog({
  open,
  account,
  onClose,
  onIssued,
}: {
  open: boolean;
  account: ServiceAccount;
  onClose: () => void;
  onIssued: (
    credential: CreatedServiceAccountCredential,
  ) => Promise<void> | void;
}) {
  const { t } = usePreferences();
  const [busy, setBusy] = useState(false);
  const [issueError, setIssueError] = useState<unknown>(null);
  const [credentialName, setCredentialName] = useState("");
  const [validDays, setValidDays] = useState<30 | 90 | 180 | 365>(90);

  const submitCredential = async () => {
    setBusy(true);
    setIssueError(null);
    try {
      const { data, error: createError } = await createServiceAccountCredential(
        {
          path: { serviceAccountId: account.id },
          body: {
            name: credentialName.trim(),
            expiresAt: new Date(
              Date.now() + validDays * 86_400_000,
            ).toISOString(),
          },
        },
      );
      if (createError || !data) {
        setIssueError(createError ?? new Error(t("identity.issuanceFailed")));
        return;
      }
      setCredentialName("");
      setValidDays(90);
      await onIssued(data);
    } catch (nextError) {
      setIssueError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("identity.issueCredential")}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("identity.cancel")}
          </Button>
          <Button
            type="primary"
            loading={busy}
            disabled={!credentialName.trim()}
            onClick={() => void submitCredential()}
          >
            {t("identity.issue")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {issueError !== null && <ErrorBanner error={issueError} />}
        <Field
          label={t("identity.credentialName")}
          hint={t("identity.includeTheConsumerAndRotationBatch")}
        >
          <Input
            value={credentialName}
            maxLength={128}
            placeholder="jenkins-blue"
            onChange={(event) => setCredentialName(event.target.value)}
          />
        </Field>
        <Field label={t("identity.validity")}>
          <Select<typeof validDays>
            className="w-full"
            value={validDays}
            onChange={setValidDays}
            options={[
              { value: 30, label: t("identity.30Days") },
              {
                value: 90,
                label: t("identity.90DaysRecommended"),
              },
              { value: 180, label: t("identity.180Days") },
              { value: 365, label: t("identity.365Days") },
            ]}
          />
        </Field>
        <p className="text-xs leading-5 text-fg-tertiary">
          {t("identity.oldAndNewCredentialsMayOverlapBrieflyDeployThe")}
        </p>
      </div>
    </Modal>
  );
}
