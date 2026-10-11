import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  DeleteOutlined,
  KeyOutlined,
  LogoutOutlined,
  SaveOutlined,
} from "@ant-design/icons";
import {
  Alert,
  App,
  Avatar,
  Badge as AntdBadge,
  Button,
  Descriptions,
  Divider,
  Drawer,
  Form,
  Input,
  Select,
  Space,
  Switch,
  theme as antdTheme,
} from "antd";
import {
  deleteUser,
  resetUserPassword,
  revokeUserSessions,
  updateUser,
} from "../../../client";
import type { ResetUserPassword, UpdateUser, User } from "../../../client";
import { Badge, StateBadge } from "../../../components/ui/Badge";
import { ErrorBanner } from "../../../components/ui/Feedback";
import { ConfirmDialog, Modal } from "../../../components/ui/Modal";
import { formatDate } from "../../../lib/format";
import { usePreferences } from "../../../lib/preferences";
import { identityKeys } from "../../../lib/query/keys";
import {
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./passwordPolicy";
import { UserIdentitiesPanel } from "./UserIdentitiesPanel";
import { UserRepositoryAccessPanel } from "./UserRepositoryAccessPanel";
import { UserSessionsPanel } from "./UserSessionsPanel";
import { isUserLocked, roleTone, userInitials } from "./userPresentation";

interface UserDetailsDrawerProps {
  user: User | null;
  onClose: () => void;
  onChanged: (user: User) => void;
  onDeleted: () => void;
}

type PendingAction = "revoke-sessions" | "delete-account";

export function UserDetailsDrawer({
  user,
  onClose,
  onChanged,
  onDeleted,
}: UserDetailsDrawerProps) {
  const { message } = App.useApp();
  const { locale, t } = usePreferences();
  const { token } = antdTheme.useToken();
  const [profileForm] = Form.useForm<UpdateUser>();
  const [passwordForm] = Form.useForm<ResetUserPassword>();
  const [profileBusy, setProfileBusy] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(
    null,
  );
  const queryClient = useQueryClient();
  const refreshSessions = (userId: string) =>
    void queryClient.invalidateQueries({
      queryKey: identityKeys.userSessions(userId),
    });
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!user) return;
    setError(null);
    profileForm.setFieldsValue({
      displayName: user.displayName,
      email: user.email,
      description: user.description,
      role: user.role,
      state: user.state,
    });
  }, [profileForm, user]);

  if (!user) return null;

  const saveProfile = async () => {
    const values = await profileForm.validateFields().catch(() => null);
    if (!values) return;
    setProfileBusy(true);
    setError(null);
    const { data, error: requestError } = await updateUser({
      path: { userId: user.id },
      headers: { "If-Match": user.version },
      body: {
        ...values,
        displayName: values.displayName?.trim() ?? "",
        email: values.email?.trim() ?? "",
        description: values.description?.trim() ?? "",
      },
    });
    setProfileBusy(false);
    if (requestError || !data) {
      setError(requestError ?? new Error(t("identity.failedToSaveUser")));
      return;
    }
    void message.success(t("identity.userProfileSaved"));
    onChanged(data);
  };

  const resetPassword = async () => {
    const values = await passwordForm.validateFields().catch(() => null);
    if (!values) return;
    setActionBusy(true);
    setError(null);
    const { data, error: requestError } = await resetUserPassword({
      path: { userId: user.id },
      headers: { "If-Match": user.version },
      body: values,
    });
    setActionBusy(false);
    if (requestError || !data) {
      setError(requestError ?? new Error(t("identity.failedToResetPassword")));
      return;
    }
    setPasswordOpen(false);
    passwordForm.resetFields();
    void message.success(t("identity.passwordResetAndExistingSessionsRevoked"));
    refreshSessions(user.id);
    onChanged(data);
  };

  const openConfirm = (action: PendingAction) => {
    setError(null);
    setPendingAction(action);
  };

  const closeConfirm = () => {
    if (actionBusy) return;
    setError(null);
    setPendingAction(null);
  };

  const closePassword = () => {
    if (actionBusy) return;
    setError(null);
    setPasswordOpen(false);
  };

  const revokeSessions = async () => {
    setActionBusy(true);
    setError(null);
    const { data, error: requestError } = await revokeUserSessions({
      path: { userId: user.id },
      headers: { "If-Match": user.version },
    });
    setActionBusy(false);
    if (requestError || !data) {
      setError(requestError ?? new Error(t("identity.failedToRevokeSessions")));
      return;
    }
    setPendingAction(null);
    void message.success(t("identity.allSessionsRevoked"));
    refreshSessions(user.id);
    onChanged(data);
  };

  const deleteAccount = async () => {
    setActionBusy(true);
    setError(null);
    const { error: requestError } = await deleteUser({
      path: { userId: user.id },
    });
    setActionBusy(false);
    if (requestError) {
      setError(requestError);
      return;
    }
    setPendingAction(null);
    void message.success(t("identity.userDeleted"));
    onDeleted();
  };

  const locked = isUserLocked(user);

  return (
    <>
      <Drawer
        open
        size={720}
        destroyOnHidden
        title={
          <div className="flex min-w-0 items-center gap-3">
            <Avatar className="shrink-0" size={36}>
              {userInitials(user)}
            </Avatar>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">
                {user.displayName || user.name}
              </div>
              <div className="truncate font-mono text-xs text-fg-tertiary">
                {user.name}
              </div>
            </div>
          </div>
        }
        extra={
          <Space size={8}>
            <Badge tone={roleTone(user.role)}>
              {user.role === "none" ? t("identity.pending") : user.role}
            </Badge>
            {locked ? (
              <Badge tone="warning">{t("identity.locked")}</Badge>
            ) : (
              <StateBadge state={user.state} />
            )}
          </Space>
        }
        footer={
          <div className="flex justify-end gap-2">
            <Button onClick={onClose}>{t("identity.close")}</Button>
            <Button
              type="primary"
              icon={<SaveOutlined />}
              loading={profileBusy}
              onClick={() => void saveProfile()}
            >
              {t("identity.saveChanges")}
            </Button>
          </div>
        }
        onClose={onClose}
      >
        {error ? (
          <div className="mb-4">
            <ErrorBanner error={error} />
          </div>
        ) : null}

        {(locked || user.mustChangePassword) && (
          <Alert
            className="mb-5"
            showIcon
            type={locked ? "warning" : "info"}
            title={
              locked
                ? t("identity.accountTemporarilyLockedAfterFailedSignIns")
                : t("identity.theUserMustChangeTheirPasswordAtNextSign")
            }
            description={
              locked
                ? t("identity.lockedUntil", {
                    locale: String(formatDate(user.lockedUntil, locale)),
                  })
                : undefined
            }
          />
        )}

        <section>
          <h2 className="mb-3 text-sm font-semibold text-fg-strong">
            {t("identity.accountProfile")}
          </h2>
          <Form<UpdateUser>
            form={profileForm}
            layout="horizontal"
            labelCol={{ flex: "120px" }}
            wrapperCol={{ flex: 1 }}
            labelAlign="left"
            colon={false}
            requiredMark="optional"
          >
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
          </Form>
        </section>

        <Divider />

        <section>
          <h2 className="mb-3 text-sm font-semibold text-fg-strong">
            {t("identity.signInAndSecurity")}
          </h2>
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
          <Space className="mt-4" wrap>
            <Button
              icon={<KeyOutlined />}
              disabled={actionBusy}
              onClick={() => {
                setError(null);
                passwordForm.resetFields();
                passwordForm.setFieldsValue({ mustChangePassword: true });
                setPasswordOpen(true);
              }}
            >
              {user.localPasswordEnabled
                ? t("identity.resetPassword")
                : t("identity.setLocalPassword")}
            </Button>
            <Button
              icon={<LogoutOutlined />}
              loading={actionBusy}
              onClick={() => openConfirm("revoke-sessions")}
            >
              {t("identity.revokeAllSessions")}
            </Button>
          </Space>
        </section>

        <Divider />

        <UserSessionsPanel userId={user.id} />

        <Divider />

        <UserIdentitiesPanel userId={user.id} />

        <Divider />

        <UserRepositoryAccessPanel username={user.name} />

        <Divider />

        <section>
          <h2 className="mb-1 text-sm font-semibold text-fg-strong">
            {t("identity.deleteAccount")}
          </h2>
          <p className="mb-3 text-xs text-fg-tertiary">
            {t("identity.deletionCannotBeUndoneTheFinalActiveAdministratorIs")}
          </p>
          <Button
            danger
            icon={<DeleteOutlined />}
            loading={actionBusy}
            onClick={() => openConfirm("delete-account")}
          >
            {t("identity.deleteUser")}
          </Button>
        </section>
      </Drawer>

      <Modal
        open={passwordOpen}
        title={t("identity.resetPasswordFor", { user: String(user.name) })}
        onClose={closePassword}
        busy={actionBusy}
        footer={
          <Space>
            <Button onClick={closePassword} disabled={actionBusy}>
              {t("identity.cancel")}
            </Button>
            <Button
              type="primary"
              loading={actionBusy}
              onClick={() => void resetPassword()}
            >
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
        <Form<ResetUserPassword> form={passwordForm} layout="vertical">
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

      <ConfirmDialog
        open={pendingAction !== null}
        busy={actionBusy}
        danger={pendingAction === "delete-account"}
        title={
          pendingAction === "delete-account"
            ? t("identity.deleteUser")
            : t("identity.revokeAllSessions")
        }
        confirmLabel={
          pendingAction === "delete-account"
            ? t("identity.deleteUser")
            : t("identity.revokeSessions")
        }
        message={
          <>
            {pendingAction === "delete-account"
              ? t(
                  "identity.permanentlyDeleteTheAccountImmediatelyLosesAccess",
                  { user: String(user.name) },
                )
              : t(
                  "identity.allLocalSessionTokensIssuedToBecomeInvalidImmediately",
                  { user: String(user.name) },
                )}
            {error ? (
              <div className="mt-3">
                <ErrorBanner error={error} />
              </div>
            ) : null}
          </>
        }
        onConfirm={() =>
          void (pendingAction === "delete-account"
            ? deleteAccount()
            : revokeSessions())
        }
        onClose={closeConfirm}
      />
    </>
  );
}
