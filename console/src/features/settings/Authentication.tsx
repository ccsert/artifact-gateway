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
import "./settings.css";

export function AuthenticationPage() {
  const { locale, t } = usePreferences();
  const queryClient = useQueryClient();
  const settingsQuery = useOidcSettings(
    t("settings.failedToLoadAuthenticationSettings"),
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
          error ?? new Error(t("settings.failedToSaveAuthenticationSettings")),
        );
        return;
      }
      queryClient.setQueryData(settingsKeys.oidc(), data);
      void queryClient.invalidateQueries({
        queryKey: identityKeys.oidcIssuer(),
      });
      setNotice(t("settings.settingsSavedAndActiveOnThisNode"));
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
        error ?? new Error(t("settings.identityProviderConnectionFailed")),
      );
      return;
    }
    setTestResult(data);
  };

  if (loadError !== null && !settings) {
    return (
      <div>
        <PageHeader title={t("settings.authentication")} />
        <ErrorBanner error={loadError} onRetry={settingsQuery.refetch} />
      </div>
    );
  }
  if (!settings) return <Loading />;

  const sourceLabel =
    settings.source === "database"
      ? t("settings.runtimeSettings")
      : t("settings.environmentBootstrap");
  const secretConfigured = settings.clientSecretConfigured && !clearSecret;

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("settings.authentication")}
        description={t("settings.configureAnExternalOIDCProviderBrowserSignIn")}
      />

      {loadError !== null && (
        <ErrorBanner error={loadError} onRetry={settingsQuery.refetch} />
      )}

      <MetricStrip
        items={[
          {
            label: t("settings.runtimeStatus"),
            value: settings.enabled
              ? t("settings.enabled")
              : t("settings.disabled"),
            hint: sourceLabel,
            tone: settings.enabled ? "success" : "default",
          },
          {
            label: t("settings.configurationSource"),
            value: sourceLabel,
            hint: settings.version
              ? t("settings.version2", { version: settings.version })
              : t("settings.versionUnknown"),
          },
          {
            label: t("settings.clientSecret"),
            value: secretConfigured
              ? t("settings.configured")
              : t("settings.notConfigured"),
            hint: t("settings.neverReturnedByTheAPI"),
            tone: secretConfigured ? "success" : "warning",
          },
          {
            label: t("settings.lastUpdated"),
            value: settings.updatedAt
              ? formatDate(settings.updatedAt, locale)
              : t("settings.notPersisted"),
            hint: t("settings.databaseSettingsAreShared"),
          },
        ]}
      />

      <div className="space-y-4">
        {settings.source === "environment" && (
          <Alert
            type="info"
            showIcon
            title={t(
              "settings.settingsCurrentlyComeFromTheEnvironmentTheFirst",
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
            title={t("settings.connectionSucceededInMs", {
              latencyMs: testResult.latencyMs,
            })}
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
