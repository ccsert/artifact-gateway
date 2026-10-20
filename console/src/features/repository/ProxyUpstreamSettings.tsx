import { formatDescriptor } from "../../formats";
import {
  Button,
  Checkbox,
  Input,
  InputNumber,
  Radio,
  Select,
  Space,
  Switch,
} from "antd";
import type { Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import type { ProxySettingsDraft } from "./useProxySettingsDraft";
import { UpstreamAuthFields } from "./UpstreamAuthFields";
import { OCIBearerFields } from "./OCIBearerFields";

/** A proxy's upstream: endpoint, allowed hosts, egress proxy and upstream auth. */
export function ProxyUpstreamSettings({
  repo,
  draft,
}: {
  repo: Repository;
  draft: ProxySettingsDraft;
}) {
  const { t } = usePreferences();
  const descriptor = formatDescriptor(repo.format);
  const requiresHosts = descriptor?.upstreamHostAllowList ?? false;
  const {
    endpoint,
    setEndpoint,
    hosts,
    setHosts,
    egressMode,
    setEgressMode,
    egressProtocol,
    setEgressProtocol,
    egressHost,
    setEgressHost,
    egressPort,
    setEgressPort,
    egressUsername,
    setEgressUsername,
    egressPassword,
    setEgressPassword,
    egressClearCredentials,
    setEgressClearCredentials,
    egressRemoteDns,
    setEgressRemoteDns,
    egressNoProxy,
    setEgressNoProxy,
    egressTesting,
    egressTestResult,
    setEgressTestResult,
    runEgressTest,
  } = draft;
  return (
    <Space orientation="vertical" size="middle" className="w-full">
      <Field
        label={t("repository.upstreamURL")}
        hint={t("repository.hTTPSBaseURLChangesTakeEffectImmediatelyOn")}
      >
        <Input
          placeholder="https://upstream.example"
          value={endpoint}
          onChange={(e) => setEndpoint(e.target.value)}
        />
      </Field>
      <Field
        label={t("repository.allowedHosts")}
        hint={
          requiresHosts
            ? t("repository.commaSeparatedRequiredForRawConanNpmPyPI")
            : t("repository.commaSeparatedOptionalForOCIAndMavenProxies")
        }
      >
        <Input
          placeholder="upstream.example, mirror.example"
          value={hosts}
          onChange={(e) => setHosts(e.target.value)}
        />
      </Field>
      <div className="rounded-lg border border-line bg-surface-translucent px-4 py-3">
        <div className="text-sm font-medium text-fg">
          {t("repository.egressProxy")}
        </div>
        <div className="mt-1 text-xs leading-5 text-fg-tertiary">
          {t("repository.configureTheEgressProxyUsedWhenThisProxy")}
        </div>
        <Radio.Group
          className="mt-3 flex flex-col gap-2"
          value={egressMode}
          onChange={(e) => {
            setEgressMode(e.target.value);
            setEgressTestResult(null);
          }}
        >
          <Radio value="direct">
            <span className="text-sm text-fg">{t("repository.direct")}</span>
            <span className="ml-2 text-xs text-fg-tertiary">
              {t("repository.doNotUseAProxyRetainPrivateAddress")}
            </span>
          </Radio>
          <Radio value="environment">
            <span className="text-sm text-fg">
              {t("repository.useEnvironmentVariables")}
            </span>
            <span className="ml-2 text-xs text-fg-tertiary">
              {t("repository.useProcessLevelHTTPSPROXYAndNO")}
            </span>
          </Radio>
          <Radio value="custom">
            <span className="text-sm text-fg">
              {t("repository.customProxy")}
            </span>
            <span className="ml-2 text-xs text-fg-tertiary">
              {t("repository.setAnHTTPOrSOCKS5ProxySpecificallyFor")}
            </span>
          </Radio>
        </Radio.Group>
        {egressMode === "custom" && (
          <Space
            orientation="vertical"
            size="middle"
            className="mt-3 w-full border-t border-line pt-3"
          >
            <div className="flex flex-wrap gap-3">
              <Field label={t("repository.protocol")}>
                <Select
                  className="w-40"
                  value={egressProtocol}
                  onChange={setEgressProtocol}
                  options={[
                    {
                      value: "http",
                      label: t("repository.hTTPCONNECT"),
                    },
                    { value: "socks5", label: "SOCKS5" },
                  ]}
                />
              </Field>
              <Field label={t("repository.proxyHost")}>
                <Input
                  className="w-64"
                  placeholder="proxy.corp.example"
                  value={egressHost}
                  onChange={(e) => setEgressHost(e.target.value)}
                />
              </Field>
              <Field label={t("repository.port")}>
                <InputNumber
                  className="w-28"
                  min={1}
                  max={65535}
                  placeholder="1080"
                  value={egressPort}
                  onChange={(value) => setEgressPort(value)}
                />
              </Field>
            </div>
            {egressProtocol === "socks5" && (
              <div className="flex items-center justify-between gap-6">
                <div>
                  <div className="text-xs font-medium text-fg-secondary">
                    {t("repository.remoteDNSSocks5h")}
                  </div>
                  <div className="mt-1 text-xs leading-5 text-fg-disabled">
                    {t(
                      "repository.whenEnabledTheProxyResolvesTheUpstreamHostname",
                    )}
                  </div>
                </div>
                <Switch
                  checked={egressRemoteDns}
                  onChange={setEgressRemoteDns}
                />
              </div>
            )}
            <div className="flex flex-wrap gap-3">
              <Field label={t("repository.proxyUsernameOptional")}>
                <Input
                  className="w-64"
                  placeholder="gateway"
                  value={egressUsername}
                  onChange={(e) => setEgressUsername(e.target.value)}
                />
              </Field>
              <Field
                label={t("repository.proxyPasswordOptional")}
                hint={t("repository.storedEncryptedWithAES256GCMLeaveBlank2")}
              >
                <Input.Password
                  className="w-64"
                  autoComplete="new-password"
                  placeholder={
                    repo.egressProxy?.credentialsConfigured
                      ? t("repository.configuredEnterAValueToReplaceIt")
                      : t("repository.notConfigured")
                  }
                  value={egressPassword}
                  onChange={(e) => setEgressPassword(e.target.value)}
                />
              </Field>
            </div>
            {repo.egressProxy?.credentialsConfigured && (
              <Checkbox
                checked={egressClearCredentials}
                onChange={(e) => setEgressClearCredentials(e.target.checked)}
              >
                <span className="text-xs text-fg-secondary">
                  {t("repository.clearStoredProxyCredentials")}
                </span>
              </Checkbox>
            )}
            <Field
              label={t("repository.bypassListNoProxy")}
              hint={t(
                "repository.commaSeparatedHostnameSuffixesOrCIDRsMatchingUpstreams",
              )}
            >
              <Input
                placeholder="*.internal.example, 10.0.0.0/8"
                value={egressNoProxy}
                onChange={(e) => setEgressNoProxy(e.target.value)}
              />
            </Field>
          </Space>
        )}
        <div className="mt-3 flex items-center gap-3 border-t border-line pt-3">
          <Button onClick={runEgressTest} loading={egressTesting}>
            {t("repository.testConnection")}
          </Button>
          <span className="text-xs text-fg-disabled">
            {t("repository.theTestUsesTheSavedConfiguration")}
          </span>
          {egressTestResult &&
            (egressTestResult.reachable ? (
              <span className="text-xs text-[var(--ag-status-success)]">
                {t("repository.proxyReachable")}
                {egressTestResult.upstreamStatus
                  ? ` · ${t("repository.upstreamReturned", { upstreamStatus: egressTestResult.upstreamStatus })}`
                  : ""}
                {egressTestResult.latencyMs !== undefined
                  ? ` · ${t("repository.latencyMs", { latencyMs: egressTestResult.latencyMs })}`
                  : ""}
              </span>
            ) : (
              <span className="text-xs text-[var(--ag-status-danger)]">
                {t("repository.connectionFailed")}
                {egressTestResult.error ?? t("repository.unknownError")}
              </span>
            ))}
        </div>
      </div>
      {repo.format === "oci" && <OCIBearerFields draft={draft} />}
      {descriptor?.upstreamAuthentication && (
        <UpstreamAuthFields repo={repo} draft={draft} />
      )}
    </Space>
  );
}
