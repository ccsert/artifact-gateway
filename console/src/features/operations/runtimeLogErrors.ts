import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
/** A runtime log failure in the reader's language, keyed by its problem code. */
export function runtimeLogError(error: unknown, t: Translate): unknown {
  // Let the shared banner retain its endpoint/version mismatch guidance.
  if (
    typeof error === "string" &&
    /(?:^|\b)404(?:\b|$).*not found/i.test(error.trim())
  )
    return error;
  const problem =
    error && typeof error === "object"
      ? (error as { code?: string; message?: string })
      : {};
  const messages: Record<string, MessageKey> = {
    invalid_time_window: "operations.invalidTimeRangeAtMost24HoursEnding",
    log_buffer_unavailable: "operations.memoryLogQueriesAreDisabledOnThisNode",
    log_source_identity_unavailable:
      "operations.logSourceIdentityIsUnavailableCheckTheNode",
    remote_log_query_unavailable:
      "operations.thisEndpointQueriesTheConnectedProcessOnlyClear",
    log_cursor_scope_changed:
      "operations.processOrSessionChangedRefreshTheSnapshot",
    invalid_cursor: "operations.theCursorIsInvalidOrFiltersChangedRefresh",
    log_query_timeout: "operations.logQueryTimedOutRetryShortly",
    access_denied: "operations.thisIdentityCannotQueryRuntimeLogs",
    password_change_required:
      "operations.updateYourPasswordBeforeQueryingRuntimeLogs",
    invalid_filter: "operations.aFilterIsTooLongOrContainsControl",
  };
  const translation = problem.code ? messages[problem.code] : undefined;
  if (translation) return { ...problem, message: t(translation) };
  if (problem.code)
    return {
      ...problem,
      message: t("operations.logQueryFailedCheckTheConnectionOrPermissions"),
    };
  return {
    ...problem,
    message: t("operations.logQueryFailedCheckTheConnectionAndRetry"),
  };
}
