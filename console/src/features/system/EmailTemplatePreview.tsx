import { useState } from "react";
import { Button, Select, Segmented } from "antd";
import {
  previewEmailNotification,
  type EmailPreview,
  type EmailPreviewInput,
} from "../../client";
import { Card, CardHeader, Field } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { useQuotaAction } from "./useQuotaAction";
import { quotaResult } from "./useQuotaSnapshot";
import { emailPreviewDocument, safeEmailError } from "./emailPresentation";
import { quotaLabel } from "./quotaAlertPresentation";

export function EmailTemplatePreview({
  writable,
  onAuthBlocked,
}: {
  writable: boolean;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { t, locale: uiLocale, colorMode } = usePreferences();
  const [scenario, setScenario] =
    useState<EmailPreviewInput["scenario"]>("warning");
  const [locale, setLocale] = useState<EmailPreviewInput["locale"]>(
    uiLocale === "zh-CN" ? "zh-CN" : "en",
  );
  const [preview, setPreview] = useState<EmailPreview>();
  const [format, setFormat] = useState("html");
  const action = useQuotaAction(onAuthBlocked);
  return (
    <Card>
      <CardHeader title={t("system.emailTemplatePreview")} />
      <div className="grid min-w-0 gap-4 p-5">
        <p className="text-sm text-fg-tertiary">
          {t("system.offlineSyntheticSampleNoRecipientReadQueueEntry")}
        </p>
        <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
          <Field label={t("system.previewScenario")}>
            <Select
              aria-label={t("system.previewScenario")}
              value={scenario}
              disabled={!writable || action.busy}
              onChange={(v) => {
                setScenario(v);
                setPreview(undefined);
              }}
              options={["warning", "critical", "resolved"].map((value) => ({
                value,
                label: quotaLabel(value, t),
              }))}
            />
          </Field>
          <Field label={t("system.previewLanguage")}>
            <Select
              aria-label={t("system.previewLanguage")}
              value={locale}
              disabled={!writable || action.busy}
              onChange={(v) => {
                setLocale(v);
                setPreview(undefined);
              }}
              options={[
                { value: "zh-CN", label: "简体中文" },
                { value: "en", label: "English" },
              ]}
            />
          </Field>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Button
            loading={action.busy}
            disabled={!writable}
            onClick={() =>
              void action.run(
                async (signal) =>
                  quotaResult(
                    await previewEmailNotification({
                      body: { scenario, locale },
                      signal,
                    }),
                  ),
                setPreview,
              )
            }
          >
            {t("system.previewTemplate")}
          </Button>
          <Segmented
            aria-label={t("system.previewFormat")}
            value={format}
            onChange={setFormat}
            options={[
              { value: "html", label: "HTML" },
              { value: "text", label: t("system.plainText") },
            ]}
          />
        </div>
        {Boolean(action.error) && (
          <ErrorBanner error={safeEmailError(action.error, t, "preview")} />
        )}
        {preview ? (
          <>
            <p className="break-words text-sm font-semibold">
              {preview.subject}
            </p>
            <p className="text-xs text-fg-tertiary">
              {t("system.templateVersion")}: {preview.templateVersion} ·{" "}
              {quotaLabel(scenario, t)} · {locale}
            </p>
            {format === "html" ? (
              <iframe
                className="ag-email-preview"
                title={t("system.syntheticEmailHTMLPreview")}
                sandbox=""
                referrerPolicy="no-referrer"
                style={{ colorScheme: colorMode }}
                srcDoc={emailPreviewDocument(preview.html, locale)}
              />
            ) : (
              <pre className="ag-email-plain">{preview.text}</pre>
            )}
          </>
        ) : !action.busy && !action.error ? (
          <EmptyState
            title={t("system.chooseAScenarioAndPreviewTheTemplate")}
            hint={t(
              "system.warningCriticalAndRecoveryProvideEnglishAndChinese",
            )}
          />
        ) : null}
      </div>
    </Card>
  );
}
