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
  const { text } = usePreferences();
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
        setCreateError(
          createError ?? new Error(text("创建失败", "Creation failed")),
        );
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
      title={text("新建服务账号", "New service account")}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            loading={busy}
            disabled={!accountName.trim()}
            onClick={() => void submitAccount()}
          >
            {text("创建", "Create")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {createError !== null && <ErrorBanner error={createError} />}
        <Field
          label={text("账号名称", "Account name")}
          hint={text(
            "例如 pipeone-ci 或 release-bot",
            "For example pipeone-ci or release-bot",
          )}
        >
          <Input
            value={accountName}
            maxLength={128}
            onChange={(event) => setAccountName(event.target.value)}
          />
        </Field>
        <Field label={text("用途说明", "Purpose")}>
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
  const { text } = usePreferences();
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
        setIssueError(
          createError ?? new Error(text("签发失败", "Issuance failed")),
        );
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
      title={text("签发新凭据", "Issue credential")}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            loading={busy}
            disabled={!credentialName.trim()}
            onClick={() => void submitCredential()}
          >
            {text("签发", "Issue")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-4">
        {issueError !== null && <ErrorBanner error={issueError} />}
        <Field
          label={text("凭据名称", "Credential name")}
          hint={text(
            "建议包含使用方和轮换批次",
            "Include the consumer and rotation batch",
          )}
        >
          <Input
            value={credentialName}
            maxLength={128}
            placeholder="jenkins-blue"
            onChange={(event) => setCredentialName(event.target.value)}
          />
        </Field>
        <Field label={text("有效期", "Validity")}>
          <Select<typeof validDays>
            className="w-full"
            value={validDays}
            onChange={setValidDays}
            options={[
              { value: 30, label: text("30 天", "30 days") },
              {
                value: 90,
                label: text("90 天（推荐）", "90 days (recommended)"),
              },
              { value: 180, label: text("180 天", "180 days") },
              { value: 365, label: text("365 天", "365 days") },
            ]}
          />
        </Field>
        <p className="text-xs leading-5 text-fg-tertiary">
          {text(
            "新旧凭据可以短暂并存：先部署新 Token、验证成功，再吊销旧 Token，实现零停机轮换。",
            "Old and new credentials may overlap briefly: deploy the new token, verify it, then revoke the old token for zero-downtime rotation.",
          )}
        </p>
      </div>
    </Modal>
  );
}
