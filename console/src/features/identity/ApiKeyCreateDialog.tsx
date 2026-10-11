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
  const { text } = usePreferences();
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
        {text("新建密钥", "New key")}
      </Button>
      <Modal
        open={dialog.open}
        title={text("新建 API 密钥", "New API key")}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim()}
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field
            label={text("密钥名称", "Key name")}
            hint={text(
              "用于标识用途，例如 ci-deploy",
              "Describe its purpose, for example ci-deploy",
            )}
          >
            <Input
              className="font-mono"
              placeholder="my-key"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label={text("角色", "Role")}
            hint={text(
              "按最小权限选择。角色在全仓库范围内生效，优先于逐仓库授权。",
              "Choose the least privilege. This global role applies across repositories before repository grants.",
            )}
          >
            <Select<typeof role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                {
                  value: "member",
                  label: text(
                    "member · 成员（无全局权限，按仓库授权）",
                    "member · no global access, repository grants only",
                  ),
                },
                {
                  value: "admin",
                  label: text(
                    "admin · 管理员（全部权限）",
                    "admin · all permissions",
                  ),
                },
              ]}
            />
          </Field>
          <Field
            label={text("有效期", "Validity")}
            hint={text(
              "到期后密钥会自动拒绝认证，无需手动吊销。",
              "Authentication is rejected automatically after expiry.",
            )}
          >
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
          <p className="text-xs text-fg-tertiary">
            {text(
              "创建后只会显示一次明文 Token，请立即保存。",
              "The plaintext token is shown once. Save it immediately.",
            )}
          </p>
        </div>
      </Modal>
    </>
  );
}
