import { useState } from "react";
import { Button, Select } from "antd";
import {
  testEmailNotification,
  updateEmailTarget,
  type EmailTarget,
  type EmailPreviewInput,
} from "../../client";
import { Card, CardHeader, Field } from "../../components/ui/Layout";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { useQuotaAction } from "./useQuotaAction";
import { quotaResult } from "./useQuotaSnapshot";
import { emailConflict, safeEmailError } from "./emailPresentation";
import { quotaLabel } from "./quotaAlertPresentation";

export function EmailTargetAction({
  target,
  kind,
  writable,
  onCancel,
  onDone,
  onAuthBlocked,
}: {
  target: EmailTarget;
  kind: "test" | "enable" | "disable";
  writable: boolean;
  onCancel: () => void;
  onDone: (sent: boolean) => void;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { t } = usePreferences();
  const [scenario, setScenario] =
    useState<EmailPreviewInput["scenario"]>("warning");
  const [requestKey] = useState(() => crypto.randomUUID());
  const [attempted, setAttempted] = useState(false);
  const action = useQuotaAction(onAuthBlocked);
  const title =
    kind === "test"
      ? t("system.confirmSyntheticTestSend")
      : kind === "enable"
        ? t("system.confirmEnablingEmailTarget")
        : t("system.confirmDisablingEmailTarget");
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && (
        <ErrorBanner
          error={safeEmailError(
            action.error,
            t,
            kind === "test" ? "test" : "save",
          )}
        />
      )}
      <Card>
        <CardHeader title={title} />
        <div className="grid min-w-0 gap-5 p-5">
          <dl className="grid min-w-0 gap-2 text-sm">
            <dt className="text-fg-tertiary">{t("system.target")}</dt>
            <dd className="break-words font-semibold">{target.name}</dd>
            <dt className="text-fg-tertiary">
              {t("system.identityAndConfirmedVersion")}
            </dt>
            <dd className="break-all font-mono text-xs">
              {target.id} · {target.version}
            </dd>
            <dt className="text-fg-tertiary">
              {t("system.emailLanguageAndRecipient")}
            </dt>
            <dd>
              {target.locale === "en" ? "English" : "简体中文"} ·{" "}
              {t("system.oneConfiguredRecipientTheAddressCannotBeRead")}
            </dd>
          </dl>
          {kind === "test" ? (
            <>
              <Field label={t("system.syntheticScenario")}>
                <Select
                  aria-label={t("system.syntheticScenario")}
                  value={scenario}
                  disabled={attempted}
                  onChange={setScenario}
                  options={["warning", "critical", "resolved"].map((value) => ({
                    value,
                    label: quotaLabel(value, t),
                  }))}
                />
              </Field>
              <Notice
                tone="warning"
                closable={false}
                title={t("system.confirmationSendsOneEmailToThisTargetS")}
                description={t("system.theDataIsSyntheticTheRequestEntersThe")}
              />
              {attempted && Boolean(action.error) && (
                <p className="text-sm text-fg-tertiary">
                  {t("system.retryKeepsThisTargetVersionScenarioAndTest")}
                </p>
              )}
            </>
          ) : (
            <Notice
              tone="warning"
              closable={false}
              title={
                kind === "enable"
                  ? t("system.enablingSendsNoTestEmail")
                  : t("system.disablingCannotRecallInFlightMail")
              }
              description={t(
                "system.theTargetVersionChangesExistingQuotaRulesBind",
              )}
            />
          )}
          <div className="flex flex-wrap gap-2">
            <Button aria-label={t("system.cancel")} onClick={onCancel}>
              {t("system.cancel")}
            </Button>
            <Button
              type="primary"
              danger={kind === "disable"}
              loading={action.busy}
              disabled={!writable || emailConflict(action.error)}
              onClick={() => {
                setAttempted(true);
                void action.run(
                  async (signal) =>
                    kind === "test"
                      ? quotaResult(
                          await testEmailNotification({
                            body: { targetId: target.id, scenario },
                            headers: {
                              "If-Match": target.version,
                              "Idempotency-Key": requestKey,
                            },
                            signal,
                          }),
                        )
                      : quotaResult(
                          await updateEmailTarget({
                            body: {
                              name: target.name,
                              locale: target.locale,
                              enabled: kind === "enable",
                            },
                            path: { targetId: target.id },
                            headers: { "If-Match": target.version },
                            signal,
                          }),
                        ),
                  () => onDone(kind === "test"),
                );
              }}
            >
              {kind === "test"
                ? t("system.confirmSyntheticTestSend2")
                : kind === "enable"
                  ? t("system.confirmEnable")
                  : t("system.confirmDisable")}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
