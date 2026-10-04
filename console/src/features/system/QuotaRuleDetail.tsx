import { useState } from "react";
import { Button } from "antd";
import {
  deleteRepositoryQuotaAlertRule,
  getEmailDelivery,
  listRepositoryQuotaAlertEvents,
  updateRepositoryQuotaAlertRule,
  type EmailCapability,
  type EmailTarget,
  type RepositoryQuotaAlertRule,
} from "../../client";
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
  isQuotaAuthError,
} from "./quotaAlertPresentation";
import { quotaResult, useQuotaSnapshot } from "./useQuotaSnapshot";
import { useQuotaAction } from "./useQuotaAction";

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
  const { text } = usePreferences();
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
        <ErrorBanner error={safeQuotaError(action.error, text)} />
      )}
      {confirmation && (
        <Card bodyClassName="grid gap-4 p-5">
          <h3 className="text-sm font-semibold text-zinc-100">
            {confirmation.kind === "enable"
              ? text("确认启用规则", "Confirm enabling this rule")
              : confirmation.kind === "disable"
                ? text("确认停用规则", "Confirm disabling this rule")
                : text("确认删除规则", "Confirm deleting this rule")}
          </h3>
          <p className="break-words text-sm text-zinc-400">
            {repositoryName} · {target?.name ?? rule.targetId} ·{" "}
            {target?.locale === "en"
              ? "English"
              : target?.locale === "zh-CN"
                ? "中文"
                : text("语言未知", "Locale unknown")}
          </p>
          <p className="text-sm text-zinc-400">
            {confirmation.kind === "enable"
              ? text(
                  "启用后，后续真实超限事件可能产生发往该目标的邮件。不会补发先前未入队的事件，也不会发送测试邮件。",
                  "Enabling may send email to this target for future real quota events. Previously unqueued events are not caught up, and no test email is sent.",
                )
              : confirmation.kind === "disable"
                ? text(
                    "停用会停止新评估并保留严重度和历史。未领取的自动交付会取消；已领取的交付可能完成一次。",
                    "Disabling stops new evaluations and retains severity and history. Unclaimed automatic deliveries are cancelled; a claimed delivery may finish once.",
                  )
                : text(
                    "删除会停止新评估并保留历史，不会生成恢复事件。未领取的自动交付会取消；已领取的交付可能完成一次。",
                    "Deletion stops new evaluations and retains history without generating a recovery event. Unclaimed automatic deliveries are cancelled; a claimed delivery may finish once.",
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
                ? text("确认启用", "Confirm enable")
                : confirmation.kind === "disable"
                  ? text("确认停用", "Confirm disable")
                  : text("确认删除", "Confirm delete")}
            </Button>
            <Button
              aria-label={text("取消", "Cancel")}
              disabled={action.busy}
              onClick={() => setConfirmation(undefined)}
            >
              {text("取消", "Cancel")}
            </Button>
          </div>
        </Card>
      )}
      <Card>
        <CardHeader title={text("规则状态", "Rule state")} />
        <div className="grid min-w-0 gap-4 p-5">
          <h3 className="break-words text-base font-semibold text-zinc-100">
            {repositoryName}
          </h3>
          <div className="flex flex-wrap gap-2">
            <Badge>
              {rule.deleted
                ? quotaLabel("deleted", text)
                : rule.enabled
                  ? text("已启用", "Enabled")
                  : quotaLabel("disabled", text)}
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
              {quotaLabel(rule.state.severity, text)}
            </Badge>
            <Badge>{quotaLabel(rule.state.dataState, text)}</Badge>
          </div>
          {rule.state.dataState !== "available" && (
            <p className="text-sm text-zinc-500">
              {text(
                "严重度为保留状态；当前证据不可用于判断是否恢复。",
                "Severity is retained; current evidence cannot determine whether the repository has recovered.",
              )}
            </p>
          )}
          {rule.state.lastSampleAt ? (
            <p className="break-words text-sm text-zinc-400">
              {rule.state.dataState === "available"
                ? text("最近样本", "Latest sample")
                : text("历史样本", "Historical sample")}{" "}
              ({new Date(rule.state.lastSampleAt).toLocaleString()}):{" "}
              {sampleBytes(rule.state.usedBytes, text)} /{" "}
              {sampleBytes(rule.state.quotaBytes, text)}
              {((rule.state.usedBytes !== undefined &&
                !Number.isSafeInteger(rule.state.usedBytes)) ||
                (rule.state.quotaBytes !== undefined &&
                  !Number.isSafeInteger(rule.state.quotaBytes))) &&
                ` · ${text("字节值为近似显示，严重度以服务端为准。", "Byte values are approximate; severity comes from the server.")}`}
            </p>
          ) : (
            <p className="text-sm text-zinc-500">
              {text("尚无有效容量样本", "No valid capacity sample yet")}
            </p>
          )}
          <dl className="grid min-w-0 gap-3 text-sm sm:grid-cols-2">
            {[
              [
                text("警告条件", "Warning condition"),
                `${text("达到", "At or above")} ${percent(rule.policy.warningBasisPoints)} · ${rule.policy.warningForSeconds} ${text("秒", "seconds")}`,
              ],
              [
                text("严重条件", "Critical condition"),
                `${text("达到", "At or above")} ${percent(rule.policy.criticalBasisPoints)} · ${rule.policy.criticalForSeconds} ${text("秒", "seconds")}`,
              ],
              [
                text("恢复条件", "Recovery condition"),
                `${text("严格低于", "Strictly below")} ${percent(rule.policy.recoveryBelowBasisPoints)} · ${rule.policy.recoveryForSeconds} ${text("秒", "seconds")}`,
              ],
              [
                text("最大样本年龄", "Maximum sample age"),
                `${rule.policy.maxSampleAgeSeconds} ${text("秒", "seconds")}`,
              ],
              [
                text("最近评估", "Last evaluation"),
                rule.evaluatedAt
                  ? new Date(rule.evaluatedAt).toLocaleString()
                  : text("尚未评估", "Not evaluated yet"),
              ],
            ].map(([label, value]) => (
              <div key={label} className="min-w-0">
                <dt className="text-xs text-zinc-500">{label}</dt>
                <dd className="mt-1 break-words text-zinc-300">{value}</dd>
              </div>
            ))}
          </dl>
          {(
            [
              ["warningSince", text("警告计时起点", "Warning evidence since")],
              [
                "criticalSince",
                text("严重计时起点", "Critical evidence since"),
              ],
              [
                "recoverySince",
                text("恢复计时起点", "Recovery evidence since"),
              ],
            ] as const
          ).map(
            ([key, label]) =>
              rule.state[key] && (
                <p key={key} className="text-xs text-zinc-500">
                  {label}: {new Date(rule.state[key]).toLocaleString()}
                </p>
              ),
          )}
          <div className="grid gap-2 border-t border-zinc-800/60 pt-4">
            <p className="break-words text-sm text-zinc-400">
              {text("通知目标", "Notification target")}:{" "}
              {target?.name ?? rule.targetId} ·{" "}
              {target?.locale === "en"
                ? "English"
                : target?.locale === "zh-CN"
                  ? "中文"
                  : text("语言未知", "Locale unknown")}{" "}
              ·{" "}
              {target?.recipientConfigured
                ? text("收件人已配置", "Recipient configured")
                : text(
                    "收件人未配置或目标不可用",
                    "Recipient missing or target unavailable",
                  )}
            </p>
            <p className="text-sm text-zinc-400">
              {text("目标状态", "Target state")}:{" "}
              {target
                ? target.enabled
                  ? text("已启用", "Enabled")
                  : text("已停用", "Disabled")
                : text("不可用", "Unavailable")}
            </p>
            <p className="break-all text-xs text-zinc-500">
              {text("绑定目标版本", "Bound target version")}:{" "}
              {rule.targetVersion}
            </p>
            {target && target.version !== rule.targetVersion && (
              <div className="grid gap-2">
                <p className="break-all text-xs text-zinc-500">
                  {text("当前目标版本", "Current target version")}:{" "}
                  {target.version}
                </p>
                <p className="text-sm text-zinc-500">
                  {text(
                    "目标版本已变更。编辑并保存以重新绑定；此前未入队的事件不会补发。",
                    "The target version changed. Edit and save to bind it again; previously unqueued events are not caught up.",
                  )}
                </p>
              </div>
            )}
            {target && !target.enabled && (
              <p className="text-sm text-zinc-500">
                {quotaLabel("target_disabled", text)}
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
                  {text("编辑规则", "Edit rule")}
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
                    ? text("停用规则", "Disable rule")
                    : text("启用规则", "Enable rule")}
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
                  {text("删除规则", "Delete rule")}
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

function QuotaEvents({
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
          <ol className="divide-y divide-zinc-800/60">
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
                    <span className="text-xs text-zinc-500">
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
                <p className="break-words text-sm text-zinc-400">
                  {text("事件样本", "Event sample")}:{" "}
                  {sampleBytes(event.usedBytes, text)} /{" "}
                  {sampleBytes(event.quotaBytes, text)} ·{" "}
                  {text("采样", "Sampled")}{" "}
                  {new Date(event.sampleAt).toLocaleString()}
                </p>
                <p className="text-xs text-zinc-500">
                  {text("事件阈值", "Event thresholds")}:{" "}
                  {percent(event.policy.warningBasisPoints)} /{" "}
                  {percent(event.policy.criticalBasisPoints)} /{" "}
                  {percent(event.policy.recoveryBelowBasisPoints)} ·{" "}
                  {text("证据起点", "Evidence since")}{" "}
                  {new Date(event.evidenceSince).toLocaleString()}
                </p>
                {event.deliveryErrorCode && (
                  <p className="text-xs text-zinc-500">
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
      className="grid min-w-0 gap-3 border-t border-zinc-800/60 pt-4"
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
      <p className="text-sm text-zinc-400">
        {quotaLabel(item.state, text)} · {text("尝试次数", "Attempts")}:{" "}
        {item.attempts}
      </p>
      {item.state === "accepted" && (
        <p className="text-sm text-zinc-500">
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
        <p className="text-sm text-zinc-400">
          {quotaLabel(item.errorCode, text)}
        </p>
      )}
      {item.automaticCancellationCode && (
        <p className="text-sm text-zinc-400">
          {quotaLabel(item.automaticCancellationCode, text)}
        </p>
      )}
      {item.nextAttemptAt && (
        <p className="text-xs text-zinc-500">
          {text("下一次尝试", "Next attempt")}:{" "}
          {new Date(item.nextAttemptAt).toLocaleString()}
        </p>
      )}
      {item.acceptedAt && (
        <p className="text-xs text-zinc-500">
          {text("接受时间", "Accepted at")}:{" "}
          {new Date(item.acceptedAt).toLocaleString()}
        </p>
      )}
      <p className="text-xs text-zinc-500">
        {text("模板", "Template")}: {item.templateVersion} ·{" "}
        {item.locale === "en" ? "English" : "中文"}
      </p>
    </section>
  );
}
