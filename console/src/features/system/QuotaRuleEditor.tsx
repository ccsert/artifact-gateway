import type { MessageKey } from "../../messages";
import { useId, useState } from "react";
import { Button, Input, Select } from "antd";
import {
  createRepositoryQuotaAlertRule,
  updateRepositoryQuotaAlertRule,
  listRepositories,
  type EmailTarget,
  type Repository,
  type RepositoryQuotaAlertPolicy,
  type RepositoryQuotaAlertRule,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import {
  Card,
  CardHeader,
  Field,
  Pagination,
} from "../../components/ui/Layout";
import {
  fieldFeedback,
  useFormValidationFocus,
} from "../../components/ui/formFeedback";
import { usePreferences } from "../../lib/preferences";
import {
  parsePolicy,
  policyDraft,
  policyErrorMessage,
  policyErrors,
  safeQuotaError,
  isQuotaAuthError,
} from "./quotaAlertPresentation";
import { quotaBlocked, useQuotaAction } from "./useQuotaAction";
import { useInfiniteQuery } from "@tanstack/react-query";
import { quotaKeys } from "../../lib/query/keys";
import { quotaResult } from "./useQuotaSnapshot";

export function QuotaRuleEditor({
  rule,
  repositories,
  targets,
  canSave,
  onCancel,
  onSaved,
  nextPageToken,
  onAuthBlocked,
}: {
  rule?: RepositoryQuotaAlertRule;
  repositories: Repository[];
  targets: EmailTarget[];
  canSave: boolean;
  onCancel: () => void;
  onSaved: (rule: RepositoryQuotaAlertRule) => void;
  nextPageToken?: string;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { t } = usePreferences();
  const [repositoryId, setRepositoryId] = useState(rule?.repositoryId ?? "");
  const [targetId, setTargetId] = useState(rule?.targetId ?? "");
  const [draft, setDraft] = useState(() => policyDraft(rule?.policy));
  const baseId = useId();
  const { formRef, attempted, reportInvalid } = useFormValidationFocus();
  const errors = attempted ? policyErrors(draft) : {};
  const errorFor = (key: keyof RepositoryQuotaAlertPolicy) =>
    policyErrorMessage(errors[key], t);
  const repositoryError =
    attempted && !repositoryId ? t("system.chooseARepository") : undefined;
  const targetError =
    attempted && !targetId ? t("system.chooseAnEmailTarget") : undefined;
  const action = useQuotaAction(onAuthBlocked);
  // One editor opening owns its catalogue and cursor. A panel refresh must
  // not discard loaded options or restart pagination under a selected draft.
  const [firstPage] = useState(() => ({ items: repositories, nextPageToken }));
  // Pages after the panel's first one, read only when asked for and dropped
  // with the editor; a denial blocks the panel like any other quota read.
  const more = useInfiniteQuery({
    queryKey: quotaKeys.editorRepositories(firstPage.nextPageToken ?? ""),
    queryFn: async ({ pageParam, signal }) => {
      try {
        return quotaResult(
          await listRepositories({
            signal,
            query: { pageSize: 200, pageToken: pageParam },
          }),
        );
      } catch (cause) {
        if (!signal.aborted && isQuotaAuthError(cause)) onAuthBlocked(cause);
        throw cause;
      }
    },
    initialPageParam: firstPage.nextPageToken,
    getNextPageParam: (page) => page.nextPageToken || undefined,
    enabled: false,
    gcTime: 0,
    retry: false,
    meta: { authFailure: "ignore" },
  });
  const loadedPages = more.data?.pages ?? [];
  const catalogue = {
    items: loadedPages.reduce(
      (items, page) => [
        ...items,
        ...page.items.filter(
          (repo) => !items.some((item) => item.id === repo.id),
        ),
      ],
      firstPage.items,
    ),
    nextPageToken: loadedPages.length
      ? loadedPages[loadedPages.length - 1].nextPageToken
      : firstPage.nextPageToken,
  };
  const moreError = more.isFetching ? undefined : (more.error ?? undefined);
  const moreBlocked = quotaBlocked(moreError, onAuthBlocked);
  const fields = (
    percentKey: keyof RepositoryQuotaAlertPolicy,
    holdKey: keyof RepositoryQuotaAlertPolicy,
    labels: { threshold: MessageKey; hold: MessageKey },
  ) => (
    <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
      {[
        {
          key: percentKey,
          label: t(labels.threshold),
          mode: "decimal" as const,
        },
        {
          key: holdKey,
          label: t(labels.hold),
          mode: "numeric" as const,
        },
      ].map((field) => (
        <Field
          key={field.key}
          id={`${baseId}-${field.key}`}
          error={errorFor(field.key)}
          label={field.label}
        >
          <Input
            {...fieldFeedback(`${baseId}-${field.key}`, errorFor(field.key))}
            aria-label={field.label}
            inputMode={field.mode}
            value={draft[field.key]}
            disabled={action.busy}
            autoComplete="off"
            onChange={(event) =>
              setDraft((old) => ({ ...old, [field.key]: event.target.value }))
            }
          />
        </Field>
      ))}
    </div>
  );
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !isQuotaAuthError(action.error) && (
        <ErrorBanner error={safeQuotaError(action.error, t)} />
      )}
      <Card>
        <CardHeader
          title={rule ? t("system.editRule") : t("system.createDisabledRule")}
        />
        <form
          ref={formRef}
          className="ag-quota-form grid min-w-0 gap-6 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!canSave || action.blocked || action.busy) return;
            const policy = parsePolicy(draft);
            if (!policy || !repositoryId || !targetId) {
              reportInvalid();
              return;
            }
            const body = {
              repositoryId,
              targetId,
              enabled: rule?.enabled ?? false,
              policy,
            };
            void action.run(
              async (signal) =>
                quotaResult(
                  rule
                    ? await updateRepositoryQuotaAlertRule({
                        path: { ruleId: rule.id },
                        headers: { "If-Match": rule.version },
                        body,
                        signal,
                      })
                    : await createRepositoryQuotaAlertRule({ body, signal }),
                ),
              onSaved,
            );
          }}
        >
          {catalogue.nextPageToken && (
            <div className="grid gap-3">
              <Pagination
                hasMore
                disabled={!canSave || moreBlocked || action.busy}
                loading={more.isFetchingNextPage || more.isFetching}
                onMore={() =>
                  // The first extra page is the query's first fetch.
                  void (more.data
                    ? more.fetchNextPage({ cancelRefetch: false })
                    : more.refetch())
                }
                label={t("system.loadMoreRepositories")}
              />
              {Boolean(moreError) && !isQuotaAuthError(moreError) && (
                <ErrorBanner error={safeQuotaError(moreError, t)} />
              )}
            </div>
          )}
          <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
            <Field
              id={`${baseId}-repository`}
              error={repositoryError}
              label={t("system.repository")}
              hint={t("system.repositoryScopeCannotBeChangedAfterCreation")}
            >
              <Select
                {...fieldFeedback(
                  `${baseId}-repository`,
                  repositoryError,
                  true,
                )}
                virtual={false}
                aria-label={t("system.repository")}
                style={{ width: "100%" }}
                value={repositoryId || undefined}
                disabled={Boolean(rule) || action.busy}
                onChange={setRepositoryId}
                placeholder={t("system.chooseARepository2")}
                options={[
                  ...catalogue.items
                    .filter(
                      (repo) =>
                        repo.state === "active" ||
                        repo.id === rule?.repositoryId,
                    )
                    .map((repo) => ({ value: repo.id, label: repo.name })),
                  ...(rule &&
                  !catalogue.items.some((repo) => repo.id === rule.repositoryId)
                    ? [{ value: rule.repositoryId, label: rule.repositoryId }]
                    : []),
                ]}
              />
            </Field>
            <Field
              id={`${baseId}-target`}
              error={targetError}
              label={t("system.emailTarget")}
              hint={t("system.onlyTheNameAndConfigurationStatusAreShown")}
            >
              <Select
                {...fieldFeedback(`${baseId}-target`, targetError, true)}
                virtual={false}
                aria-label={t("system.emailTarget")}
                style={{ width: "100%" }}
                value={targetId || undefined}
                disabled={action.busy}
                onChange={setTargetId}
                placeholder={t("system.chooseAnEmailTarget2")}
                options={targets.map((target) => ({
                  value: target.id,
                  disabled: Boolean(
                    rule?.enabled &&
                    (!target.enabled || !target.recipientConfigured),
                  ),
                  label: `${target.name} · ${target.locale === "en" ? "English" : "中文"} · ${target.enabled && target.recipientConfigured ? t("system.ready") : t("system.notReady")}`,
                }))}
              />
            </Field>
          </div>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-fg-strong">
              {t("system.triggerWarning")}
            </legend>
            {fields("warningBasisPoints", "warningForSeconds", {
              threshold: "system.warningThreshold",
              hold: "system.warningHoldDuration",
            })}
            <p className="text-xs text-fg-tertiary">
              {t("system.triggersAfterUsageReachesTheThresholdForThe")}
            </p>
          </fieldset>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-fg-strong">
              {t("system.escalateToCritical")}
            </legend>
            {fields("criticalBasisPoints", "criticalForSeconds", {
              threshold: "system.criticalThreshold",
              hold: "system.criticalHoldDuration",
            })}
          </fieldset>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-fg-strong">
              {t("system.recovery")}
            </legend>
            {fields("recoveryBelowBasisPoints", "recoveryForSeconds", {
              threshold: "system.recoveryThreshold",
              hold: "system.recoveryHoldDuration",
            })}
            <p className="text-xs text-fg-tertiary">
              {t("system.recoveryIsNotifiedAfterUsageStaysStrictlyBelow")}
            </p>
          </fieldset>
          <Field
            id={`${baseId}-maxSampleAgeSeconds`}
            error={errorFor("maxSampleAgeSeconds")}
            label={t("system.maximumSampleAgeSeconds")}
            hint={t("system.staleOrUnknownEvidenceInterruptsHoldTimingAnd")}
          >
            <Input
              {...fieldFeedback(
                `${baseId}-maxSampleAgeSeconds`,
                errorFor("maxSampleAgeSeconds"),
                true,
              )}
              aria-label={t("system.maximumSampleAgeSeconds")}
              inputMode="numeric"
              autoComplete="off"
              value={draft.maxSampleAgeSeconds}
              disabled={action.busy}
              onChange={(event) =>
                setDraft((old) => ({
                  ...old,
                  maxSampleAgeSeconds: event.target.value,
                }))
              }
            />
          </Field>
          <p className="text-sm text-fg-tertiary">
            {rule
              ? t("system.savingBindsTheCurrentTargetVersionResetsHold")
              : t("system.newRulesAreDisabledByDefaultAndSend")}
            {rule?.enabled &&
              ` ${t("system.thisRuleIsEnabledSavingContinuesEvaluationAnd")}`}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              htmlType="submit"
              type="primary"
              loading={action.busy}
              disabled={!canSave || action.blocked}
            >
              {rule ? t("system.saveRule") : t("system.createDisabledRule")}
            </Button>
            <Button
              aria-label={t("system.cancel")}
              disabled={action.busy}
              onClick={onCancel}
            >
              {t("system.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
