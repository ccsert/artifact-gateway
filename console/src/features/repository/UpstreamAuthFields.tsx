import { Input, Radio, Space } from "antd";
import type { Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import type { ProxySettingsDraft } from "./useProxySettingsDraft";

/** Write-only credentials for the bound upstream or OCI token issuer. */
export function UpstreamAuthFields({
  repo,
  draft,
}: {
  repo: Repository;
  draft: ProxySettingsDraft;
}) {
  const { t } = usePreferences();
  const {
    upstreamScheme,
    setUpstreamScheme,
    upstreamUsername,
    setUpstreamUsername,
    upstreamSecret,
    setUpstreamSecret,
  } = draft;
  return (
    <div
      role="group"
      aria-label={t("repository.upstreamAuthentication")}
      className="rounded-lg border border-line bg-surface-translucent px-4 py-3"
    >
      <div className="text-sm font-medium text-fg">
        {t("repository.upstreamAuthentication")}
      </div>
      <div className="mt-1 text-xs leading-5 text-fg-tertiary">
        {t(
          repo.format === "oci" && draft.ociBearerEnabled
            ? "repository.upstreamCredentialToBoundIssuer"
            : "repository.attachACredentialToUpstreamFetchesWhenThe",
        )}
      </div>
      <Radio.Group
        className="mt-3 flex flex-col gap-2"
        value={upstreamScheme}
        onChange={(e) => setUpstreamScheme(e.target.value)}
      >
        <Radio value="none">
          <span className="text-sm text-fg">{t("repository.anonymous")}</span>
          <span className="ml-2 text-xs text-fg-tertiary">
            {repo.upstreamAuth?.credentialsConfigured
              ? t("repository.sendNoCredentialAndClearTheStoredOne")
              : t("repository.sendNoCredential")}
          </span>
        </Radio>
        <Radio value="basic">
          <span className="text-sm text-fg">{t("repository.basic")}</span>
          <span className="ml-2 text-xs text-fg-tertiary">
            {t("repository.sendAnHTTPBasicUsernameAndPassword")}
          </span>
        </Radio>
        <Radio value="bearer">
          <span className="text-sm text-fg">{t("repository.bearerToken")}</span>
          <span className="ml-2 text-xs text-fg-tertiary">
            {t("repository.sendAuthorizationBearerToken")}
          </span>
        </Radio>
      </Radio.Group>
      {upstreamScheme !== "none" && (
        <Space
          orientation="vertical"
          size="middle"
          className="mt-3 w-full border-t border-line pt-3"
        >
          <div className="flex flex-wrap gap-3">
            {upstreamScheme === "basic" && (
              <Field label={t("repository.upstreamUsername")}>
                <Input
                  className="w-64"
                  placeholder="gateway"
                  value={upstreamUsername}
                  onChange={(e) => setUpstreamUsername(e.target.value)}
                />
              </Field>
            )}
            <Field
              label={
                upstreamScheme === "basic"
                  ? t("repository.upstreamPassword")
                  : t("repository.upstreamToken")
              }
              hint={t("repository.storedEncryptedWithAES256GCMLeaveBlank")}
            >
              <Input.Password
                className="w-64"
                autoComplete="new-password"
                placeholder={
                  repo.upstreamAuth?.credentialsConfigured &&
                  repo.upstreamAuth.scheme === upstreamScheme
                    ? t("repository.configuredEnterAValueToReplaceIt")
                    : t("repository.notConfigured")
                }
                value={upstreamSecret}
                onChange={(e) => setUpstreamSecret(e.target.value)}
              />
            </Field>
          </div>
        </Space>
      )}
    </div>
  );
}
