import { useCallback, useRef, useState } from "react";
import { Button } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import {
  getEmailNotificationCapability,
  listEmailTargets,
  listEmailDeliveries,
  type EmailTarget,
} from "../../client";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import { usePreferences } from "../../lib/preferences";
import { quotaResult, useQuotaSnapshot } from "./useQuotaSnapshot";
import { safeEmailError } from "./emailPresentation";
import { isQuotaAuthError, quotaLabel } from "./quotaAlertPresentation";
import { QuotaBadge } from "./QuotaBadge";
import { EmailTargetEditor } from "./EmailTargetEditor";
import { EmailTargetAction } from "./EmailTargetAction";
import { EmailTemplatePreview } from "./EmailTemplatePreview";
import { EmailDeliveryResults } from "./EmailDeliveryResults";

export function EmailNotificationsPanel() {
  const { identity } = useAuth();
  const { t } = usePreferences();
  if (!platformCapabilities(identity).platformAdmin)
    return (
      <EmptyState
        title={t(
          "system.onlyPlatformAdministratorsCanManageEmailNotifications",
        )}
      />
    );
  return <AuthorizedEmailNotifications />;
}
function AuthorizedEmailNotifications() {
  const { t } = usePreferences();
  const [editor, setEditor] = useState<{ target?: EmailTarget }>();
  const [confirmation, setConfirmation] = useState<{
    target: EmailTarget;
    kind: "test" | "enable" | "disable";
  }>();
  const [notice, setNotice] = useState<"saved" | "queued">();
  const [authBlocked, setAuthBlocked] = useState<unknown>();
  const authGeneration = useRef(0);
  const blockAuthorization = useCallback((error: unknown) => {
    authGeneration.current++;
    setEditor(undefined);
    setConfirmation(undefined);
    setAuthBlocked(error);
  }, []);
  const snapshot = useQuotaSnapshot(async (signal) => {
    const read = async <T,>(
      request: Promise<{ data?: T; error?: unknown }>,
    ) => {
      try {
        return quotaResult(await request);
      } catch (error) {
        if (!signal.aborted && isQuotaAuthError(error))
          blockAuthorization(error);
        throw error;
      }
    };
    const [targets, capability, deliveries] = await Promise.all([
      read(listEmailTargets({ signal })),
      read(getEmailNotificationCapability({ signal })),
      read(listEmailDeliveries({ signal, query: { limit: 50 } })),
    ]);
    return {
      targets,
      capability,
      deliveries,
    };
  }, "email");
  const refresh = async () => {
    const generation = authGeneration.current;
    if ((await snapshot.refresh()) && generation === authGeneration.current)
      setAuthBlocked(undefined);
  };
  if (authBlocked)
    return (
      <ErrorBanner
        error={safeEmailError(authBlocked, t, "read")}
        onRetry={() => void refresh()}
      />
    );
  if (!snapshot.data)
    return snapshot.error ? (
      <ErrorBanner
        error={safeEmailError(snapshot.error, t, "read")}
        onRetry={() => void refresh()}
      />
    ) : (
      <Loading />
    );
  const { targets, capability, deliveries } = snapshot.data;
  const writable = !snapshot.error && !snapshot.loading;
  const done = (sent = false) => {
    setEditor(undefined);
    setConfirmation(undefined);
    setNotice(sent ? "queued" : "saved");
    void snapshot.refresh();
  };
  return (
    <div className="ag-page-stack ag-email-notifications">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-fg-strong">
            {t("system.emailNotifications")}
          </h2>
          <p className="mt-1 text-sm text-fg-tertiary">
            {t("system.manageWriteOnlyRecipientTargetsPreviewTemplatesAnd")}
          </p>
        </div>
        <Button
          icon={<ReloadOutlined aria-hidden="true" />}
          loading={snapshot.loading}
          onClick={() => void refresh()}
        >
          {t("system.refresh")}
        </Button>
      </div>
      {Boolean(snapshot.error) && (
        <ErrorBanner
          tone="warning"
          title={t("system.showingPreviouslyReadData")}
          error={safeEmailError(snapshot.error, t, "read")}
          onRetry={() => void refresh()}
        />
      )}
      {notice && (
        <Notice
          tone="success"
          title={
            notice === "queued"
              ? t("system.syntheticTestQueuedCheckDeliveryResults")
              : t("system.emailTargetSavedNoTestSent")
          }
          onClose={() => setNotice(undefined)}
        />
      )}
      {!capability.enabled && (
        <Notice
          tone="info"
          title={quotaLabel(capability.reason, t)}
          description={t(
            "system.aDeploymentAdministratorConfiguresTheChannelThisPage",
          )}
        />
      )}
      {editor ? (
        <EmailTargetEditor
          target={editor.target}
          writable={writable}
          onAuthBlocked={blockAuthorization}
          onCancel={() => setEditor(undefined)}
          onSaved={() => done()}
        />
      ) : confirmation ? (
        <EmailTargetAction
          {...confirmation}
          writable={
            writable && (confirmation.kind !== "test" || capability.enabled)
          }
          onAuthBlocked={blockAuthorization}
          onCancel={() => setConfirmation(undefined)}
          onDone={done}
        />
      ) : (
        <>
          <Card>
            <CardHeader
              title={t("system.emailTargets")}
              extra={
                <Button
                  type="primary"
                  disabled={!writable}
                  onClick={() => {
                    setNotice(undefined);
                    setEditor({});
                  }}
                >
                  {t("system.newEmailTarget")}
                </Button>
              }
            />
            {targets.length === 0 ? (
              <EmptyState
                title={t("system.noEmailTargets")}
                hint={t("system.newTargetsAreDisabledSavingSendsNoMail")}
              />
            ) : (
              <ul className="divide-y divide-line">
                {targets.map((target) => (
                  <li key={target.id} className="grid min-w-0 gap-3 px-5 py-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <h3 className="break-words text-sm font-semibold text-fg-strong">
                        {target.name}
                      </h3>
                      <QuotaBadge>
                        {target.enabled
                          ? t("system.enabled")
                          : t("system.disabled")}
                      </QuotaBadge>
                    </div>
                    <p className="text-xs text-fg-tertiary">
                      {target.locale === "en" ? "English" : "简体中文"} ·{" "}
                      {target.recipientConfigured
                        ? t("system.recipientConfiguredWriteOnly")
                        : t("system.recipientNotConfigured")}
                    </p>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        size="small"
                        aria-label={t("system.edit", { name: target.name })}
                        disabled={!writable}
                        onClick={() => {
                          setNotice(undefined);
                          setEditor({ target });
                        }}
                      >
                        {t("system.edit2")}
                      </Button>
                      <Button
                        size="small"
                        disabled={!writable || !target.recipientConfigured}
                        aria-label={t(
                          target.enabled
                            ? "system.disableTarget"
                            : "system.enableTarget",
                          { name: target.name },
                        )}
                        onClick={() => {
                          setNotice(undefined);
                          setConfirmation({
                            target,
                            kind: target.enabled ? "disable" : "enable",
                          });
                        }}
                      >
                        {target.enabled
                          ? t("system.disable")
                          : t("system.enable")}
                      </Button>
                      <Button
                        size="small"
                        disabled={
                          !writable ||
                          !capability.enabled ||
                          !target.enabled ||
                          !target.recipientConfigured
                        }
                        aria-label={t("system.test", { name: target.name })}
                        onClick={() => {
                          setNotice(undefined);
                          setConfirmation({ target, kind: "test" });
                        }}
                      >
                        {t("system.sendSyntheticTest")}
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Card>
          <EmailTemplatePreview
            writable={writable}
            onAuthBlocked={blockAuthorization}
          />
          <EmailDeliveryResults deliveries={deliveries} targets={targets} />
          {snapshot.readAt && (
            <p className="text-xs text-fg-tertiary">
              {t("system.readAt")}: {new Date(snapshot.readAt).toLocaleString()}{" "}
              · {t("system.refreshesEvery30SecondsWhileVisible")}
            </p>
          )}
        </>
      )}
    </div>
  );
}
