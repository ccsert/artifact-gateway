import { useEffect, useState } from "react";
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
  Modal,
  Select,
  Space,
  Switch,
} from "antd";
import {
  deleteUser,
  resetUserPassword,
  revokeUserSessions,
  updateUser,
} from "../../client";
import type { ResetUserPassword, UpdateUser, User } from "../../client";
import { Badge, StateBadge } from "../../components/Badge";
import { ErrorBanner } from "../../components/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./passwordPolicy";
import { UserIdentitiesPanel } from "./UserIdentitiesPanel";
import { UserSessionsPanel } from "./UserSessionsPanel";
import { isUserLocked, roleTone, userInitials } from "./userPresentation";

interface UserDetailsDrawerProps {
  user: User | null;
  onClose: () => void;
  onChanged: (user: User) => void;
  onDeleted: () => void;
}

export function UserDetailsDrawer({
  user,
  onClose,
  onChanged,
  onDeleted,
}: UserDetailsDrawerProps) {
  const { message, modal } = App.useApp();
  const { locale, text } = usePreferences();
  const [profileForm] = Form.useForm<UpdateUser>();
  const [passwordForm] = Form.useForm<ResetUserPassword>();
  const [profileBusy, setProfileBusy] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [sessionsRefreshKey, setSessionsRefreshKey] = useState(0);
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
      setError(
        requestError ?? new Error(text("保存用户失败", "Failed to save user")),
      );
      return;
    }
    void message.success(text("用户资料已保存", "User profile saved"));
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
      setError(
        requestError ??
          new Error(text("重置密码失败", "Failed to reset password")),
      );
      return;
    }
    setPasswordOpen(false);
    passwordForm.resetFields();
    void message.success(
      text(
        "密码已重置，现有会话已失效",
        "Password reset and existing sessions revoked",
      ),
    );
    setSessionsRefreshKey((current) => current + 1);
    onChanged(data);
  };

  const confirmRevokeSessions = () => {
    modal.confirm({
      title: text("撤销全部会话", "Revoke all sessions"),
      content: text(
        `用户 ${user.name} 已签发的本地登录令牌将立即失效，需要重新登录。`,
        `All local session tokens issued to ${user.name} become invalid immediately.`,
      ),
      okText: text("撤销会话", "Revoke sessions"),
      cancelText: text("取消", "Cancel"),
      onOk: async () => {
        setActionBusy(true);
        setError(null);
        const { data, error: requestError } = await revokeUserSessions({
          path: { userId: user.id },
          headers: { "If-Match": user.version },
        });
        setActionBusy(false);
        if (requestError || !data) {
          setError(
            requestError ??
              new Error(text("撤销会话失败", "Failed to revoke sessions")),
          );
          throw requestError;
        }
        void message.success(text("全部会话已撤销", "All sessions revoked"));
        setSessionsRefreshKey((current) => current + 1);
        onChanged(data);
      },
    });
  };

  const confirmDelete = () => {
    modal.confirm({
      title: text("删除用户", "Delete user"),
      content: text(
        `确定永久删除用户 ${user.name}？该账户将立即无法访问系统。`,
        `Permanently delete ${user.name}? The account immediately loses access.`,
      ),
      okText: text("删除用户", "Delete user"),
      okButtonProps: { danger: true },
      cancelText: text("取消", "Cancel"),
      onOk: async () => {
        setActionBusy(true);
        setError(null);
        const { error: requestError } = await deleteUser({
          path: { userId: user.id },
        });
        setActionBusy(false);
        if (requestError) {
          setError(requestError);
          throw requestError;
        }
        void message.success(text("用户已删除", "User deleted"));
        onDeleted();
      },
    });
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
              <div className="truncate font-mono text-xs text-zinc-500">
                {user.name}
              </div>
            </div>
          </div>
        }
        extra={
          <Space size={8}>
            <Badge tone={roleTone(user.role)}>{user.role}</Badge>
            {locked ? (
              <Badge tone="amber">{text("已锁定", "Locked")}</Badge>
            ) : (
              <StateBadge state={user.state} />
            )}
          </Space>
        }
        footer={
          <div className="flex justify-end gap-2">
            <Button onClick={onClose}>{text("关闭", "Close")}</Button>
            <Button
              type="primary"
              icon={<SaveOutlined />}
              loading={profileBusy}
              onClick={() => void saveProfile()}
            >
              {text("保存更改", "Save changes")}
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
                ? text(
                    "账户因连续登录失败而暂时锁定",
                    "Account temporarily locked after failed sign-ins",
                  )
                : text(
                    "用户下次登录只能修改密码",
                    "The user must change their password at next sign-in",
                  )
            }
            description={
              locked
                ? text(
                    `锁定至 ${formatDate(user.lockedUntil, locale)}`,
                    `Locked until ${formatDate(user.lockedUntil, locale)}`,
                  )
                : undefined
            }
          />
        )}

        <section>
          <h2 className="mb-3 text-sm font-semibold text-zinc-100">
            {text("账户资料", "Account profile")}
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
            <Form.Item
              name="displayName"
              label={text("显示名", "Display name")}
            >
              <Input maxLength={128} />
            </Form.Item>
            <Form.Item
              name="email"
              label={text("邮箱", "Email")}
              rules={[
                {
                  type: "email",
                  message: text("请输入有效邮箱", "Enter a valid email"),
                },
              ]}
            >
              <Input maxLength={254} />
            </Form.Item>
            <Form.Item name="description" label={text("说明", "Description")}>
              <Input.TextArea rows={3} maxLength={512} showCount />
            </Form.Item>
            <Form.Item name="role" label={text("角色", "Role")}>
              <Select
                options={[
                  {
                    value: "reader",
                    label: text("reader · 只读", "reader · read-only"),
                  },
                  {
                    value: "writer",
                    label: text("writer · 读写", "writer · read/write"),
                  },
                  {
                    value: "admin",
                    label: text("admin · 管理员", "admin · administrator"),
                  },
                ]}
              />
            </Form.Item>
            <Form.Item name="state" label={text("账户状态", "Account state")}>
              <Select
                options={[
                  { value: "active", label: text("有效", "Active") },
                  { value: "disabled", label: text("已停用", "Disabled") },
                ]}
              />
            </Form.Item>
          </Form>
        </section>

        <Divider />

        <section>
          <h2 className="mb-3 text-sm font-semibold text-zinc-100">
            {text("登录与安全", "Sign-in and security")}
          </h2>
          <Descriptions bordered size="small" column={2}>
            <Descriptions.Item label={text("最后登录", "Last sign-in")}>
              {user.lastLoginAt
                ? formatDate(user.lastLoginAt, locale)
                : text("从未登录", "Never")}
            </Descriptions.Item>
            <Descriptions.Item label={text("失败次数", "Failed attempts")}>
              <AntdBadge
                count={user.failedLoginAttempts}
                showZero
                color={user.failedLoginAttempts ? "#d97706" : "#52525b"}
              />
            </Descriptions.Item>
            <Descriptions.Item label={text("密码更新时间", "Password changed")}>
              {user.localPasswordEnabled
                ? formatDate(user.passwordChangedAt, locale)
                : text("未设置本地密码", "Not configured")}
            </Descriptions.Item>
            <Descriptions.Item label={text("本地密码", "Local password")}>
              {user.localPasswordEnabled
                ? text("已启用", "Enabled")
                : text("未启用（外部身份）", "Not enabled (external identity)")}
            </Descriptions.Item>
            <Descriptions.Item label={text("下次登录改密", "Change required")}>
              {user.mustChangePassword ? text("是", "Yes") : text("否", "No")}
            </Descriptions.Item>
            <Descriptions.Item label={text("创建时间", "Created")}>
              {formatDate(user.createdAt, locale)}
            </Descriptions.Item>
            <Descriptions.Item label={text("最后更新", "Updated")}>
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
              {text(
                user.localPasswordEnabled ? "重置密码" : "设置本地密码",
                user.localPasswordEnabled
                  ? "Reset password"
                  : "Set local password",
              )}
            </Button>
            <Button
              icon={<LogoutOutlined />}
              loading={actionBusy}
              onClick={confirmRevokeSessions}
            >
              {text("撤销全部会话", "Revoke all sessions")}
            </Button>
          </Space>
        </section>

        <Divider />

        <UserSessionsPanel userId={user.id} refreshKey={sessionsRefreshKey} />

        <Divider />

        <UserIdentitiesPanel userId={user.id} />

        <Divider />

        <section>
          <h2 className="mb-1 text-sm font-semibold text-zinc-100">
            {text("删除账户", "Delete account")}
          </h2>
          <p className="mb-3 text-xs text-zinc-500">
            {text(
              "删除不可撤销；最后一个有效管理员受到后端保护。",
              "Deletion cannot be undone. The final active administrator is protected.",
            )}
          </p>
          <Button
            danger
            icon={<DeleteOutlined />}
            loading={actionBusy}
            onClick={confirmDelete}
          >
            {text("删除用户", "Delete user")}
          </Button>
        </section>
      </Drawer>

      <Modal
        open={passwordOpen}
        title={text(
          `重置 ${user.name} 的密码`,
          `Reset password for ${user.name}`,
        )}
        width={520}
        destroyOnHidden
        confirmLoading={actionBusy}
        okText={text("重置密码", "Reset password")}
        cancelText={text("取消", "Cancel")}
        onOk={() => void resetPassword()}
        onCancel={() => {
          setError(null);
          setPasswordOpen(false);
        }}
      >
        {error ? (
          <div className="mb-4">
            <ErrorBanner error={error} />
          </div>
        ) : null}
        <Form<ResetUserPassword> form={passwordForm} layout="vertical">
          <Form.Item
            name="password"
            label={text("新密码", "New password")}
            rules={[
              {
                required: true,
                message: text("请输入新密码", "Enter a new password"),
              },
              {
                validator: async (_rule, value?: string) => {
                  if (value && !localPasswordMeetsMinimum(value)) {
                    throw new Error(
                      text(
                        "密码至少需要 8 个字符",
                        "Use at least 8 characters",
                      ),
                    );
                  }
                  if (value && !localPasswordFitsBcrypt(value)) {
                    throw new Error(
                      text("密码不能超过 72 字节", "Use no more than 72 bytes"),
                    );
                  }
                },
              },
            ]}
          >
            <Input.Password autoComplete="new-password" maxLength={72} />
          </Form.Item>
          <Form.Item
            name="mustChangePassword"
            label={text("下次登录", "Next sign-in")}
            valuePropName="checked"
          >
            <Switch
              checkedChildren={text("必须修改密码", "Change required")}
              unCheckedChildren={text("直接使用", "Ready to use")}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
