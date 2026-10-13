import { useState } from "react";
import { Alert } from "antd";
import { replaceOidcSettings, testOidcSettings } from "../../client";
import type {
  OidcConnectionTest,
  OidcSettingsUpdateWritable,
} from "../../client";
import { Card, PageHeader } from "../../components/ui/Layout";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { MetricStrip } from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { useQueryClient } from "@tanstack/react-query";
import { identityKeys, settingsKeys } from "../../lib/query/keys";
import { AuthenticationForm } from "./AuthenticationForm";
import { useOidcSettings } from "./settingsQueries";

export function AuthenticationPage() {
  const { locale, text } = usePreferences();
  const queryClient = useQueryClient();
  const settingsQuery = useOidcSettings(
    text("加载身份认证配置失败", "Failed to load authentication settings"),
  );
  const settings = settingsQuery.data ?? null;
  const loadError = settingsQuery.error;
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<OidcConnectionTest | null>(null);
  // Marking the secret for removal belongs to the version it was marked on.
  const [secretRemoval, setSecretRemoval] = useState({
    version: "",
    clear: false,
  });
  const clearSecret =
    secretRemoval.clear && secretRemoval.version === settings?.version;
  const setClearSecret = (clear: boolean) =>
    setSecretRemoval({ version: settings?.version ?? "", clear });

  const save = async (body: OidcSettingsUpdateWritable) => {
    if (!settings) return;
    // Identity changes remove protected queries. Capture this query instance
    // as its generation so a late save cannot repopulate another session.
    const generation = queryClient.getQueryCache().find({
      queryKey: settingsKeys.oidc(),
      exact: true,
    });
    if (!generation) return;
    setSaving(true);
    setActionError(null);
    setNotice("");
    setTestResult(null);
    try {
      const { data, error } = await replaceOidcSettings({
        body,
        headers: { "If-Match": settings.version },
      });
      if (
        queryClient.getQueryCache().find({
          queryKey: settingsKeys.oidc(),
          exact: true,
        }) !== generation
      )
        return;
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
      queryClient.setQueryData(settingsKeys.oidc(), data);
      void queryClient.invalidateQueries({
        queryKey: identityKeys.oidcIssuer(),
      });
      setNotice(
        text(
          "配置已保存并在当前节点生效",
          "Settings saved and active on this node",
        ),
      );
    } catch (nextError) {
      setActionError(nextError);
    } finally {
      setSaving(false);
    }
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

  if (loadError !== null && !settings) {
    return (
      <div>
        <PageHeader title={text("身份认证", "Authentication")} />
        <ErrorBanner error={loadError} onRetry={settingsQuery.refetch} />
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

      {loadError !== null && (
        <ErrorBanner error={loadError} onRetry={settingsQuery.refetch} />
      )}

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

      <div className="space-y-4">
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
        {notice && (
          <Notice tone="success" title={notice} onClose={() => setNotice("")} />
        )}
        {testResult && (
          <Notice
            tone="success"
            title={text(
              `连接成功，发现耗时 ${testResult.latencyMs} ms`,
              `Connection succeeded in ${testResult.latencyMs} ms`,
            )}
            description={testResult.authorizationEndpoint || undefined}
            onClose={() => setTestResult(null)}
          />
        )}
      </div>

      <Card>
        <AuthenticationForm
          key={settings.version}
          settings={settings}
          sourceLabel={sourceLabel}
          clearSecret={clearSecret}
          setClearSecret={setClearSecret}
          saving={saving}
          testing={testing}
          onSave={save}
          onTest={() => void testConnection()}
          onEdited={() => {
            setNotice("");
            setTestResult(null);
          }}
        />
      </Card>
    </div>
  );
}
