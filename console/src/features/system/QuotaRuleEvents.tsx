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
  const { text } = usePreferences();
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
          title={text(
            "事件刷新失败，保留上次读取的数据",
            "Event refresh failed; showing previously read data",
          )}
          error={safeQuotaError(events.error, text)}
          onRetry={() => void events.refresh()}
        />
      )}
      <Card>
        <CardHeader
          title={text("最近 50 条事件", "Latest 50 events")}
          extra={
            <Button
              size="small"
              loading={events.loading}
              onClick={() => void events.refresh()}
            >
              {text("刷新事件", "Refresh events")}
            </Button>
          }
        />
        {!events.data ? (
          events.error ? (
            <div className="p-5">
              <ErrorBanner
                error={safeQuotaError(events.error, text)}
                onRetry={() => void events.refresh()}
              />
            </div>
          ) : (
            <Loading />
          )
        ) : events.data.length === 0 ? (
          <EmptyState
            title={text("尚无告警事件", "No alert events yet")}
            hint={text(
              "停用、未知或陈旧数据不会生成恢复事件。",
              "Disabled rules and unknown or stale data do not generate recovery events.",
            )}
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
                      {quotaLabel(event.scenario, text)}
                    </Badge>
                    <span className="text-xs text-fg-tertiary">
                      #{event.sequence} ·{" "}
                      {new Date(event.occurredAt).toLocaleString()}
                    </span>
                  </div>
                  <Badge>
                    {quotaLabel(
                      event.deliveryState ?? event.notificationCode,
                      text,
                    )}
                  </Badge>
                </div>
                <p className="break-words text-sm text-fg-secondary">
                  {text("事件样本", "Event sample")}:{" "}
                  {sampleBytes(event.usedBytes, text)} /{" "}
                  {sampleBytes(event.quotaBytes, text)} ·{" "}
                  {text("采样", "Sampled")}{" "}
                  {new Date(event.sampleAt).toLocaleString()}
                </p>
                <p className="text-xs text-fg-tertiary">
                  {text("事件阈值", "Event thresholds")}:{" "}
                  {percent(event.policy.warningBasisPoints)} /{" "}
                  {percent(event.policy.criticalBasisPoints)} /{" "}
                  {percent(event.policy.recoveryBelowBasisPoints)} ·{" "}
                  {text("证据起点", "Evidence since")}{" "}
                  {new Date(event.evidenceSince).toLocaleString()}
                </p>
                {event.deliveryErrorCode && (
                  <p className="text-xs text-fg-tertiary">
                    {quotaLabel(event.deliveryErrorCode, text)}
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
                        ? text("收起交付详情", "Hide delivery details")
                        : text("查看交付详情", "View delivery details")}
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
  const { text } = usePreferences();
  const delivery = useQuotaSnapshot(
    async (signal) =>
      quotaResult(await getEmailDelivery({ path: { deliveryId }, signal })),
    deliveryId,
    onAuthBlocked,
  );
  if (!delivery.data)
    return delivery.error ? (
      <ErrorBanner
        error={safeQuotaError(delivery.error, text)}
        onRetry={() => void delivery.refresh()}
      />
    ) : (
      <Loading />
    );
  const item = delivery.data;
  return (
    <section
      className="grid min-w-0 gap-3 border-t border-line pt-4"
      aria-label={text("邮件交付详情", "Email delivery details")}
    >
      {Boolean(delivery.error) && (
        <ErrorBanner
          tone="warning"
          title={text("交付详情已过期", "Delivery details are out of date")}
          error={safeQuotaError(delivery.error, text)}
          onRetry={() => void delivery.refresh()}
        />
      )}
      <p className="text-sm text-fg-secondary">
        {quotaLabel(item.state, text)} · {text("尝试次数", "Attempts")}:{" "}
        {item.attempts}
      </p>
      {item.state === "accepted" && (
        <p className="text-sm text-fg-tertiary">
          {text(
            "SMTP accepted 仅表示服务器接受，不表示收件箱已送达。",
            "SMTP accepted only means the server accepted the message; it does not confirm inbox delivery.",
          )}
        </p>
      )}
      {item.possibleDuplicate && (
        <Notice
          tone="warning"
          title={text("可能重复", "Possible duplicate")}
          description={text(
            "此前交付结果不确定，重试可能产生重复邮件。",
            "An earlier delivery outcome was uncertain; a retry may duplicate the email.",
          )}
        />
      )}
      {item.errorCode && (
        <p className="text-sm text-fg-secondary">
          {quotaLabel(item.errorCode, text)}
        </p>
      )}
      {item.automaticCancellationCode && (
        <p className="text-sm text-fg-secondary">
          {quotaLabel(item.automaticCancellationCode, text)}
        </p>
      )}
      {item.nextAttemptAt && (
        <p className="text-xs text-fg-tertiary">
          {text("下一次尝试", "Next attempt")}:{" "}
          {new Date(item.nextAttemptAt).toLocaleString()}
        </p>
      )}
      {item.acceptedAt && (
        <p className="text-xs text-fg-tertiary">
          {text("接受时间", "Accepted at")}:{" "}
          {new Date(item.acceptedAt).toLocaleString()}
        </p>
      )}
      <p className="text-xs text-fg-tertiary">
        {text("模板", "Template")}: {item.templateVersion} ·{" "}
        {item.locale === "en" ? "English" : "中文"}
      </p>
    </section>
  );
}
