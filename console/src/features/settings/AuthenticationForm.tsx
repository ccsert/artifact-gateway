import { useState } from "react";
import {
  CheckCircleOutlined,
  DeleteOutlined,
  LockOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SaveOutlined,
} from "@ant-design/icons";
import { Button, Form, Input, Popconfirm, Space, Switch } from "antd";
import type { OidcSettings, OidcSettingsUpdateWritable } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import {
  authenticationFieldErrors,
  toFormValues,
  toUpdateBody,
  type AuthenticationFormValues,
} from "./authenticationModel";
import {
  OidcProviderFields,
  OidcRoleMappingFields,
} from "./OidcSettingsFields";

/**
 * The OIDC settings form for one saved version. The page mounts it per
 * version, so a save or another administrator's change starts it afresh,
 * while refreshed reads of the same version keep the reader's edits.
 */
export function AuthenticationForm({
  settings,
  sourceLabel,
  clearSecret,
  setClearSecret,
  saving,
  testing,
  onSave,
  onTest,
  onEdited,
}: {
  settings: OidcSettings;
  sourceLabel: string;
  clearSecret: boolean;
  setClearSecret: (clear: boolean) => void;
  saving: boolean;
  testing: boolean;
  onSave: (body: OidcSettingsUpdateWritable) => Promise<void>;
  onTest: () => void;
  onEdited: () => void;
}) {
  const { text } = usePreferences();
  const [form] = Form.useForm<AuthenticationFormValues>();
  const [dirty, setDirty] = useState(false);

  const submit = async () => {
    const values = await form.validateFields();
    const errors = authenticationFieldErrors(values, text);
    if (errors.length > 0) {
      form.setFields(errors);
      return;
    }
    await onSave(toUpdateBody(values, clearSecret));
  };

  return (
    <Form<AuthenticationFormValues>
      form={form}
      layout="vertical"
      requiredMark={false}
      initialValues={toFormValues(settings)}
      onValuesChange={(changedValues) => {
        if (changedValues.clientSecret?.trim()) setClearSecret(false);
        setDirty(true);
        onEdited();
      }}
    >
      <div className="ag-auth-settings-header">
        <div className="flex min-w-0 items-center gap-3">
          <div className="ag-auth-settings-icon">
            <SafetyCertificateOutlined />
          </div>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-fg-strong">
              {text("企业身份提供方", "Enterprise identity provider")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {text(
                "使用 Authorization Code + PKCE；会话仅保存已验证的受限身份。",
                "Uses Authorization Code + PKCE; sessions retain only the validated bounded identity.",
              )}
            </p>
          </div>
        </div>
        <Form.Item name="enabled" valuePropName="checked" noStyle>
          <Switch
            checkedChildren={text("启用", "On")}
            unCheckedChildren={text("停用", "Off")}
          />
        </Form.Item>
      </div>

      <div className="ag-auth-settings-body">
        <OidcProviderFields />

        <section className="ag-settings-section">
          <div className="ag-settings-section-title">
            <LockOutlined />
            <span>{text("客户端凭据", "Client credentials")}</span>
          </div>
          <Form.Item
            name="clientSecret"
            label="Client Secret"
            extra={
              clearSecret
                ? text(
                    "保存后将移除当前密钥。输入新值可替换。",
                    "The current secret will be removed on save. Enter a new value to replace it.",
                  )
                : settings.clientSecretConfigured
                  ? text(
                      "已配置。留空会保留现有密钥。",
                      "Configured. Leave empty to retain the current secret.",
                    )
                  : text(
                      "公共 PKCE 客户端可以不配置密钥。",
                      "Public PKCE clients may omit a secret.",
                    )
            }
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder={
                settings.clientSecretConfigured
                  ? text("保持现有密钥", "Keep existing secret")
                  : text("可选", "Optional")
              }
              autoComplete="new-password"
            />
          </Form.Item>
          {settings.clientSecretConfigured && (
            <div className="-mt-3 flex items-center gap-3">
              {clearSecret ? (
                <Button
                  size="small"
                  onClick={() => {
                    setClearSecret(false);
                    setDirty(true);
                  }}
                >
                  {text("撤销移除", "Undo removal")}
                </Button>
              ) : (
                <Popconfirm
                  title={text("移除客户端密钥？", "Remove client secret?")}
                  description={text(
                    "保存配置前不会实际删除。",
                    "The secret is not removed until settings are saved.",
                  )}
                  okText={text("标记移除", "Mark for removal")}
                  cancelText={text("取消", "Cancel")}
                  onConfirm={() => {
                    setClearSecret(true);
                    setDirty(true);
                  }}
                >
                  <Button danger size="small" icon={<DeleteOutlined />}>
                    {text("移除密钥", "Remove secret")}
                  </Button>
                </Popconfirm>
              )}
              <Badge tone={clearSecret ? "danger" : "success"}>
                {clearSecret
                  ? text("等待保存", "Pending save")
                  : text("已加密存储", "Encrypted at rest")}
              </Badge>
            </div>
          )}
          <div className="ag-auth-runtime-context">
            <div className="mb-3 text-xs font-semibold text-fg">
              {text("生效上下文", "Effective context")}
            </div>
            <dl>
              <div>
                <dt>{text("来源", "Source")}</dt>
                <dd>
                  <Badge
                    tone={
                      settings.source === "database"
                        ? "visualization-1"
                        : "neutral"
                    }
                  >
                    {sourceLabel}
                  </Badge>
                </dd>
              </div>
              <div>
                <dt>{text("版本", "Version")}</dt>
                <dd className="font-mono">{settings.version || "—"}</dd>
              </div>
              <div>
                <dt>{text("其他节点", "Other nodes")}</dt>
                <dd>{text("最长 5 秒收敛", "Converge within 5s")}</dd>
              </div>
              <div>
                <dt>{text("现有会话", "Existing sessions")}</dt>
                <dd>
                  {text(
                    "退出或到期前继续有效",
                    "Remain valid until logout or expiry",
                  )}
                </dd>
              </div>
            </dl>
          </div>
        </section>

        <OidcRoleMappingFields />
      </div>

      <div className="ag-auth-settings-footer">
        <div className="min-w-0 text-xs text-fg-tertiary">
          <span>{text("当前回调：", "Current callback: ")}</span>
          {form.getFieldValue("redirectUrl") ? (
            <CopyableValue value={form.getFieldValue("redirectUrl")} />
          ) : (
            "—"
          )}
        </div>
        <Space>
          <Button
            icon={<ReloadOutlined />}
            loading={testing}
            disabled={dirty || !settings.enabled}
            onClick={onTest}
          >
            {text("测试已保存配置", "Test saved settings")}
          </Button>
          <Button
            type="primary"
            icon={dirty ? <SaveOutlined /> : <CheckCircleOutlined />}
            loading={saving}
            disabled={!dirty}
            onClick={() => void submit()}
          >
            {dirty
              ? text("保存并应用", "Save and apply")
              : text("配置已同步", "Settings synchronized")}
          </Button>
        </Space>
      </div>
    </Form>
  );
}
