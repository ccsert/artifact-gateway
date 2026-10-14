import type { EmailDelivery, EmailTarget } from "../../client";
import { Card, CardHeader } from "../../components/ui/Layout";
import { EmptyState } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { QuotaBadge } from "./QuotaBadge";
import { quotaLabel } from "./quotaAlertPresentation";

export function EmailDeliveryResults({
  deliveries,
  targets,
}: {
  deliveries: EmailDelivery[];
  targets: EmailTarget[];
}) {
  const { t } = usePreferences();
  const time = (value?: string) =>
    value
      ? new Date(value).toLocaleString(undefined, { timeZone: "UTC" }) + " UTC"
      : t("system.unknown");
  return (
    <Card>
      <CardHeader title={t("system.recentDeliveryResultsUpTo50")} />
      <p className="px-5 pt-4 text-sm text-fg-tertiary">
        {t("system.sMTPAcceptanceDoesNotMeanInboxDeliveryOr")}
      </p>
      {deliveries.length === 0 ? (
        <EmptyState title={t("system.noEmailDeliveriesYet")} />
      ) : (
        <ul className="divide-y divide-line">
          {deliveries.map((delivery) => (
            <li className="grid min-w-0 gap-3 px-5 py-4" key={delivery.id}>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h3 className="break-words text-sm font-semibold">
                  {targets.find((v) => v.id === delivery.targetId)?.name ??
                    delivery.targetId}
                </h3>
                <QuotaBadge
                  tone={
                    delivery.state === "dead"
                      ? "danger"
                      : delivery.state === "accepted"
                        ? "success"
                        : "neutral"
                  }
                >
                  {quotaLabel(delivery.state, t)}
                </QuotaBadge>
              </div>
              <p className="text-sm text-fg-tertiary">
                {delivery.kind === "repository_quota"
                  ? t("system.repositoryQuotaAlert")
                  : t("system.administratorSyntheticTest")}{" "}
                · {quotaLabel(delivery.scenario, t)} · {delivery.locale} ·{" "}
                {t("system.attempts")}: {delivery.attempts}
              </p>
              <p className="break-all font-mono text-xs text-fg-tertiary">
                {t("system.deliveryID")}: {delivery.id}
              </p>
              <p className="text-xs text-fg-tertiary">
                {t("system.updated")}: {time(delivery.updatedAt)}
                {delivery.nextAttemptAt && (
                  <>
                    {" "}
                    · {t("system.nextAttempt2")}: {time(delivery.nextAttemptAt)}
                  </>
                )}
                {delivery.acceptedAt && (
                  <>
                    {" "}
                    · {t("system.accepted")}: {time(delivery.acceptedAt)}
                  </>
                )}
              </p>
              {delivery.errorCode && (
                <p className="text-sm text-fg-secondary">
                  {quotaLabel(delivery.errorCode, t)}
                </p>
              )}
              {delivery.automaticCancellationCode && (
                <p className="text-sm text-fg-secondary">
                  {quotaLabel(delivery.automaticCancellationCode, t)}
                </p>
              )}
              {delivery.possibleDuplicate && (
                <p className="text-sm text-[var(--ag-status-warning)]">
                  {t("system.possibleDuplicateAnEarlierSMTPAttemptHadAn")}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
