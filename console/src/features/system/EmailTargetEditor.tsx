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
import { quotaKeys } from "../../lib/query/keys";
import { isQuotaAuthError } from "./quotaAlertPresentation";
import { useExplicitRead } from "../../lib/query/useExplicitRead";
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
  const { t, locale: uiLocale } = usePreferences();
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
      ? t("system.enterATargetName")
      : errors.name
        ? t("system.useA1128CharacterNameWithoutLine")
        : undefined;
  const recipientError =
    errors.recipient === "required"
      ? t("system.enterOneRecipientEmailAddress")
      : errors.recipient
        ? t("system.enterOneValidMailboxWithoutADisplayName")
        : undefined;
  const action = useQuotaAction(onAuthBlocked);
  // After a version conflict the person reloads the target explicitly and
  // confirms again; the read goes through the query layer.
  const reload = useExplicitRead(
    quotaKeys.emailTarget(current?.id ?? ""),
    async (signal) =>
      quotaResult(
        await getEmailTarget({
          path: { targetId: current?.id ?? "" },
          signal,
        }),
      ),
    (error) => {
      if (isQuotaAuthError(error)) onAuthBlocked(error);
    },
  );
  const [conflict, setConflict] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !acknowledged && (
        <ErrorBanner error={safeEmailError(action.error, t, "save")} />
      )}
      {Boolean(reload.error) && (
        <ErrorBanner error={safeEmailError(reload.error, t, "read")} />
      )}
      <Card>
        <CardHeader
          title={
            current
              ? t("system.editEmailTarget")
              : t("system.createDisabledEmailTarget")
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
            label={t("system.targetName")}
            hint={t("system.useATeamOrPurposeNameDoNot")}
          >
            <Input
              {...fieldFeedback(`${baseId}-name`, nameError, true)}
              aria-label={t("system.targetName")}
              value={name}
              disabled={action.busy || reload.busy}
              autoComplete="off"
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={t("system.emailLanguage")}>
            <Select
              aria-label={t("system.emailLanguage")}
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
            label={t("system.recipientAddress")}
            hint={
              current
                ? t("system.theExistingAddressIsWriteOnlyLeaveEmpty")
                : t("system.oneRecipientOnlyItsAddressIsNeverReturned")
            }
          >
            <Input
              {...fieldFeedback(`${baseId}-recipient`, recipientError, true)}
              aria-label={t("system.recipientAddress")}
              value={recipient}
              disabled={action.busy || reload.busy || conflict}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setRecipient(e.target.value)}
            />
          </Field>
          <p className="text-sm text-fg-tertiary">
            {current
              ? t(
                  current.enabled
                    ? "system.theTargetIsEnabledSavingRetainsThisState"
                    : "system.theTargetIsDisabledSavingRetainsThisState",
                )
              : t("system.savingDoesNotEnableTheTargetOrSend")}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              aria-label={t("system.cancel")}
              onClick={() => {
                setRecipient("");
                onCancel();
              }}
            >
              {t("system.cancel")}
            </Button>
            {conflict && current && (
              <Button
                loading={reload.busy}
                onClick={() =>
                  void reload.run((next) => {
                    setCurrent(next);
                    setRecipient("");
                    setConflict(false);
                    setAcknowledged(true);
                  })
                }
              >
                {t("system.refreshLatestTargetAndEditAgain")}
              </Button>
            )}
            <Button
              type="primary"
              htmlType="submit"
              loading={action.busy}
              disabled={!writable || conflict || reload.busy}
            >
              {current
                ? t("system.saveTarget")
                : t("system.saveDisabledTarget")}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
