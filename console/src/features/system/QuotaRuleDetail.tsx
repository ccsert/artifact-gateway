import { useState } from "react";
import { Button } from "antd";
import {
  deleteRepositoryQuotaAlertRule,
  updateRepositoryQuotaAlertRule,
  type EmailCapability,
  type EmailTarget,
  type RepositoryQuotaAlertRule,
} from "../../client";
import { QuotaBadge as Badge } from "./QuotaBadge";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import {
  percent,
  quotaLabel,
  safeQuotaError,
  sampleBytes,
  isQuotaAuthError,
} from "./quotaAlertPresentation";
import { quotaResult } from "./useQuotaSnapshot";
import { useQuotaAction } from "./useQuotaAction";
import { QuotaEvents } from "./QuotaRuleEvents";

export function QuotaRuleDetail({
  rule,
  repositoryName,
  targets,
  capability,
  onEdit,
  onChanged,
  onDeleted,
  writable,
  onAuthBlocked,
}: {
  rule: RepositoryQuotaAlertRule;
  repositoryName: string;
  targets: EmailTarget[];
  capability: EmailCapability;
  writable: boolean;
  onAuthBlocked: (error: unknown) => void;
  onEdit: () => void;
  onChanged: (rule: RepositoryQuotaAlertRule) => void;
  onDeleted: () => void;
}) {
  const { t } = usePreferences();
  const [confirmation, setConfirmation] = useState<{
    kind: "enable" | "disable" | "delete";
    rule: RepositoryQuotaAlertRule;
  }>();
  const action = useQuotaAction(onAuthBlocked);
  const target = targets.find((item) => item.id === rule.targetId);
  const ready =
    capability.enabled &&
    capability.reason === "ready" &&
    target?.enabled &&
    target.recipientConfigured &&
    target.version === rule.targetVersion;
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !isQuotaAuthError(action.error) && (
        <ErrorBanner error={safeQuotaError(action.error, t)} />
      )}
      {confirmation && (
        <Card bodyClassName="grid gap-4 p-5">
          <h3 className="text-sm font-semibold text-fg-strong">
            {confirmation.kind === "enable"
              ? t("system.confirmEnablingThisRule")
              : confirmation.kind === "disable"
                ? t("system.confirmDisablingThisRule")
                : t("system.confirmDeletingThisRule")}
          </h3>
          <p className="break-words text-sm text-fg-secondary">
            {repositoryName} · {target?.name ?? rule.targetId} ·{" "}
            {target?.locale === "en"
              ? "English"
              : target?.locale === "zh-CN"
                ? "中文"
                : t("system.localeUnknown")}
          </p>
          <p className="text-sm text-fg-secondary">
            {confirmation.kind === "enable"
              ? t("system.enablingMaySendEmailToThisTargetFor")
              : confirmation.kind === "disable"
                ? t("system.disablingStopsNewEvaluationsAndRetainsSeverityAnd")
                : t(
                    "system.deletionStopsNewEvaluationsAndRetainsHistoryWithout",
                  )}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              type="primary"
              danger={confirmation.kind === "delete"}
              loading={action.busy}
              disabled={
                !writable ||
                action.blocked ||
                (confirmation.kind === "enable" && !ready)
              }
              onClick={() => {
                const captured = confirmation.rule;
                void action.run(
                  async (signal) => {
                    if (confirmation.kind === "delete") {
                      const result = await deleteRepositoryQuotaAlertRule({
                        path: { ruleId: captured.id },
                        headers: { "If-Match": captured.version },
                        signal,
                      });
                      if (result.error || result.response?.status !== 204)
                        throw result.error ?? new Error("unavailable");
                      return;
                    }
                    return quotaResult(
                      await updateRepositoryQuotaAlertRule({
                        path: { ruleId: captured.id },
                        headers: { "If-Match": captured.version },
                        body: {
                          repositoryId: captured.repositoryId,
                          targetId: captured.targetId,
                          policy: captured.policy,
                          enabled: confirmation.kind === "enable",
                        },
                        signal,
                      }),
                    );
                  },
                  (next) => {
                    setConfirmation(undefined);
                    if (next) onChanged(next);
                    else onDeleted();
                  },
                );
              }}
            >
              {confirmation.kind === "enable"
                ? t("system.confirmEnable")
                : confirmation.kind === "disable"
                  ? t("system.confirmDisable")
                  : t("system.confirmDelete")}
            </Button>
            <Button
              aria-label={t("system.cancel")}
              disabled={action.busy}
              onClick={() => setConfirmation(undefined)}
            >
              {t("system.cancel")}
            </Button>
          </div>
        </Card>
      )}
      <Card>
        <CardHeader title={t("system.ruleState")} />
        <div className="grid min-w-0 gap-4 p-5">
          <h3 className="break-words text-base font-semibold text-fg-strong">
            {repositoryName}
          </h3>
          <div className="flex flex-wrap gap-2">
            <Badge>
              {rule.deleted
                ? quotaLabel("deleted", t)
                : rule.enabled
                  ? t("system.enabled")
                  : quotaLabel("disabled", t)}
            </Badge>
            <Badge
              tone={
                rule.state.severity === "critical"
                  ? "danger"
                  : rule.state.severity === "warning"
                    ? "warning"
                    : "neutral"
              }
            >
              {quotaLabel(rule.state.severity, t)}
            </Badge>
            <Badge>{quotaLabel(rule.state.dataState, t)}</Badge>
          </div>
          {rule.state.dataState !== "available" && (
            <p className="text-sm text-fg-tertiary">
              {t(
                "system.severityIsRetainedCurrentEvidenceCannotDetermineWhether",
              )}
            </p>
          )}
          {rule.state.lastSampleAt ? (
            <p className="break-words text-sm text-fg-secondary">
              {rule.state.dataState === "available"
                ? t("system.latestSample")
                : t("system.historicalSample")}{" "}
              ({new Date(rule.state.lastSampleAt).toLocaleString()}):{" "}
              {sampleBytes(rule.state.usedBytes, t)} /{" "}
              {sampleBytes(rule.state.quotaBytes, t)}
              {((rule.state.usedBytes !== undefined &&
                !Number.isSafeInteger(rule.state.usedBytes)) ||
                (rule.state.quotaBytes !== undefined &&
                  !Number.isSafeInteger(rule.state.quotaBytes))) &&
                ` · ${t("system.byteValuesAreApproximateSeverityComesFromThe")}`}
            </p>
          ) : (
            <p className="text-sm text-fg-tertiary">
              {t("system.noValidCapacitySampleYet")}
            </p>
          )}
          <dl className="grid min-w-0 gap-3 text-sm sm:grid-cols-2">
            {[
              [
                t("system.warningCondition"),
                `${t("system.atOrAbove")} ${percent(rule.policy.warningBasisPoints)} · ${rule.policy.warningForSeconds} ${t("system.seconds")}`,
              ],
              [
                t("system.criticalCondition"),
                `${t("system.atOrAbove")} ${percent(rule.policy.criticalBasisPoints)} · ${rule.policy.criticalForSeconds} ${t("system.seconds")}`,
              ],
              [
                t("system.recoveryCondition"),
                `${t("system.strictlyBelow")} ${percent(rule.policy.recoveryBelowBasisPoints)} · ${rule.policy.recoveryForSeconds} ${t("system.seconds")}`,
              ],
              [
                t("system.maximumSampleAge"),
                `${rule.policy.maxSampleAgeSeconds} ${t("system.seconds")}`,
              ],
              [
                t("system.lastEvaluation"),
                rule.evaluatedAt
                  ? new Date(rule.evaluatedAt).toLocaleString()
                  : t("system.notEvaluatedYet"),
              ],
            ].map(([label, value]) => (
              <div key={label} className="min-w-0">
                <dt className="text-xs text-fg-tertiary">{label}</dt>
                <dd className="mt-1 break-words text-fg-secondary">{value}</dd>
              </div>
            ))}
          </dl>
          {(
            [
              ["warningSince", t("system.warningEvidenceSince")],
              ["criticalSince", t("system.criticalEvidenceSince")],
              ["recoverySince", t("system.recoveryEvidenceSince")],
            ] as const
          ).map(
            ([key, label]) =>
              rule.state[key] && (
                <p key={key} className="text-xs text-fg-tertiary">
                  {label}: {new Date(rule.state[key]).toLocaleString()}
                </p>
              ),
          )}
          <div className="grid gap-2 border-t border-line pt-4">
            <p className="break-words text-sm text-fg-secondary">
              {t("system.notificationTarget")}: {target?.name ?? rule.targetId}{" "}
              ·{" "}
              {target?.locale === "en"
                ? "English"
                : target?.locale === "zh-CN"
                  ? "中文"
                  : t("system.localeUnknown")}{" "}
              ·{" "}
              {target?.recipientConfigured
                ? t("system.recipientConfigured")
                : t("system.recipientMissingOrTargetUnavailable")}
            </p>
            <p className="text-sm text-fg-secondary">
              {t("system.targetState")}:{" "}
              {target
                ? target.enabled
                  ? t("system.enabled")
                  : t("system.disabled")
                : t("system.unavailable")}
            </p>
            <p className="break-all text-xs text-fg-tertiary">
              {t("system.boundTargetVersion")}: {rule.targetVersion}
            </p>
            {target && target.version !== rule.targetVersion && (
              <div className="grid gap-2">
                <p className="break-all text-xs text-fg-tertiary">
                  {t("system.currentTargetVersion")}: {target.version}
                </p>
                <p className="text-sm text-fg-tertiary">
                  {t("system.theTargetVersionChangedEditAndSaveTo")}
                </p>
              </div>
            )}
            {target && !target.enabled && (
              <p className="text-sm text-fg-tertiary">
                {quotaLabel("target_disabled", t)}
              </p>
            )}
            {!rule.deleted && (
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={
                    !writable ||
                    action.busy ||
                    action.blocked ||
                    Boolean(confirmation)
                  }
                  onClick={onEdit}
                >
                  {t("system.editRule")}
                </Button>
                <Button
                  disabled={
                    !writable ||
                    action.busy ||
                    action.blocked ||
                    Boolean(confirmation) ||
                    (!rule.enabled && !ready)
                  }
                  onClick={() =>
                    setConfirmation({
                      kind: rule.enabled ? "disable" : "enable",
                      rule,
                    })
                  }
                >
                  {rule.enabled
                    ? t("system.disableRule")
                    : t("system.enableRule")}
                </Button>
                <Button
                  danger
                  disabled={
                    !writable ||
                    action.busy ||
                    action.blocked ||
                    Boolean(confirmation)
                  }
                  onClick={() => setConfirmation({ kind: "delete", rule })}
                >
                  {t("system.deleteRule")}
                </Button>
              </div>
            )}
          </div>
        </div>
      </Card>
      <QuotaEvents
        key={rule.id}
        ruleId={rule.id}
        onAuthBlocked={onAuthBlocked}
      />
    </div>
  );
}
