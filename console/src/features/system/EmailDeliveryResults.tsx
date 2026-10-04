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
  const { text } = usePreferences();
  const time = (value?: string) =>
    value
      ? new Date(value).toLocaleString(undefined, { timeZone: "UTC" }) + " UTC"
      : text("未知", "Unknown");
  return (
    <Card>
      <CardHeader
        title={text(
          "最近交付结果（最多 50 条）",
          "Recent delivery results (up to 50)",
        )}
      />
      <p className="px-5 pt-4 text-sm text-zinc-500">
        {text(
          "SMTP 已接受不代表最终送达或已读。停用目标不能撤回在途邮件；结果不确定的重试可能重复。",
          "SMTP acceptance does not mean inbox delivery or reading. Disabling cannot recall in-flight mail; uncertain retries may duplicate mail.",
        )}
      </p>
      {deliveries.length === 0 ? (
        <EmptyState
          title={text("暂无邮件交付记录", "No email deliveries yet")}
        />
      ) : (
        <ul className="divide-y divide-zinc-800/60">
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
                  {quotaLabel(delivery.state, text)}
                </QuotaBadge>
              </div>
              <p className="text-sm text-zinc-500">
                {delivery.kind === "repository_quota"
                  ? text("仓库配额告警", "Repository quota alert")
                  : text("管理员合成测试", "Administrator synthetic test")}{" "}
                · {quotaLabel(delivery.scenario, text)} · {delivery.locale} ·{" "}
                {text("尝试次数", "Attempts")}: {delivery.attempts}
              </p>
              <p className="break-all font-mono text-xs text-zinc-500">
                {text("交付 ID", "Delivery ID")}: {delivery.id}
              </p>
              <p className="text-xs text-zinc-500">
                {text("更新时间", "Updated")}: {time(delivery.updatedAt)}
                {delivery.nextAttemptAt && (
                  <>
                    {" "}
                    · {text("下次尝试", "Next attempt")}:{" "}
                    {time(delivery.nextAttemptAt)}
                  </>
                )}
                {delivery.acceptedAt && (
                  <>
                    {" "}
                    · {text("接受时间", "Accepted")}:{" "}
                    {time(delivery.acceptedAt)}
                  </>
                )}
              </p>
              {delivery.errorCode && (
                <p className="text-sm text-zinc-400">
                  {quotaLabel(delivery.errorCode, text)}
                </p>
              )}
              {delivery.automaticCancellationCode && (
                <p className="text-sm text-zinc-400">
                  {quotaLabel(delivery.automaticCancellationCode, text)}
                </p>
              )}
              {delivery.possibleDuplicate && (
                <p className="text-sm text-[var(--ag-status-warning)]">
                  {text(
                    "可能重复：先前尝试的 SMTP 结果不确定。",
                    "Possible duplicate: an earlier SMTP attempt had an uncertain outcome.",
                  )}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
