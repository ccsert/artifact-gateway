import { useCallback, useEffect, useState } from "react";
import {
  CopyOutlined,
  InfoCircleOutlined,
  PauseOutlined,
  CaretRightOutlined,
  DownloadOutlined,
} from "@ant-design/icons";
import { Alert, Button, Space, Switch } from "antd";
import { useSearchParams } from "react-router-dom";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useRuntimeLogStream } from "./useRuntimeLogStream";
import type { LogQuery } from "./useRuntimeLogStream";
import {
  logNDJSON,
  logText,
  MAX_DISPLAY_CHARS,
  MAX_LOG_BYTES,
  MAX_LOG_RECORDS,
} from "./runtimeLogView";
import { RuntimeLogFilters } from "./RuntimeLogFilters";
import { logCopy, RuntimeLogRow } from "./RuntimeLogRow";
import { runtimeLogError } from "./runtimeLogErrors";
import "./RuntimeLogsPanel.css";

export function RuntimeLogsPanel() {
  const { locale, t } = usePreferences();
  const [searchParams] = useSearchParams();
  const auditRequestId = searchParams.get("requestId") ?? "";
  const auditTraceId = searchParams.get("traceId") ?? "";
  const [query, setQuery] = useState<LogQuery>(() => ({
    requestId: auditRequestId || undefined,
    traceId: auditTraceId || undefined,
    limit: 50,
  }));
  const {
    page,
    entries,
    error,
    loading,
    following,
    visible,
    gap,
    scopeChanged,
    trimmed,
    unread,
    bytes,
    viewport: viewportRef,
    follow,
    pause,
    resumeBottom,
    scrolled,
    refresh,
    loadOlder,
    hasOlder,
    resetVersion,
  } = useRuntimeLogStream(query);
  const [wrap, setWrap] = useState(true);
  const [selected, setSelected] = useState("");
  const [selectionTooLarge, setSelectionTooLarge] = useState(false);
  const [selectionVersion, setSelectionVersion] = useState(-1);
  const { copiedValue, copy } = useClipboardAction();

  const changedSelection = useCallback(() => {
    const selection = window.getSelection();
    const node = viewportRef.current;
    const withinStream =
      selection &&
      node &&
      selection.anchorNode &&
      selection.focusNode &&
      node.contains(selection.anchorNode) &&
      node.contains(selection.focusNode) &&
      Array.from({ length: selection.rangeCount }, (_, index) =>
        selection.getRangeAt(index),
      ).every(
        (range) =>
          node.contains(range.startContainer) &&
          node.contains(range.endContainer),
      );
    const raw = withinStream ? selection.toString() : "";
    const safe = logText(raw, MAX_LOG_BYTES);
    const tooLarge =
      raw.length > MAX_LOG_BYTES ||
      safe.length > MAX_LOG_BYTES ||
      new TextEncoder().encode(safe).byteLength > MAX_LOG_BYTES;
    setSelectionTooLarge(tooLarge);
    setSelected(tooLarge ? "" : safe);
    setSelectionVersion(resetVersion);
    return tooLarge ? "" : safe;
  }, [viewportRef, resetVersion]);

  useEffect(() => {
    const selection = window.getSelection();
    const node = viewportRef.current;
    if (selection?.anchorNode && node?.contains(selection.anchorNode))
      selection.removeAllRanges();
    changedSelection();
    document.addEventListener("selectionchange", changedSelection);
    return () =>
      document.removeEventListener("selectionchange", changedSelection);
  }, [changedSelection, viewportRef]);

  useEffect(() => {
    changedSelection();
  }, [entries, changedSelection]);

  const download = () => {
    const url = URL.createObjectURL(
      new Blob([logNDJSON(entries)], {
        type: "application/x-ndjson;charset=utf-8",
      }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "runtime-logs.ndjson";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <Card>
      <CardHeader title={t("operations.runtimeLogs")} />
      <div className="ag-runtime-log-intro">
        <InfoCircleOutlined aria-hidden="true" />
        <span>{t("operations.searchThisProcessSInMemoryLogsRestart")}</span>
      </div>
      <RuntimeLogFilters
        auditRequestId={auditRequestId}
        auditTraceId={auditTraceId}
        components={page?.source?.components ?? []}
        loading={loading}
        onRefresh={refresh}
        setQuery={setQuery}
      />
      {error ? (
        <div className="p-4">
          <ErrorBanner
            error={runtimeLogError(error, t)}
            tone={page ? "warning" : "error"}
            onRetry={refresh}
            title={
              scopeChanged
                ? t("operations.processOrSessionChangedRefreshTheSnapshot2")
                : undefined
            }
          />
        </div>
      ) : null}
      {loading && !page && !error ? (
        <div className="p-6">
          <Loading label={t("operations.queryingRuntimeLogs")} />
        </div>
      ) : null}
      {page ? (
        <div className="border-t border-[var(--ag-border-subtle)]">
          <div className="ag-runtime-log-source flex flex-wrap gap-x-3 gap-y-1 px-5 py-3 text-xs text-[var(--ag-content-tertiary)]">
            <span>
              {t("operations.currentProcess")}:{" "}
              {logText(page.instanceId, MAX_DISPLAY_CHARS)}
            </span>
            <span>
              {t("operations.session")}:{" "}
              {logText(page.sessionId, MAX_DISPLAY_CHARS)}
            </span>
            <span>
              {t("operations.loadedBytes", { length: entries.length, bytes })}
            </span>
            <span>
              {t("operations.retainedSequences")}:{" "}
              {page.retention
                ? `${page.retention.earliestSequence}–${page.retention.latestSequence}`
                : t("operations.notReported")}
            </span>
            <span>
              {page.source
                ? t("operations.minimumRequestsSlowMsBufferLines", {
                    minimumLevel: page.source.minimumLevel,
                    accessMode: page.source.accessMode,
                    slowThresholdMs: page.source.slowThresholdMs,
                    capacityLines: page.source.capacityLines,
                  })
                : t("operations.gatewayDidNotReportItsLoggingPolicy")}
            </span>
          </div>
          <div className="ag-runtime-log-toolbar">
            <Space wrap>
              <Button
                icon={following ? <PauseOutlined /> : <CaretRightOutlined />}
                disabled={!page.afterCursor || Boolean(query.from || query.to)}
                onClick={following ? pause : follow}
              >
                {following
                  ? t("operations.pauseFollow")
                  : t("operations.followNewLogs")}
              </Button>
              {following && !visible ? (
                <span>{t("operations.pollingPausedWhileHidden")}</span>
              ) : null}
              {unread > 0 ? (
                <Button onClick={resumeBottom}>
                  {t("operations.unreadResumeAtBottom", { unread })}
                </Button>
              ) : null}
              <label className="ag-runtime-log-wrap">
                <Switch size="small" checked={wrap} onChange={setWrap} />
                {t("operations.wrapLines")}
              </label>
              <Button
                icon={<CopyOutlined />}
                disabled={!selected || selectionVersion !== resetVersion}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  const current = changedSelection();
                  if (current) void copy(current, "selection");
                }}
              >
                {t("operations.copySelection")}
              </Button>
              <Button
                icon={<CopyOutlined />}
                disabled={!entries.length}
                onClick={() => void copy(logNDJSON(entries), "loaded")}
              >
                {t("operations.copyLoaded")}
              </Button>
              <Button
                icon={<DownloadOutlined />}
                disabled={!entries.length}
                onClick={download}
              >
                {t("operations.downloadNDJSON")}
              </Button>
            </Space>
            <details className="ag-runtime-log-help">
              <summary>{t("operations.queryAndCopyDetails")}</summary>
              <p>
                {t("operations.pausePreservesTheCursorForResumeReadingAbove")}
              </p>
              <p>
                {t("operations.copyAndExportLoadedResultsOnlyAtMost", {
                  MAX_LOG_RECORDS,
                  MAX_DISPLAY_CHARS,
                })}
              </p>
            </details>
            {selectionTooLarge && selectionVersion === resetVersion ? (
              <Alert
                type="warning"
                showIcon
                title={t("operations.selectionExceeds1MiBSelectLessTextTo")}
              />
            ) : null}
            {page.source?.componentsTruncated ? (
              <span>
                {t("operations.theComponentListIsBoundedEnterAnotherExact")}
              </span>
            ) : null}
            {query.requestId || query.traceId ? (
              <span>
                {t("operations.currentCorrelationFilters")}: Request{" "}
                {logText(query.requestId ?? "", MAX_DISPLAY_CHARS)} · Trace{" "}
                {logText(query.traceId ?? "", MAX_DISPLAY_CHARS)}
              </span>
            ) : null}
            {gap ? (
              <Alert
                type="warning"
                showIcon
                title={t("operations.serverRetentionOverwroteSomeUnreadLogs")}
              />
            ) : null}
            {scopeChanged ? (
              <Alert
                type="warning"
                showIcon
                title={t("operations.changedToANewProcessOrSessionPrevious")}
              />
            ) : null}
            {trimmed > 0 ? (
              <Alert
                type="info"
                showIcon
                title={t(
                  "operations.clientCapacityRemovedRowsThisDoesNotIndicate",
                  { trimmed },
                )}
              />
            ) : null}
          </div>
          {entries.length === 0 ? (
            <EmptyState
              compact
              title={t("operations.noLogsInThisRange")}
              hint={t("operations.tryTheLast24HoursOrDifferentFilters")}
            />
          ) : (
            <div
              ref={viewportRef}
              onScroll={scrolled}
              role="log"
              aria-live="off"
              aria-label={t("operations.runtimeLogStream")}
              className={`ag-runtime-log-stream${wrap ? "" : " ag-runtime-log-nowrap"}`}
            >
              {entries.map((entry) => (
                <RuntimeLogRow
                  key={`${entry.instanceId}-${entry.sessionId}-${entry.sequence}`}
                  entry={entry}
                  locale={locale}
                  copied={
                    copiedValue === `${entry.sessionId}-${entry.sequence}`
                  }
                  onCopy={() =>
                    void copy(
                      logCopy(entry),
                      `${entry.sessionId}-${entry.sequence}`,
                    )
                  }
                />
              ))}
            </div>
          )}
          <Pagination
            hasMore={
              hasOlder &&
              entries.length < MAX_LOG_RECORDS &&
              bytes < MAX_LOG_BYTES - 16_384
            }
            loading={loading}
            label={t("operations.loadOlderLogs")}
            onMore={loadOlder}
          />
        </div>
      ) : null}
    </Card>
  );
}
