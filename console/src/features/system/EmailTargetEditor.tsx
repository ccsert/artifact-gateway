import { useState } from "react";
import { Button, Input, Select } from "antd";
import {
  createEmailTarget,
  updateEmailTarget,
  getEmailTarget,
  type EmailTarget,
} from "../../client";
import { Card, CardHeader, Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { useQuotaAction } from "./useQuotaAction";
import { quotaResult } from "./useQuotaSnapshot";
import {
  emailConflict,
  safeEmailError,
  validEmailTarget,
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
  const [invalid, setInvalid] = useState(false);
  const action = useQuotaAction(onAuthBlocked);
  const reload = useQuotaAction(onAuthBlocked);
  const [conflict, setConflict] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !acknowledged && (
        <ErrorBanner error={safeEmailError(action.error, text)} />
      )}
      {Boolean(reload.error) && (
        <ErrorBanner error={safeEmailError(reload.error, text)} />
      )}
      {invalid && (
        <ErrorBanner
          error={text(
            "请填写 1–128 字目标名称及单个有效邮箱地址，不能包含换行。编辑时地址留空保留原值。",
            "Enter a 1–128 character name and one valid mailbox without line breaks. Leave the address empty when editing to retain it.",
          )}
        />
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
          className="grid min-w-0 gap-5 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!writable || action.busy || conflict || reload.busy) return;
            if (!validEmailTarget(name, recipient, !current)) {
              setInvalid(true);
              return;
            }
            setInvalid(false);
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
            label={text("目标名称", "Target name")}
            hint={text(
              "使用团队或用途名称，不要把邮箱地址写入名称。",
              "Use a team or purpose name; do not put a mailbox in the name.",
            )}
          >
            <Input
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
              aria-label={text("收件人地址", "Recipient address")}
              value={recipient}
              disabled={action.busy || reload.busy || conflict}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setRecipient(e.target.value)}
            />
          </Field>
          <p className="text-sm text-zinc-500">
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
