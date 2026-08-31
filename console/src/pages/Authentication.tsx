import { useCallback, useEffect, useState } from "react";
import {
  ApiOutlined,
  CheckCircleOutlined,
  DeleteOutlined,
  LockOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SaveOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Form,
  Input,
  Popconfirm,
  Select,
  Space,
  Switch,
} from "antd";
import {
  getOidcSettings,
  replaceOidcSettings,
  testOidcSettings,
} from "../client";
import type {
  OidcConnectionTest,
  OidcSettings,
  OidcSettingsUpdateWritable,
} from "../client";
import { Card, PageHeader } from "../components/Layout";
import { ErrorBanner, Loading } from "../components/Feedback";
import { CopyableValue, MetricStrip } from "../components/ConsolePrimitives";
import { Badge } from "../components/Badge";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";

interface AuthenticationFormValues {
  enabled: boolean;
  issuer: string;
  audience: string;
  jwksUrl: string;
  clientId: string;
  clientSecret: string;
  redirectUrl: string;
  scopes: string[];
  adminSubjects: string[];
  readerRoles: string[];
  writerRoles: string[];
  adminRoles: string[];
  provisioningMode: "disabled" | "jit";
  emailLinkingEnabled: boolean;
  jitDefaultRole: "admin" | "writer" | "reader";
}

export function AuthenticationPage() {
  const { locale, text } = usePreferences();
  const [form] = Form.useForm<AuthenticationFormValues>();
  const [settings, setSettings] = useState<OidcSettings | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [dirty, setDirty] = useState(false);
  const [clearSecret, setClearSecret] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<OidcConnectionTest | null>(null);

  const applySettings = useCallback((next: OidcSettings) => {
    setSettings(next);
    setDirty(false);
    setClearSecret(false);
  }, []);

  const load = useCallback(async () => {
    setLoadError(null);
    const { data, error } = await getOidcSettings();
    if (error || !data) {
      setLoadError(
        error ??
          new Error(
            text(
              "加载身份认证配置失败",
              "Failed to load authentication settings",
            ),
          ),
      );
      return;
    }
    applySettings(data);
  }, [applySettings, text]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!settings) return;
    form.setFieldsValue({
      enabled: settings.enabled,
      issuer: settings.issuer,
      audience: settings.audience,
      jwksUrl: settings.jwksUrl ?? "",
      clientId: settings.clientId,
      clientSecret: "",
      redirectUrl: settings.redirectUrl,
      scopes: settings.scopes,
      adminSubjects: settings.adminSubjects,
      readerRoles: settings.readerRoles,
      writerRoles: settings.writerRoles,
      adminRoles: settings.adminRoles,
      provisioningMode: settings.provisioningMode,
      emailLinkingEnabled: settings.emailLinkingEnabled,
      jitDefaultRole: settings.jitDefaultRole,
    });
  }, [form, settings]);

  const validateValues = (values: AuthenticationFormValues) => {
    if (!values.enabled) return true;
    const required: (keyof AuthenticationFormValues)[] = ["issuer", "audience"];
    const missing = required.filter(
      (name) => !String(values[name] ?? "").trim(),
    );
    const fields = missing.map((name) => ({
      name,
      errors: [text("启用 OIDC 时必填", "Required when OIDC is enabled")],
    }));
    const hasClientId = Boolean(values.clientId.trim());
    const hasRedirectUrl = Boolean(values.redirectUrl.trim());
    if (hasClientId !== hasRedirectUrl) {
      fields.push({
        name: hasClientId ? "redirectUrl" : "clientId",
        errors: [
          text(
            "Client ID 与回调地址必须同时配置",
            "Client ID and Redirect URL must be configured together",
          ),
        ],
      });
    }
    if (values.clientSecret.trim() && !hasClientId) {
      fields.push({
        name: "clientId",
        errors: [
          text(
            "配置 Client Secret 时必须填写 Client ID",
            "Client ID is required when a client secret is configured",
          ),
        ],
      });
    }
    if (fields.length === 0) return true;
    form.setFields(fields);
    return false;
  };

  const save = async () => {
    if (!settings) return;
    const values = await form.validateFields();
    if (!validateValues(values)) return;
    setSaving(true);
    setActionError(null);
    setNotice("");
    setTestResult(null);
    const body: OidcSettingsUpdateWritable = {
      enabled: values.enabled,
      issuer: values.issuer.trim(),
      audience: values.audience.trim(),
      clientId: values.clientId.trim(),
      redirectUrl: values.redirectUrl.trim(),
      scopes: values.scopes ?? [],
      adminSubjects: values.adminSubjects ?? [],
      readerRoles: values.readerRoles ?? [],
      writerRoles: values.writerRoles ?? [],
      adminRoles: values.adminRoles ?? [],
      provisioningMode: values.provisioningMode,
      emailLinkingEnabled: values.emailLinkingEnabled,
      jitDefaultRole: values.jitDefaultRole,
    };
    if (values.jwksUrl.trim()) body.jwksUrl = values.jwksUrl.trim();
    if (values.clientSecret.trim())
      body.clientSecret = values.clientSecret.trim();
    if (clearSecret) body.clearClientSecret = true;

    const { data, error } = await replaceOidcSettings({
      body,
      headers: { "If-Match": settings.version },
    });
    setSaving(false);
    if (error || !data) {
      setActionError(
        error ??
          new Error(
            text(
              "保存身份认证配置失败",
              "Failed to save authentication settings",
            ),
          ),
      );
      return;
    }
    applySettings(data);
    setNotice(
      text(
        "配置已保存并在当前节点生效",
        "Settings saved and active on this node",
      ),
    );
  };

  const testConnection = async () => {
    setTesting(true);
    setActionError(null);
    setNotice("");
    const { data, error } = await testOidcSettings();
    setTesting(false);
    if (error || !data) {
      setActionError(
        error ??
          new Error(
            text("身份提供方连接失败", "Identity provider connection failed"),
          ),
      );
      return;
    }
    setTestResult(data);
  };

  if (loadError !== null) {
    return (
      <div>
        <PageHeader title={text("身份认证", "Authentication")} />
        <ErrorBanner error={loadError} onRetry={load} />
      </div>
    );
  }
  if (!settings) return <Loading />;

  const sourceLabel =
    settings.source === "database"
      ? text("运行时配置", "Runtime settings")
      : text("环境变量引导", "Environment bootstrap");
  const secretConfigured = settings.clientSecretConfigured && !clearSecret;

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("身份认证", "Authentication")}
        description={text(
          "配置外部 OIDC 身份提供方、浏览器登录与角色映射",
          "Configure an external OIDC provider, browser sign-in, and role mapping",
        )}
      />

      <MetricStrip
        items={[
          {
            label: text("运行状态", "Runtime status"),
            value: settings.enabled
              ? text("已启用", "Enabled")
              : text("未启用", "Disabled"),
            hint: sourceLabel,
            tone: settings.enabled ? "success" : "default",
          },
          {
            label: text("配置来源", "Configuration source"),
            value: sourceLabel,
            hint: settings.version
              ? text(`版本 ${settings.version}`, `Version ${settings.version}`)
              : text("版本未知", "Version unknown"),
          },
          {
            label: text("客户端密钥", "Client secret"),
            value: secretConfigured
              ? text("已配置", "Configured")
              : text("未配置", "Not configured"),
            hint: text("密钥不会从 API 回读", "Never returned by the API"),
            tone: secretConfigured ? "success" : "warning",
          },
          {
            label: text("最近更新", "Last updated"),
            value: settings.updatedAt
              ? formatDate(settings.updatedAt, locale)
              : text("尚未持久化", "Not persisted"),
            hint: text("数据库配置跨节点共享", "Database settings are shared"),
          },
        ]}
      />

      <div className="ag-feedback-stack space-y-4">
        {settings.source === "environment" && (
          <Alert
            type="info"
            showIcon
            title={text(
              "当前读取环境变量；首次保存会创建数据库运行时配置。",
              "Settings currently come from the environment. The first save creates runtime database settings.",
            )}
          />
        )}
        {actionError !== null && <ErrorBanner error={actionError} />}
        {notice && <Alert type="success" showIcon title={notice} />}
        {testResult && (
          <Alert
            type="success"
            showIcon
            title={text(
              `连接成功，发现耗时 ${testResult.latencyMs} ms`,
              `Connection succeeded in ${testResult.latencyMs} ms`,
            )}
            description={testResult.authorizationEndpoint}
          />
        )}
      </div>

      <Card>
        <Form<AuthenticationFormValues>
          form={form}
          layout="vertical"
          requiredMark={false}
          onValuesChange={(changedValues) => {
            if (changedValues.clientSecret?.trim()) setClearSecret(false);
            setDirty(true);
            setNotice("");
            setTestResult(null);
          }}
        >
          <div className="ag-auth-settings-header">
            <div className="flex min-w-0 items-center gap-3">
              <div className="ag-auth-settings-icon">
                <SafetyCertificateOutlined />
              </div>
              <div className="min-w-0">
                <h2 className="text-sm font-semibold text-zinc-100">
                  {text("企业身份提供方", "Enterprise identity provider")}
                </h2>
                <p className="mt-1 text-xs text-zinc-500">
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
            <section className="ag-settings-section">
              <div className="ag-settings-section-title">
                <ApiOutlined />
                <span>{text("提供方连接", "Provider connection")}</span>
              </div>
              <div className="grid grid-cols-2 gap-x-5">
                <Form.Item
                  className="col-span-2"
                  name="issuer"
                  label="Issuer URL"
                  extra={text(
                    "例如 Keycloak Realm 或 GitLab 的 OIDC Issuer。",
                    "For example, a Keycloak realm or GitLab OIDC issuer.",
                  )}
                >
                  <Input placeholder="https://id.example.com/realms/artifacts" />
                </Form.Item>
                <Form.Item
                  name="audience"
                  label={text("API Audience", "API audience")}
                >
                  <Input placeholder="artifact-gateway-api" />
                </Form.Item>
                <Form.Item name="clientId" label="Client ID">
                  <Input placeholder="artifact-gateway-console" />
                </Form.Item>
                <Form.Item
                  name="redirectUrl"
                  label={text("回调地址", "Redirect URL")}
                >
                  <Input placeholder="https://gateway.example.com/auth/oidc/callback" />
                </Form.Item>
                <Form.Item
                  name="jwksUrl"
                  label="JWKS URL"
                  extra={text(
                    "留空时使用提供方 discovery 文档。",
                    "Leave empty to use provider discovery.",
                  )}
                >
                  <Input
                    placeholder={text("自动发现", "Discovered automatically")}
                  />
                </Form.Item>
                <Form.Item className="col-span-2" name="scopes" label="Scopes">
                  <Select
                    mode="tags"
                    tokenSeparators={[",", " "]}
                    placeholder="openid profile email"
                  />
                </Form.Item>
              </div>
            </section>

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
                  <Badge tone={clearSecret ? "red" : "green"}>
                    {clearSecret
                      ? text("等待保存", "Pending save")
                      : text("已加密存储", "Encrypted at rest")}
                  </Badge>
                </div>
              )}
              <div className="ag-auth-runtime-context">
                <div className="mb-3 text-xs font-semibold text-zinc-200">
                  {text("生效上下文", "Effective context")}
                </div>
                <dl>
                  <div>
                    <dt>{text("来源", "Source")}</dt>
                    <dd>
                      <Badge
                        tone={settings.source === "database" ? "cyan" : "zinc"}
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

            <section className="ag-settings-section ag-settings-section-wide">
              <div className="ag-settings-section-title">
                <SafetyCertificateOutlined />
                <span>{text("角色映射", "Role mapping")}</span>
              </div>
              <p className="mb-4 text-xs text-zinc-500">
                {text(
                  "支持 Keycloak Realm/Client Roles、顶层 roles、groups 等常见声明；权限按 admin、writer、reader 的最高级生效。",
                  "Supports common Keycloak realm/client roles, top-level roles, and groups claims. The highest matching gateway role wins.",
                )}
              </p>
              <div className="grid grid-cols-3 gap-x-5">
                <Form.Item name="readerRoles" label="Reader">
                  <Select mode="tags" tokenSeparators={[",", " "]} />
                </Form.Item>
                <Form.Item name="writerRoles" label="Writer">
                  <Select mode="tags" tokenSeparators={[",", " "]} />
                </Form.Item>
                <Form.Item name="adminRoles" label="Admin">
                  <Select mode="tags" tokenSeparators={[",", " "]} />
                </Form.Item>
                <Form.Item
                  className="col-span-3"
                  name="adminSubjects"
                  label={text(
                    "管理员 Subject 白名单",
                    "Administrator subject allowlist",
                  )}
                  extra={text(
                    "仅用于必须按稳定 sub 直接授予管理员权限的账号。",
                    "Use only for accounts that require administrator access by stable sub claim.",
                  )}
                >
                  <Select mode="tags" tokenSeparators={[",", " "]} />
                </Form.Item>
                <Form.Item
                  name="provisioningMode"
                  label={text("未绑定身份", "Unlinked identities")}
                  extra={text(
                    "关闭时只有管理员手动绑定的身份可以进入本地账户；JIT 会在首次登录时创建账户。",
                    "When disabled, only administrator-linked identities can enter local accounts. JIT creates an account on first sign-in.",
                  )}
                >
                  <Select
                    options={[
                      {
                        value: "disabled",
                        label: text(
                          "拒绝并保持外部身份",
                          "Keep external principal",
                        ),
                      },
                      {
                        value: "jit",
                        label: text(
                          "首次登录自动创建",
                          "Provision on first sign-in",
                        ),
                      },
                    ]}
                  />
                </Form.Item>
                <Form.Item
                  name="jitDefaultRole"
                  label={text("JIT 默认角色", "JIT default role")}
                  extra={text(
                    "仅当声明没有匹配角色映射时使用。",
                    "Used only when no external role mapping matches.",
                  )}
                >
                  <Select
                    options={[
                      { value: "reader", label: "reader" },
                      { value: "writer", label: "writer" },
                      { value: "admin", label: "admin" },
                    ]}
                  />
                </Form.Item>
                <Form.Item
                  name="emailLinkingEnabled"
                  label={text("已验证邮箱自动关联", "Verified email linking")}
                  valuePropName="checked"
                  extra={text(
                    "仅使用 email_verified=true 的声明；多个匹配账户会拒绝登录。",
                    "Only email_verified=true claims are used; multiple matching accounts reject sign-in.",
                  )}
                >
                  <Switch
                    checkedChildren={text("允许", "On")}
                    unCheckedChildren={text("关闭", "Off")}
                  />
                </Form.Item>
              </div>
            </section>
          </div>

          <div className="ag-auth-settings-footer">
            <div className="min-w-0 text-xs text-zinc-500">
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
                onClick={testConnection}
              >
                {text("测试已保存配置", "Test saved settings")}
              </Button>
              <Button
                type="primary"
                icon={dirty ? <SaveOutlined /> : <CheckCircleOutlined />}
                loading={saving}
                disabled={!dirty}
                onClick={save}
              >
                {dirty
                  ? text("保存并应用", "Save and apply")
                  : text("配置已同步", "Settings synchronized")}
              </Button>
            </Space>
          </div>
        </Form>
      </Card>
    </div>
  );
}
