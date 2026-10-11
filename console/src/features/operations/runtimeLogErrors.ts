/** A runtime log failure in the reader's language, keyed by its problem code. */
export function runtimeLogError(
  error: unknown,
  text: (zh: string, en: string) => string,
): unknown {
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
  const messages: Record<string, [string, string]> = {
    invalid_time_window: [
      "时间范围无效：最多 24 小时，结束时间不能超过当前时间 5 分钟。请调整范围后查询。",
      "Invalid time range: at most 24 hours, ending no later than 5 minutes from now. Adjust the range and search again.",
    ],
    log_buffer_unavailable: [
      "当前节点未启用内存日志查询，请检查日志缓冲配置。",
      "Memory log queries are disabled on this node. Check the buffer configuration.",
    ],
    log_source_identity_unavailable: [
      "当前日志来源信息不可用，请检查节点配置后重试。",
      "Log source identity is unavailable. Check the node configuration and retry.",
    ],
    remote_log_query_unavailable: [
      "当前入口只能查询所连接进程，请清除节点筛选后重试。",
      "This endpoint queries the connected process only. Clear the instance filter and retry.",
    ],
    log_cursor_scope_changed: [
      "进程或会话已变化，请刷新新快照。",
      "Process or session changed; refresh the snapshot.",
    ],
    invalid_cursor: [
      "查询游标已失效或筛选发生变化，请刷新快照。",
      "The cursor is invalid or filters changed. Refresh the snapshot.",
    ],
    log_query_timeout: [
      "日志查询超时，请稍后重试。",
      "Log query timed out. Retry shortly.",
    ],
    access_denied: [
      "当前身份没有查询运行日志的权限。",
      "This identity cannot query runtime logs.",
    ],
    password_change_required: [
      "需要先更新密码，之后再查询运行日志。",
      "Update your password before querying runtime logs.",
    ],
    invalid_filter: [
      "筛选内容过长或包含控制字符，请调整后查询。",
      "A filter is too long or contains control characters. Adjust it and search again.",
    ],
  };
  const translation = problem.code ? messages[problem.code] : undefined;
  if (translation) return { ...problem, message: text(...translation) };
  if (problem.code)
    return {
      ...problem,
      message: text(
        "日志查询失败，请检查连接或访问权限后重试。",
        "Log query failed. Check the connection or permissions and retry.",
      ),
    };
  return {
    ...problem,
    message: text(
      "日志查询失败，请检查连接后重试。",
      "Log query failed. Check the connection and retry.",
    ),
  };
}
