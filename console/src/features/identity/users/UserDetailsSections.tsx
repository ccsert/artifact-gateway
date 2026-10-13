import {
  Badge as AntdBadge,
  Descriptions,
  Form,
  Input,
  Select,
  Space,
  Switch,
  Button,
  theme as antdTheme,
  type FormInstance,
} from "antd";
import type { ResetUserPassword, User } from "../../../client";
import { ErrorBanner } from "../../../components/ui/Feedback";
import { Modal } from "../../../components/ui/Modal";
import { formatDate } from "../../../lib/format";
import { usePreferences } from "../../../lib/preferences";
import {
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./passwordPolicy";

/** The editable profile fields; they render inside the drawer's form. */
export function UserProfileFields() {
  const { t } = usePreferences();
  return (
    <>
      <Form.Item name="displayName" label={t("identity.displayName")}>
        <Input maxLength={128} />
      </Form.Item>
      <Form.Item
        name="email"
        label={t("identity.email")}
        rules={[
          {
            type: "email",
            message: t("identity.enterAValidEmail"),
          },
        ]}
      >
        <Input maxLength={254} />
      </Form.Item>
      <Form.Item name="description" label={t("identity.description")}>
        <Input.TextArea rows={3} maxLength={512} showCount />
      </Form.Item>
      <Form.Item name="role" label={t("identity.role")}>
        <Select
          options={[
            {
              value: "member",
              label: t("identity.memberNoRepositoryAccessUntilGranted"),
            },
            {
              value: "none",
              label: t("identity.noneAwaitingApproval"),
            },
            {
              value: "admin",
              label: t("identity.adminAdministrator"),
            },
          ]}
        />
      </Form.Item>
      <Form.Item name="state" label={t("identity.accountState")}>
        <Select
          options={[
            { value: "active", label: t("identity.active") },
            { value: "disabled", label: t("identity.disabled") },
          ]}
        />
      </Form.Item>
    </>
  );
}

/** Sign-in history and password state of one account. */
export function UserSecuritySummary({ user }: { user: User }) {
  const { locale, t } = usePreferences();
  const { token } = antdTheme.useToken();
  return (
    <Descriptions bordered size="small" column={2}>
      <Descriptions.Item label={t("identity.lastSignIn")}>
        {user.lastLoginAt
          ? formatDate(user.lastLoginAt, locale)
          : t("identity.never")}
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.failedAttempts")}>
        <AntdBadge
          count={user.failedLoginAttempts}
          showZero
          color={
            user.failedLoginAttempts
              ? token.colorWarning
              : token.colorTextQuaternary
          }
        />
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.passwordChanged")}>
        {user.localPasswordEnabled
          ? formatDate(user.passwordChangedAt, locale)
          : t("identity.notConfigured")}
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.localPassword")}>
        {user.localPasswordEnabled
          ? t("identity.enabled")
          : t("identity.notEnabledExternalIdentity")}
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.changeRequired2")}>
        {user.mustChangePassword ? t("identity.yes") : t("identity.no")}
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.created")}>
        {formatDate(user.createdAt, locale)}
      </Descriptions.Item>
      <Descriptions.Item label={t("identity.updated")}>
        {formatDate(user.updatedAt, locale)}
      </Descriptions.Item>
    </Descriptions>
  );
}

/** Set or reset an account's local password. */
export function ResetPasswordDialog({
  open,
  userName,
  form,
  busy,
  error,
  onClose,
  onSubmit,
}: {
  open: boolean;
  userName: string;
  form: FormInstance<ResetUserPassword>;
  busy: boolean;
  error: unknown;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const { t } = usePreferences();
  return (
    <Modal
      open={open}
      title={t("identity.resetPasswordFor", { user: userName })}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("identity.cancel")}
          </Button>
          <Button type="primary" loading={busy} onClick={onSubmit}>
            {t("identity.resetPassword")}
          </Button>
        </Space>
      }
    >
      {error ? (
        <div className="mb-4">
          <ErrorBanner error={error} />
        </div>
      ) : null}
      <Form<ResetUserPassword> form={form} layout="vertical">
        <Form.Item
          name="password"
          label={t("identity.newPassword")}
          rules={[
            {
              required: true,
              message: t("identity.enterANewPassword"),
            },
            {
              validator: async (_rule, value?: string) => {
                if (value && !localPasswordMeetsMinimum(value)) {
                  throw new Error(t("identity.useAtLeast8Characters"));
                }
                if (value && !localPasswordFitsBcrypt(value)) {
                  throw new Error(t("identity.useNoMoreThan72Bytes"));
                }
              },
            },
          ]}
        >
          <Input.Password autoComplete="new-password" maxLength={72} />
        </Form.Item>
        <Form.Item
          name="mustChangePassword"
          label={t("identity.nextSignIn")}
          valuePropName="checked"
        >
          <Switch
            checkedChildren={t("identity.changeRequired3")}
            unCheckedChildren={t("identity.readyToUse")}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
