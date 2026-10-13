import type { FormEvent } from "react";
import { KeyOutlined } from "@ant-design/icons";
import { Alert, Button, Input } from "antd";
import { Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

/** The required password change after signing in with a temporary password. */
export function PasswordChangeForm({
  newPassword,
  setNewPassword,
  confirmPassword,
  setConfirmPassword,
  error,
  busy,
  onSubmit,
}: {
  newPassword: string;
  setNewPassword: (value: string) => void;
  confirmPassword: string;
  setConfirmPassword: (value: string) => void;
  error: string;
  busy: boolean;
  onSubmit: (event: FormEvent) => void;
}) {
  const { t } = usePreferences();
  return (
    <form onSubmit={onSubmit} className="ag-login-form">
      <Notice
        tone="info"
        closable={false}
        title={t("auth.changePasswordTitle")}
        description={t("auth.changePasswordHint")}
      />
      <Field label={t("auth.newPassword")}>
        <Input.Password
          size="large"
          autoComplete="new-password"
          maxLength={72}
          value={newPassword}
          onChange={(event) => setNewPassword(event.target.value)}
        />
      </Field>
      <Field label={t("auth.confirmPassword")}>
        <Input.Password
          size="large"
          autoComplete="new-password"
          maxLength={72}
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
        />
      </Field>
      {error && <Alert type="error" showIcon title={error} />}
      <Button
        type="primary"
        size="large"
        htmlType="submit"
        block
        icon={<KeyOutlined />}
        loading={busy}
        disabled={!newPassword || !confirmPassword}
      >
        {t("auth.changePassword")}
      </Button>
    </form>
  );
}
