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
  const { text, locale: uiLocale, colorMode } = usePreferences();
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
      <CardHeader title={text("邮件模板预览", "Email template preview")} />
      <div className="grid min-w-0 gap-4 p-5">
        <p className="text-sm text-fg-tertiary">
          {text(
            "离线合成样例，不读取收件人、不入队、不发送。链接与外部资源在沙箱预览中禁用。",
            "Offline synthetic sample: no recipient read, queue entry, or send. Links and external resources are disabled in the sandbox preview.",
          )}
        </p>
        <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
          <Field label={text("预览场景", "Preview scenario")}>
            <Select
              aria-label={text("预览场景", "Preview scenario")}
              value={scenario}
              disabled={!writable || action.busy}
              onChange={(v) => {
                setScenario(v);
                setPreview(undefined);
              }}
              options={["warning", "critical", "resolved"].map((value) => ({
                value,
                label: quotaLabel(value, text),
              }))}
            />
          </Field>
          <Field label={text("预览语言", "Preview language")}>
            <Select
              aria-label={text("预览语言", "Preview language")}
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
            {text("预览模板", "Preview template")}
          </Button>
          <Segmented
            aria-label={text("预览格式", "Preview format")}
            value={format}
            onChange={setFormat}
            options={[
              { value: "html", label: "HTML" },
              { value: "text", label: text("纯文本", "Plain text") },
            ]}
          />
        </div>
        {Boolean(action.error) && (
          <ErrorBanner error={safeEmailError(action.error, text, "preview")} />
        )}
        {preview ? (
          <>
            <p className="break-words text-sm font-semibold">
              {preview.subject}
            </p>
            <p className="text-xs text-fg-tertiary">
              {text("模板版本", "Template version")}: {preview.templateVersion}{" "}
              · {quotaLabel(scenario, text)} · {locale}
            </p>
            {format === "html" ? (
              <iframe
                className="ag-email-preview"
                title={text(
                  "合成邮件 HTML 预览",
                  "Synthetic email HTML preview",
                )}
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
            title={text(
              "选择场景后预览模板",
              "Choose a scenario and preview the template",
            )}
            hint={text(
              "警告、严重和恢复均提供中英文 HTML 与等价纯文本。",
              "Warning, critical, and recovery provide English and Chinese HTML and equivalent plain text.",
            )}
          />
        ) : null}
      </div>
    </Card>
  );
}
