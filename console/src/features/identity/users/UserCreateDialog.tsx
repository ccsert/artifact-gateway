import { useEffect, useState } from "react";
import { App, Button, Form, Input, Select, Space, Switch } from "antd";
import { createUser } from "../../../client";
import type { CreateUser, User } from "../../../client";
import { ErrorBanner } from "../../../components/ui/Feedback";
import { Modal } from "../../../components/ui/Modal";
import { usePreferences } from "../../../lib/preferences";
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
  const { t } = usePreferences();
  const [form] = Form.useForm<CreateUser>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    form.resetFields();
    form.setFieldsValue({ role: "member", mustChangePassword: true });
  }, [form, open]);

  const submit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values) return;
    setBusy(true);
    setError(null);
    try {
      const { data, error: requestError } = await createUser({
        body: {
          ...values,
          name: values.name.trim(),
          displayName: values.displayName?.trim(),
          email: values.email?.trim(),
          description: values.description?.trim(),
        },
      });
      if (requestError || !data) {
        setError(requestError ?? new Error(t("identity.failedToCreateUser")));
        return;
      }
      void message.success(t("identity.userCreated"));
      onCreated(data);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("identity.createLocalUser")}
      width={680}
      busy={busy}
      onClose={onClose}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("identity.cancel")}
          </Button>
          <Button type="primary" loading={busy} onClick={() => void submit()}>
            {t("identity.createUser")}
          </Button>
        </Space>
      }
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
            label={t("identity.username")}
            rules={[
              {
                required: true,
                whitespace: true,
                message: t("identity.enterAUsername"),
              },
            ]}
          >
            <Input autoComplete="off" maxLength={128} placeholder="alice" />
          </Form.Item>
          <Form.Item name="displayName" label={t("identity.displayName")}>
            <Input
              maxLength={128}
              placeholder={t("identity.forExampleAliceChen")}
            />
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
            <Input
              autoComplete="off"
              maxLength={254}
              placeholder="alice@example.com"
            />
          </Form.Item>
          <Form.Item
            name="role"
            label={t("identity.role")}
            rules={[{ required: true }]}
          >
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
        </div>
        <Form.Item name="description" label={t("identity.description")}>
          <Input.TextArea
            rows={3}
            maxLength={512}
            showCount
            placeholder={t(
              "identity.recordTheAccountPurposeTeamOrResponsibility",
            )}
          />
        </Form.Item>
        <Form.Item
          name="password"
          label={t("identity.initialPassword")}
          extra={t("identity.useAtLeast8CharactersAndNoMore")}
          rules={[
            {
              required: true,
              message: t("identity.enterAnInitialPassword"),
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
          label={t("identity.firstSignInRequirement")}
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
