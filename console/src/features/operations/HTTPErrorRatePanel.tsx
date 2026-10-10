import { useEffect, useState } from "react";
import type {
  DiagnosticHttpErrorRate,
  HttpErrorRateReason,
} from "../../client";
import { Badge } from "../../components/ui/Badge";
import { Card, CardHeader } from "../../components/ui/Layout";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

const reasons: Record<HttpErrorRateReason, [string, string]> = {
  source_unavailable: [
    "此节点未提供可信观察",
    "Trusted observation unavailable",
  ],
  warming_up: ["窗口预热，数据不足", "Window warming up; insufficient data"],
  session_changed: [
    "进程会话已切换，窗口重新预热",
    "Process session changed; window warming up again",
  ],
  counter_reset: [
    "计数器已重置，窗口重新预热",
    "Counter reset; window warming up again",
  ],
  sampling_gap: [
    "采样中断，窗口重新预热",
    "Sampling gap; window warming up again",
  ],
  clock_invalid: [
    "采样时钟异常，比例未知",
    "Invalid sample clock; ratio unknown",
  ],
  count_overflow: [
    "计数超出安全范围，比例未知",
    "Counts exceed the safe range; ratio unknown",
  ],
  no_traffic: [
    "窗口内无请求，比例未知",
    "No requests in the window; ratio unknown",
  ],
  low_sample: [
    "少于 20 个请求，数据不足",
    "Fewer than 20 requests; insufficient data",
  ],
  sample_stale: ["样本已过期，比例未知", "Sample is stale; ratio unknown"],
};

function safeCount(value?: number): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

export function HTTPErrorRatePanel({
  rate,
}: {
  rate?: DiagnosticHttpErrorRate;
}) {
  const { text, locale } = usePreferences();
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    const receivedAt = Date.now();
    setElapsed(0);
    const timer = setInterval(() => setElapsed(Date.now() - receivedAt), 1000);
    return () => clearInterval(timer);
  }, [rate]);
  // Use the greater of server-reported age plus display time and browser UTC
  // age, so a delayed response cannot make an expired sample look fresh.
  const browserAge = rate?.sampleAt
    ? Date.now() - Date.parse(rate.sampleAt)
    : NaN;
  const age = rate?.sampleAt
    ? Math.max(
        Date.parse(rate.checkedAt) - Date.parse(rate.sampleAt) + elapsed,
        browserAge,
      )
    : NaN;
  const clockInvalid =
    elapsed < 0 ||
    (rate?.sampleAt && (!Number.isFinite(age) || age < 0 || browserAge < 0));
  const stale =
    rate?.state === "stale" || (Number.isFinite(age) && age > 30_000);
  const countsValid =
    safeCount(rate?.requests) &&
    safeCount(rate?.errors) &&
    rate.errors <= rate.requests;
  const valid =
    rate?.state === "available" &&
    !stale &&
    !clockInvalid &&
    countsValid &&
    rate.requests! >= 20 &&
    typeof rate.coverageSeconds === "number" &&
    rate.coverageSeconds >= 300 &&
    rate.coverageSeconds <= 330 &&
    Number.isFinite(age) &&
    typeof rate.ratio === "number" &&
    Number.isFinite(rate.ratio) &&
    rate.ratio >= 0 &&
    rate.ratio <= 1 &&
    Math.abs(rate.ratio - rate.errors! / rate.requests!) < 1e-12;
  const reason = clockInvalid
    ? "clock_invalid"
    : stale
      ? "sample_stale"
      : rate?.reason;
  const percent = valid
    ? rate.ratio! > 0 && rate.ratio! * 100 < 0.01
      ? "<0.01%"
      : `${(rate.ratio! * 100).toLocaleString(locale, { maximumFractionDigits: 2 })}%`
    : "—";
  return (
    <Card className="ag-http-error-rate-card">
      <CardHeader
        title={text("业务 HTTP 5xx 观察", "Business HTTP 5xx observation")}
      />
      <div className="ag-http-error-rate-content">
        <p className="text-xs leading-5 text-fg-tertiary">
          {text(
            "当前进程业务 HTTP：1xx–5xx 为总请求，5xx 为错误；包括管理 API，排除健康检查与 metrics 探针。此比例不表示服务健康，不触发告警。",
            "This process's business HTTP: 1xx–5xx requests, with 5xx counted as errors. Includes management API; excludes health and metrics probes. The ratio is not a health judgment or an alert.",
          )}
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Badge tone="neutral">
            {valid
              ? text("样本可用", "Sample available")
              : stale
                ? text("已过期", "Stale")
                : text("数据不足或未知", "Insufficient data or unknown")}
          </Badge>
          {!valid && (
            <p className="text-sm text-fg-secondary">
              {reason && reasons[reason]
                ? text(...reasons[reason])
                : text(
                    "样本未报告或无效，比例未知",
                    "Sample unavailable or invalid; ratio unknown",
                  )}
            </p>
          )}
        </div>
        <dl className="ag-http-error-rate-values">
          <div>
            <dt>{text("5xx 占比", "5xx ratio")}</dt>
            <dd data-testid="http-error-rate-ratio">{percent}</dd>
          </div>
          <div>
            <dt>
              {stale
                ? text("旧快照总请求", "Previous snapshot requests")
                : text("总请求数", "Requests")}
            </dt>
            <dd>{countsValid ? rate.requests!.toLocaleString(locale) : "—"}</dd>
          </div>
          <div>
            <dt>
              {stale
                ? text("旧快照 5xx", "Previous snapshot 5xx")
                : text("5xx 错误数", "5xx errors")}
            </dt>
            <dd>{countsValid ? rate.errors!.toLocaleString(locale) : "—"}</dd>
          </div>
        </dl>
        {rate && (
          <dl className="ag-http-error-rate-metadata">
            <div>
              <dt>{text("观察节点 / 会话", "Observed node / session")}</dt>
              <dd>
                {rate.instanceId || "—"} / {rate.sessionId || "—"}
              </dd>
            </div>
            <div>
              <dt>{text("实际窗口", "Actual window")}</dt>
              <dd>
                {formatDate(rate.windowStart, locale)} →{" "}
                {formatDate(rate.windowEnd, locale)}
                {typeof rate.coverageSeconds === "number" && (
                  <>
                    {" "}
                    ·{" "}
                    {rate.coverageSeconds.toLocaleString(locale, {
                      maximumFractionDigits: 1,
                    })}
                    s
                  </>
                )}
              </dd>
            </div>
            <div>
              <dt>
                {text("采样时间 / 快照检查", "Sample / snapshot checked")}
              </dt>
              <dd>
                {formatDate(rate.sampleAt, locale)} /{" "}
                {formatDate(rate.checkedAt, locale)}
              </dd>
            </div>
          </dl>
        )}
        <p className="text-xs leading-5 text-fg-tertiary">
          {text(
            "目标窗口 300 秒 · 最少 20 个请求 · 每 15 秒采样 · 最大样本年龄 30 秒。预热计数只覆盖实际窗口；20 个请求不保证统计置信度。",
            "Target window 300s · minimum 20 requests · sample every 15s · maximum sample age 30s. Warmup counts cover only the actual window; 20 requests do not guarantee statistical confidence.",
          )}
        </p>
      </div>
    </Card>
  );
}
