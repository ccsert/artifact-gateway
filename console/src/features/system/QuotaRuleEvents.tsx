import { useState } from "react";
import { Button } from "antd";
import { getEmailDelivery, listRepositoryQuotaAlertEvents } from "../../client";
import { QuotaBadge as Badge } from "./QuotaBadge";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import {
  percent,
  quotaLabel,
  safeQuotaError,
  sampleBytes,
} from "./quotaAlertPresentation";
import { quotaResult, useQuotaSnapshot } from "./useQuotaSnapshot";

export function QuotaEvents({
  ruleId,
  onAuthBlocked,
}: {
  ruleId: string;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { t } = usePreferences();
  const events = useQuotaSnapshot(
    async (signal) =>
      quotaResult(
        await listRepositoryQuotaAlertEvents({ path: { ruleId }, signal }),
      ),
    ruleId,
    onAuthBlocked,
  );
  const [delivery, setDelivery] = useState<string>();
  return (
    <div className="ag-page-stack">
      {Boolean(events.error) && events.data && (
        <ErrorBanner
          tone="warning"
          title={t("system.eventRefreshFailedShowingPreviouslyReadData")}
          error={safeQuotaError(events.error, t)}
          onRetry={() => void events.refresh()}
        />
      )}
      <Card>
        <CardHeader
          title={t("system.latest50Events")}
          extra={
            <Button
              size="small"
              loading={events.loading}
              onClick={() => void events.refresh()}
            >
              {t("system.refreshEvents")}
            </Button>
          }
        />
        {!events.data ? (
          events.error ? (
            <div className="p-5">
              <ErrorBanner
                error={safeQuotaError(events.error, t)}
                onRetry={() => void events.refresh()}
              />
            </div>
          ) : (
            <Loading />
          )
        ) : events.data.length === 0 ? (
          <EmptyState
            title={t("system.noAlertEventsYet")}
            hint={t("system.disabledRulesAndUnknownOrStaleDataDo")}
          />
        ) : (
          <ol className="divide-y divide-line">
            {events.data.map((event) => (
              <li key={event.id} className="grid min-w-0 gap-3 px-5 py-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge
                      tone={
                        event.scenario === "critical"
                          ? "danger"
                          : event.scenario === "warning"
                            ? "warning"
                            : "success"
                      }
                    >
                      {quotaLabel(event.scenario, t)}
                    </Badge>
                    <span className="text-xs text-fg-tertiary">
                      #{event.sequence} ·{" "}
                      {new Date(event.occurredAt).toLocaleString()}
                    </span>
                  </div>
                  <Badge>
                    {quotaLabel(
                      event.deliveryState ?? event.notificationCode,
                      t,
                    )}
                  </Badge>
                </div>
                <p className="break-words text-sm text-fg-secondary">
                  {t("system.eventSample")}: {sampleBytes(event.usedBytes, t)} /{" "}
                  {sampleBytes(event.quotaBytes, t)} · {t("system.sampled")}{" "}
                  {new Date(event.sampleAt).toLocaleString()}
                </p>
                <p className="text-xs text-fg-tertiary">
                  {t("system.eventThresholds")}:{" "}
                  {percent(event.policy.warningBasisPoints)} /{" "}
                  {percent(event.policy.criticalBasisPoints)} /{" "}
                  {percent(event.policy.recoveryBelowBasisPoints)} ·{" "}
                  {t("system.evidenceSince")}{" "}
                  {new Date(event.evidenceSince).toLocaleString()}
                </p>
                {event.deliveryErrorCode && (
                  <p className="text-xs text-fg-tertiary">
                    {quotaLabel(event.deliveryErrorCode, t)}
                  </p>
                )}
                {event.deliveryId && (
                  <div>
                    <Button
                      size="small"
                      onClick={() =>
                        setDelivery(
                          delivery === event.deliveryId
                            ? undefined
                            : event.deliveryId,
                        )
                      }
                    >
                      {delivery === event.deliveryId
                        ? t("system.hideDeliveryDetails")
                        : t("system.viewDeliveryDetails")}
                    </Button>
                  </div>
                )}
                {event.deliveryId && delivery === event.deliveryId && (
                  <QuotaDelivery
                    key={delivery}
                    deliveryId={delivery}
                    onAuthBlocked={onAuthBlocked}
                  />
                )}
              </li>
            ))}
          </ol>
        )}
      </Card>
    </div>
  );
}

function QuotaDelivery({
  deliveryId,
  onAuthBlocked,
}: {
  deliveryId: string;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { t } = usePreferences();
  const delivery = useQuotaSnapshot(
    async (signal) =>
      quotaResult(await getEmailDelivery({ path: { deliveryId }, signal })),
    deliveryId,
    onAuthBlocked,
  );
  if (!delivery.data)
    return delivery.error ? (
      <ErrorBanner
        error={safeQuotaError(delivery.error, t)}
        onRetry={() => void delivery.refresh()}
      />
    ) : (
      <Loading />
    );
  const item = delivery.data;
  return (
    <section
      className="grid min-w-0 gap-3 border-t border-line pt-4"
      aria-label={t("system.emailDeliveryDetails")}
    >
      {Boolean(delivery.error) && (
        <ErrorBanner
          tone="warning"
          title={t("system.deliveryDetailsAreOutOfDate")}
          error={safeQuotaError(delivery.error, t)}
          onRetry={() => void delivery.refresh()}
        />
      )}
      <p className="text-sm text-fg-secondary">
        {quotaLabel(item.state, t)} · {t("system.attempts")}: {item.attempts}
      </p>
      {item.state === "accepted" && (
        <p className="text-sm text-fg-tertiary">
          {t("system.sMTPAcceptedOnlyMeansTheServerAcceptedThe")}
        </p>
      )}
      {item.possibleDuplicate && (
        <Notice
          tone="warning"
          title={t("system.possibleDuplicate")}
          description={t("system.anEarlierDeliveryOutcomeWasUncertainARetry")}
        />
      )}
      {item.errorCode && (
        <p className="text-sm text-fg-secondary">
          {quotaLabel(item.errorCode, t)}
        </p>
      )}
      {item.automaticCancellationCode && (
        <p className="text-sm text-fg-secondary">
          {quotaLabel(item.automaticCancellationCode, t)}
        </p>
      )}
      {item.nextAttemptAt && (
        <p className="text-xs text-fg-tertiary">
          {t("system.nextAttempt")}:{" "}
          {new Date(item.nextAttemptAt).toLocaleString()}
        </p>
      )}
      {item.acceptedAt && (
        <p className="text-xs text-fg-tertiary">
          {t("system.acceptedAt")}: {new Date(item.acceptedAt).toLocaleString()}
        </p>
      )}
      <p className="text-xs text-fg-tertiary">
        {t("system.template")}: {item.templateVersion} ·{" "}
        {item.locale === "en" ? "English" : "中文"}
      </p>
    </section>
  );
}
