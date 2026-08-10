import { useEffect, useState } from "react";
import { App, Form, Input, Modal, Select, Switch } from "antd";
import { createUser } from "../../client";
import type { CreateUser, User } from "../../client";
import { ErrorBanner } from "../../components/Feedback";
import { usePreferences } from "../../lib/preferences";
import {
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./passwordPolicy";

interface UserCreateDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (user: User) => void;
}

export function UserCreateDialog({
  open,
  onClose,
  onCreated,
}: UserCreateDialogProps) {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const [form] = Form.useForm<CreateUser>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    form.resetFields();
    form.setFieldsValue({ role: "reader", mustChangePassword: true });
  }, [form, open]);

  const submit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values) return;
    setBusy(true);
    setError(null);
    const { data, error: requestError } = await createUser({
      body: {
        ...values,
        name: values.name.trim(),
        displayName: values.displayName?.trim(),
        email: values.email?.trim(),
        description: values.description?.trim(),
      },
    });
    setBusy(false);
    if (requestError || !data) {
      setError(
        requestError ??
          new Error(text("创建用户失败", "Failed to create user")),
      );
      return;
    }
    void message.success(text("用户已创建", "User created"));
    onCreated(data);
  };

  return (
    <Modal
      open={open}
      title={text("新建本地用户", "Create local user")}
      width={680}
      centered
      destroyOnHidden
      confirmLoading={busy}
      okText={text("创建用户", "Create user")}
      cancelText={text("取消", "Cancel")}
      onOk={() => void submit()}
      onCancel={onClose}
    >
      {error ? (
        <div className="mb-4">
          <ErrorBanner error={error} />
        </div>
      ) : null}
      <Form<CreateUser> form={form} layout="vertical" requiredMark="optional">
        <div className="grid grid-cols-2 gap-x-4">
          <Form.Item
            name="name"
            label={text("用户名", "Username")}
            rules={[
              {
                required: true,
                whitespace: true,
                message: text("请输入用户名", "Enter a username"),
              },
            ]}
          >
            <Input autoComplete="off" maxLength={128} placeholder="alice" />
          </Form.Item>
          <Form.Item name="displayName" label={text("显示名", "Display name")}>
            <Input
              maxLength={128}
              placeholder={text("例如：Alice Chen", "For example: Alice Chen")}
            />
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
            <Input
              autoComplete="off"
              maxLength={254}
              placeholder="alice@example.com"
            />
          </Form.Item>
          <Form.Item
            name="role"
            label={text("角色", "Role")}
            rules={[{ required: true }]}
          >
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
        </div>
        <Form.Item name="description" label={text("说明", "Description")}>
          <Input.TextArea
            rows={3}
            maxLength={512}
            showCount
            placeholder={text(
              "记录账户用途、所属团队或责任范围",
              "Record the account purpose, team, or responsibility",
            )}
          />
        </Form.Item>
        <Form.Item
          name="password"
          label={text("初始密码", "Initial password")}
          extra={text(
            "至少 8 个字符且不超过 72 字节。密码只在创建时使用，不会再次显示。",
            "Use at least 8 characters and no more than 72 bytes. The password is never displayed again.",
          )}
          rules={[
            {
              required: true,
              message: text("请输入初始密码", "Enter an initial password"),
            },
            {
              validator: async (_rule, value?: string) => {
                if (value && !localPasswordMeetsMinimum(value)) {
                  throw new Error(
                    text("密码至少需要 8 个字符", "Use at least 8 characters"),
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
          label={text("首次登录要求", "First sign-in requirement")}
          valuePropName="checked"
        >
          <Switch
            checkedChildren={text("必须修改密码", "Change required")}
            unCheckedChildren={text("直接使用", "Ready to use")}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
