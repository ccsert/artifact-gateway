import { useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import { Button, Input, Select, Space } from "antd";
import { createApiKey } from "../../client";
import type { CreatedApiKey } from "../../client";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";

/** The "New key" action and its form; reports the created key once. */
export function ApiKeyCreateDialog({
  onCreated,
}: {
  onCreated: (key: CreatedApiKey) => void;
}) {
  const { t } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [role, setRole] = useState<"member" | "admin">("member");
  const [validDays, setValidDays] = useState<30 | 90 | 180 | 365>(90);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const { data, error: err } = await createApiKey({
        body: {
          name: name.trim(),
          roles: [role],
          expiresAt: new Date(
            Date.now() + validDays * 86_400_000,
          ).toISOString(),
        },
      });
      if (err) {
        setError(err);
        return;
      }
      if (data) {
        dialog.hide();
        setName("");
        setRole("member");
        setValidDays(90);
        onCreated(data);
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {t("identity.newKey")}
      </Button>
      <Modal
        open={dialog.open}
        title={t("identity.newApiKey")}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {t("identity.cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim()}
            >
              {t("identity.create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field
            label={t("identity.keyName")}
            hint={t("identity.describeItsPurposeForExampleCiDeploy")}
          >
            <Input
              className="font-mono"
              placeholder="my-key"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label={t("identity.role")}
            hint={t(
              "identity.chooseTheLeastPrivilegeThisGlobalRoleAppliesAcross",
            )}
          >
            <Select<typeof role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                {
                  value: "member",
                  label: t("identity.memberNoGlobalAccessRepositoryGrantsOnly"),
                },
                {
                  value: "admin",
                  label: t("identity.adminAllPermissions"),
                },
              ]}
            />
          </Field>
          <Field
            label={t("identity.validity")}
            hint={t(
              "identity.authenticationIsRejectedAutomaticallyAfterExpiry",
            )}
          >
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
          <p className="text-xs text-fg-tertiary">
            {t("identity.thePlaintextTokenIsShownOnceSaveItImmediately")}
          </p>
        </div>
      </Modal>
    </>
  );
}
