import { formatDescriptor } from "../../formats";
import { useState } from "react";
import { Button, Space, Switch } from "antd";
import { updateRepository } from "../../client";
import type { Repository, RepositoryCapabilities } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { ProxyUpstreamSettings } from "./ProxyUpstreamSettings";
import { useProxySettingsDraft } from "./useProxySettingsDraft";

export function RepositorySettingsTab({
  repo,
  capabilities,
  onUpdated,
}: {
  repo: Repository;
  capabilities: RepositoryCapabilities | null;
  onUpdated: () => void;
}) {
  const { t } = usePreferences();
  const [anonymousRead, setAnonymousRead] = useState(repo.anonymousRead);
  const [mavenStrictPublication, setMavenStrictPublication] = useState(
    repo.mavenStrictPublication,
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const proxyDraft = useProxySettingsDraft(repo, setError);

  const descriptor = formatDescriptor(repo.format);

  const resetForm = () => {
    proxyDraft.reset();
    setAnonymousRead(repo.anonymousRead);
    setMavenStrictPublication(repo.mavenStrictPublication);
    setError(null);
    setNotice("");
  };

  const setSubmissionError = (nextError: unknown) => {
    if (
      typeof nextError === "object" &&
      nextError !== null &&
      "code" in nextError &&
      nextError.code === "upstream_credential_rebind_required"
    ) {
      setError({
        ...nextError,
        message: t("repository.upstreamCredentialRebindRequired"),
      });
      return;
    }
    setError(nextError);
  };

  const submit = async () => {
    setSaving(true);
    setError(null);
    setNotice("");
    try {
      const body = {
        anonymousRead,
        ...(repo.type === "hosted" && descriptor?.strictPublicationSetting
          ? { mavenStrictPublication }
          : {}),
        ...(repo.type === "proxy"
          ? proxyDraft.body(Boolean(descriptor?.upstreamAuthentication))
          : {}),
      };
      // Plaintext exists only in this write request, never in refreshed data.
      proxyDraft.clearSecrets();
      const { error: err } = await updateRepository({
        path: { repositoryId: repo.id },
        headers: { "If-Match": repo.version },
        body,
      });
      if (err) {
        setSubmissionError(err);
        return;
      }
      setNotice(t("repository.repositorySettingsSaved"));
      onUpdated();
    } catch (nextError) {
      setSubmissionError(nextError);
    } finally {
      proxyDraft.clearSecrets();
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-5 flex items-start justify-between gap-6 border-b border-line pb-4">
        <div>
          <h2 className="text-sm font-semibold text-fg-strong">
            {t("repository.repositorySettings")}
          </h2>
          <p className="mt-1 text-xs leading-5 text-fg-tertiary">
            {t("repository.manageReadAccessAndUpstreamConnectivityForProxy")}
          </p>
        </div>
        <Space>
          <Button onClick={resetForm} disabled={saving}>
            {t("repository.reset")}
          </Button>
          <Button type="primary" onClick={submit} loading={saving}>
            {t("repository.saveChanges")}
          </Button>
        </Space>
      </div>
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      <Space orientation="vertical" size="large" className="w-full">
        <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-surface-translucent px-4 py-3">
          <div>
            <div className="text-sm font-medium text-fg">
              {t("repository.allowAnonymousReads")}
            </div>
            <div className="mt-1 text-xs leading-5 text-fg-tertiary">
              {t("repository.whenEnabledProtocolGETHEADRequestsCanRead")}
            </div>
          </div>
          <Switch
            checked={anonymousRead}
            onChange={setAnonymousRead}
            aria-label={t("repository.allowAnonymousReads")}
          />
        </div>
        {repo.type === "hosted" && descriptor?.strictPublicationSetting && (
          <div className="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-line bg-surface-translucent px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-fg">
                {t("repository.strictPublication")}
              </div>
              <div className="mt-1 max-w-[72ch] text-xs leading-5 text-fg-tertiary">
                {t("repository.whenOffNexusStyleDirectVisibilityLetsStandard")}
              </div>
            </div>
            <Switch
              checked={mavenStrictPublication}
              onChange={setMavenStrictPublication}
              aria-label={t("repository.strictPublication")}
            />
          </div>
        )}
        {repo.type === "proxy" && (
          <ProxyUpstreamSettings repo={repo} draft={proxyDraft} />
        )}
        {capabilities && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-line pt-4 text-xs text-fg-tertiary">
            <span className="mr-1">{t("repository.supportedOperations")}</span>
            {capabilities.operations.map((operation) => (
              <Badge key={operation} tone="neutral">
                {operation}
              </Badge>
            ))}
          </div>
        )}
        {error ? <ErrorBanner error={error} /> : null}
      </Space>
    </div>
  );
}
