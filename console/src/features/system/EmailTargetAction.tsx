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
  const { text } = usePreferences();
  const [scenario, setScenario] =
    useState<EmailPreviewInput["scenario"]>("warning");
  const [requestKey] = useState(() => crypto.randomUUID());
  const [attempted, setAttempted] = useState(false);
  const action = useQuotaAction(onAuthBlocked);
  const title =
    kind === "test"
      ? text("确认合成测试发送", "Confirm synthetic test send")
      : kind === "enable"
        ? text("确认启用邮件目标", "Confirm enabling email target")
        : text("确认停用邮件目标", "Confirm disabling email target");
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && (
        <ErrorBanner
          error={safeEmailError(
            action.error,
            text,
            kind === "test" ? "test" : "save",
          )}
        />
      )}
      <Card>
        <CardHeader title={title} />
        <div className="grid min-w-0 gap-5 p-5">
          <dl className="grid min-w-0 gap-2 text-sm">
            <dt className="text-fg-tertiary">{text("目标", "Target")}</dt>
            <dd className="break-words font-semibold">{target.name}</dd>
            <dt className="text-fg-tertiary">
              {text("标识与确认版本", "Identity and confirmed version")}
            </dt>
            <dd className="break-all font-mono text-xs">
              {target.id} · {target.version}
            </dd>
            <dt className="text-fg-tertiary">
              {text("邮件语言与收件人", "Email language and recipient")}
            </dt>
            <dd>
              {target.locale === "en" ? "English" : "简体中文"} ·{" "}
              {text(
                "已配置的单个收件人，地址不可读取",
                "One configured recipient; the address cannot be read",
              )}
            </dd>
          </dl>
          {kind === "test" ? (
            <>
              <Field label={text("合成场景", "Synthetic scenario")}>
                <Select
                  aria-label={text("合成场景", "Synthetic scenario")}
                  value={scenario}
                  disabled={attempted}
                  onChange={setScenario}
                  options={["warning", "critical", "resolved"].map((value) => ({
                    value,
                    label: quotaLabel(value, text),
                  }))}
                />
              </Field>
              <Notice
                tone="warning"
                closable={false}
                title={text(
                  "确认将向此目标配置的收件人发送一封邮件",
                  "Confirmation sends one email to this target's configured recipient",
                )}
                description={text(
                  "数据是合成样例。请求会入真实交付队列并受全局限频；取消不能撤回已入队或正在发送的邮件。SMTP 接受不等于收件箱送达。",
                  "The data is synthetic. The request enters the real delivery queue and is rate limited. Cancelling cannot recall queued or in-flight mail. SMTP acceptance does not mean inbox delivery.",
                )}
              />
              {attempted && Boolean(action.error) && (
                <p className="text-sm text-fg-tertiary">
                  {text(
                    "重试沿用本次目标、版本、场景和测试标识，不会自动创建另一次测试。",
                    "Retry keeps this target, version, scenario, and test key; it does not automatically create another test.",
                  )}
                </p>
              )}
            </>
          ) : (
            <Notice
              tone="warning"
              closable={false}
              title={
                kind === "enable"
                  ? text("启用不发送测试邮件", "Enabling sends no test email")
                  : text(
                      "停用不能撤回正在发送的邮件",
                      "Disabling cannot recall in-flight mail",
                    )
              }
              description={text(
                "目标版本会变更。已有配额规则绑定旧版本，需要在规则页重新确认路由；队列中的旧版本邮件会停止交付。",
                "The target version changes. Existing quota rules bind the old version and require route confirmation on the rules page; queued old-version mail will stop.",
              )}
            />
          )}
          <div className="flex flex-wrap gap-2">
            <Button aria-label={text("取消", "Cancel")} onClick={onCancel}>
              {text("取消", "Cancel")}
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
                ? text("确认发送合成测试", "Confirm synthetic test send")
                : kind === "enable"
                  ? text("确认启用", "Confirm enable")
                  : text("确认停用", "Confirm disable")}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
