import { useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import { Button, Input, Select } from "antd";
import { createApiKey } from "../../client";
import type { CreatedApiKey } from "../../client";
import { Field } from "../../components/ui/Layout";
import { FormModal, useDisclosure } from "../../components/ui/Modal";
import { unwrap } from "../../lib/query/client";
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

  const submit = async () => {
    const created = await unwrap(
      createApiKey({
        body: {
          name: name.trim(),
          roles: [role],
          expiresAt: new Date(
            Date.now() + validDays * 86_400_000,
          ).toISOString(),
        },
      }),
    );
    if (!created) return;
    dialog.hide();
    setName("");
    setRole("member");
    setValidDays(90);
    onCreated(created);
  };

  return (
    <>
      <Button type="primary" icon={<PlusOutlined />} onClick={dialog.show}>
        {t("identity.newKey")}
      </Button>
      <FormModal
        open={dialog.open}
        title={t("identity.newApiKey")}
        submitLabel={t("identity.create")}
        canSubmit={!!name.trim()}
        onSubmit={submit}
        onClose={dialog.hide}
      >
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
          hint={t("identity.authenticationIsRejectedAutomaticallyAfterExpiry")}
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
      </FormModal>
    </>
  );
}
