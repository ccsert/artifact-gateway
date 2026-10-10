import { useId, useState } from "react";
import { Button, Input, Select } from "antd";
import {
  createEmailTarget,
  updateEmailTarget,
  getEmailTarget,
  type EmailTarget,
} from "../../client";
import { Card, CardHeader, Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import {
  fieldFeedback,
  useFormValidationFocus,
} from "../../components/ui/formFeedback";
import { usePreferences } from "../../lib/preferences";
import { useQuotaAction } from "./useQuotaAction";
import { quotaResult } from "./useQuotaSnapshot";
import {
  emailConflict,
  safeEmailError,
  emailTargetErrors,
} from "./emailPresentation";

export function EmailTargetEditor({
  target,
  writable,
  onCancel,
  onSaved,
  onAuthBlocked,
}: {
  target?: EmailTarget;
  writable: boolean;
  onCancel: () => void;
  onSaved: () => void;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { text, locale: uiLocale } = usePreferences();
  const [current, setCurrent] = useState(target);
  const [name, setName] = useState(target?.name ?? "");
  const [locale, setLocale] = useState<"en" | "zh-CN">(
    target?.locale ?? (uiLocale === "zh-CN" ? "zh-CN" : "en"),
  );
  const [recipient, setRecipient] = useState("");
  const baseId = useId();
  const { formRef, attempted, reportInvalid } = useFormValidationFocus();
  const errors = attempted ? emailTargetErrors(name, recipient, !current) : {};
  const nameError =
    errors.name === "required"
      ? text("请输入目标名称。", "Enter a target name.")
      : errors.name
        ? text(
            "名称须为 1–128 字，不能包含换行或空字符。",
            "Use a 1–128 character name without line breaks or null characters.",
          )
        : undefined;
  const recipientError =
    errors.recipient === "required"
      ? text("请输入单个收件人邮箱地址。", "Enter one recipient email address.")
      : errors.recipient
        ? text(
            "请输入单个有效邮箱，不含显示名、分隔符或换行。",
            "Enter one valid mailbox without a display name, separators, or line breaks.",
          )
        : undefined;
  const action = useQuotaAction(onAuthBlocked);
  const reload = useQuotaAction(onAuthBlocked);
  const [conflict, setConflict] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !acknowledged && (
        <ErrorBanner error={safeEmailError(action.error, text, "save")} />
      )}
      {Boolean(reload.error) && (
        <ErrorBanner error={safeEmailError(reload.error, text, "read")} />
      )}
      <Card>
        <CardHeader
          title={
            current
              ? text("编辑邮件目标", "Edit email target")
              : text("新建停用邮件目标", "Create disabled email target")
          }
        />
        <form
          ref={formRef}
          className="grid min-w-0 gap-5 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!writable || action.busy || conflict || reload.busy) return;
            if (
              Object.keys(emailTargetErrors(name, recipient, !current)).length
            ) {
              reportInvalid();
              return;
            }
            setAcknowledged(false);
            const body = {
              name: name.trim(),
              locale,
              enabled: current?.enabled ?? false,
              ...(recipient ? { recipient } : {}),
            };
            void action.run(
              async (signal) => {
                try {
                  return quotaResult(
                    current
                      ? await updateEmailTarget({
                          path: { targetId: current.id },
                          headers: { "If-Match": current.version },
                          body,
                          signal,
                        })
                      : await createEmailTarget({ body, signal }),
                  );
                } catch (error) {
                  if (emailConflict(error)) {
                    setRecipient("");
                    setConflict(true);
                  }
                  throw error;
                }
              },
              () => {
                setRecipient("");
                onSaved();
              },
            );
          }}
        >
          <Field
            id={`${baseId}-name`}
            error={nameError}
            label={text("目标名称", "Target name")}
            hint={text(
              "使用团队或用途名称，不要把邮箱地址写入名称。",
              "Use a team or purpose name; do not put a mailbox in the name.",
            )}
          >
            <Input
              {...fieldFeedback(`${baseId}-name`, nameError, true)}
              aria-label={text("目标名称", "Target name")}
              value={name}
              disabled={action.busy || reload.busy}
              autoComplete="off"
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={text("邮件语言", "Email language")}>
            <Select
              aria-label={text("邮件语言", "Email language")}
              value={locale}
              disabled={action.busy || reload.busy}
              onChange={setLocale}
              options={[
                { value: "zh-CN", label: "简体中文" },
                { value: "en", label: "English" },
              ]}
            />
          </Field>
          <Field
            id={`${baseId}-recipient`}
            error={recipientError}
            label={text("收件人地址", "Recipient address")}
            hint={
              current
                ? text(
                    "旧地址只写不可读。留空保留，输入新地址替换；取消或冲突会清除输入。",
                    "The existing address is write-only. Leave empty to retain it or enter a replacement; cancel or conflict clears the input.",
                  )
                : text(
                    "只保存一个收件人，地址不会回显。新目标默认停用。",
                    "One recipient only; its address is never returned. New targets are disabled.",
                  )
            }
          >
            <Input
              {...fieldFeedback(`${baseId}-recipient`, recipientError, true)}
              aria-label={text("收件人地址", "Recipient address")}
              value={recipient}
              disabled={action.busy || reload.busy || conflict}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setRecipient(e.target.value)}
            />
          </Field>
          <p className="text-sm text-fg-tertiary">
            {current
              ? text(
                  `当前目标${current.enabled ? "已启用" : "已停用"}；保存保留该状态，不发送测试。`,
                  `The target is ${current.enabled ? "enabled" : "disabled"}; saving retains this state and sends no test.`,
                )
              : text(
                  "保存不会启用目标或发送邮件。",
                  "Saving does not enable the target or send mail.",
                )}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              aria-label={text("取消", "Cancel")}
              onClick={() => {
                setRecipient("");
                onCancel();
              }}
            >
              {text("取消", "Cancel")}
            </Button>
            {conflict && current && (
              <Button
                loading={reload.busy}
                onClick={() =>
                  void reload.run(
                    async (signal) =>
                      quotaResult(
                        await getEmailTarget({
                          path: { targetId: current.id },
                          signal,
                        }),
                      ),
                    (next) => {
                      setCurrent(next);
                      setRecipient("");
                      setConflict(false);
                      setAcknowledged(true);
                    },
                  )
                }
              >
                {text(
                  "刷新最新目标并重新编辑",
                  "Refresh latest target and edit again",
                )}
              </Button>
            )}
            <Button
              type="primary"
              htmlType="submit"
              loading={action.busy}
              disabled={!writable || conflict || reload.busy}
            >
              {current
                ? text("保存目标", "Save target")
                : text("保存停用目标", "Save disabled target")}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
