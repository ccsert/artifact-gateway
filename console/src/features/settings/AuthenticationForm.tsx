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
  const { t } = usePreferences();
  const [form] = Form.useForm<AuthenticationFormValues>();
  const [dirty, setDirty] = useState(false);

  const submit = async () => {
    const values = await form.validateFields();
    const errors = authenticationFieldErrors(values, t);
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
              {t("settings.enterpriseIdentityProvider")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {t("settings.usesAuthorizationCodePKCESessionsRetainOnlyThe")}
            </p>
          </div>
        </div>
        <Form.Item name="enabled" valuePropName="checked" noStyle>
          <Switch
            checkedChildren={t("settings.on2")}
            unCheckedChildren={t("settings.off2")}
          />
        </Form.Item>
      </div>

      <div className="ag-auth-settings-body">
        <OidcProviderFields />

        <section className="ag-settings-section">
          <div className="ag-settings-section-title">
            <LockOutlined />
            <span>{t("settings.clientCredentials")}</span>
          </div>
          <Form.Item
            name="clientSecret"
            label="Client Secret"
            extra={
              clearSecret
                ? t("settings.theCurrentSecretWillBeRemovedOnSave")
                : settings.clientSecretConfigured
                  ? t("settings.configuredLeaveEmptyToRetainTheCurrentSecret")
                  : t("settings.publicPKCEClientsMayOmitASecret")
            }
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder={
                settings.clientSecretConfigured
                  ? t("settings.keepExistingSecret")
                  : t("settings.optional")
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
                  {t("settings.undoRemoval")}
                </Button>
              ) : (
                <Popconfirm
                  title={t("settings.removeClientSecret")}
                  description={t(
                    "settings.theSecretIsNotRemovedUntilSettingsAre",
                  )}
                  okText={t("settings.markForRemoval")}
                  cancelText={t("settings.cancel")}
                  onConfirm={() => {
                    setClearSecret(true);
                    setDirty(true);
                  }}
                >
                  <Button danger size="small" icon={<DeleteOutlined />}>
                    {t("settings.removeSecret")}
                  </Button>
                </Popconfirm>
              )}
              <Badge tone={clearSecret ? "danger" : "success"}>
                {clearSecret
                  ? t("settings.pendingSave")
                  : t("settings.encryptedAtRest")}
              </Badge>
            </div>
          )}
          <div className="ag-auth-runtime-context">
            <div className="mb-3 text-xs font-semibold text-fg">
              {t("settings.effectiveContext")}
            </div>
            <dl>
              <div>
                <dt>{t("settings.source")}</dt>
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
                <dt>{t("settings.version")}</dt>
                <dd className="font-mono">{settings.version || "—"}</dd>
              </div>
              <div>
                <dt>{t("settings.otherNodes")}</dt>
                <dd>{t("settings.convergeWithin5s")}</dd>
              </div>
              <div>
                <dt>{t("settings.existingSessions")}</dt>
                <dd>{t("settings.remainValidUntilLogoutOrExpiry")}</dd>
              </div>
            </dl>
          </div>
        </section>

        <OidcRoleMappingFields />
      </div>

      <div className="ag-auth-settings-footer">
        <div className="min-w-0 text-xs text-fg-tertiary">
          <span>{t("settings.currentCallback")}</span>
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
            {t("settings.testSavedSettings")}
          </Button>
          <Button
            type="primary"
            icon={dirty ? <SaveOutlined /> : <CheckCircleOutlined />}
            loading={saving}
            disabled={!dirty}
            onClick={() => void submit()}
          >
            {dirty
              ? t("settings.saveAndApply")
              : t("settings.settingsSynchronized")}
          </Button>
        </Space>
      </div>
    </Form>
  );
}
