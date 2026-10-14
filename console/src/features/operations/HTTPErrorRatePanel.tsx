import type { MessageKey } from "../../messages";
import { useEffect, useState } from "react";
import type {
  DiagnosticHttpErrorRate,
  HttpErrorRateReason,
} from "../../client";
import { Badge } from "../../components/ui/Badge";
import { Card, CardHeader } from "../../components/ui/Layout";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import "./operations.css";

const reasons: Record<HttpErrorRateReason, MessageKey> = {
  source_unavailable: "operations.trustedObservationUnavailable",
  warming_up: "operations.windowWarmingUpInsufficientData",
  session_changed: "operations.processSessionChangedWindowWarmingUpAgain",
  counter_reset: "operations.counterResetWindowWarmingUpAgain",
  sampling_gap: "operations.samplingGapWindowWarmingUpAgain",
  clock_invalid: "operations.invalidSampleClockRatioUnknown",
  count_overflow: "operations.countsExceedTheSafeRangeRatioUnknown",
  no_traffic: "operations.noRequestsInTheWindowRatioUnknown",
  low_sample: "operations.fewerThan20RequestsInsufficientData",
  sample_stale: "operations.sampleIsStaleRatioUnknown",
};

function safeCount(value?: number): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

export function HTTPErrorRatePanel({
  rate,
}: {
  rate?: DiagnosticHttpErrorRate;
}) {
  const { t, locale } = usePreferences();
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
      <CardHeader title={t("operations.businessHTTP5xxObservation")} />
      <div className="ag-http-error-rate-content">
        <p className="text-xs leading-5 text-fg-tertiary">
          {t("operations.thisProcessSBusinessHTTP1xx5xxRequests")}
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Badge tone="neutral">
            {valid
              ? t("operations.sampleAvailable")
              : stale
                ? t("operations.stale")
                : t("operations.insufficientDataOrUnknown")}
          </Badge>
          {!valid && (
            <p className="text-sm text-fg-secondary">
              {reason && reasons[reason]
                ? t(reasons[reason])
                : t("operations.sampleUnavailableOrInvalidRatioUnknown")}
            </p>
          )}
        </div>
        <dl className="ag-http-error-rate-values">
          <div>
            <dt>{t("operations.5xxRatio")}</dt>
            <dd data-testid="http-error-rate-ratio">{percent}</dd>
          </div>
          <div>
            <dt>
              {stale
                ? t("operations.previousSnapshotRequests")
                : t("operations.requests")}
            </dt>
            <dd>{countsValid ? rate.requests!.toLocaleString(locale) : "—"}</dd>
          </div>
          <div>
            <dt>
              {stale
                ? t("operations.previousSnapshot5xx")
                : t("operations.5xxErrors")}
            </dt>
            <dd>{countsValid ? rate.errors!.toLocaleString(locale) : "—"}</dd>
          </div>
        </dl>
        {rate && (
          <dl className="ag-http-error-rate-metadata">
            <div>
              <dt>{t("operations.observedNodeSession")}</dt>
              <dd>
                {rate.instanceId || "—"} / {rate.sessionId || "—"}
              </dd>
            </div>
            <div>
              <dt>{t("operations.actualWindow")}</dt>
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
              <dt>{t("operations.sampleSnapshotChecked")}</dt>
              <dd>
                {formatDate(rate.sampleAt, locale)} /{" "}
                {formatDate(rate.checkedAt, locale)}
              </dd>
            </div>
          </dl>
        )}
        <p className="text-xs leading-5 text-fg-tertiary">
          {t("operations.targetWindow300sMinimum20RequestsSampleEvery")}
        </p>
      </div>
    </Card>
  );
}
