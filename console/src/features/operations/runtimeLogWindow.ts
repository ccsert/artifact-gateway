import type { Dayjs } from "dayjs";
import type { LogQuery } from "./useRuntimeLogStream";

export type RuntimeLogRangeError = "required" | "order" | "duration" | "future";
export type RuntimeLogTimeRange = 900 | 3600 | 86400 | "custom";

// The picker displays seconds. Normalize that same precision before validating
// and serializing, so invisible milliseconds cannot turn 24h into 24h + 1ms.
export function customRuntimeLogWindow(
  range: [Dayjs, Dayjs] | null,
  now = Date.now(),
): { bounds?: Pick<LogQuery, "from" | "to">; error?: RuntimeLogRangeError } {
  if (!range || range.some((value) => !value.isValid()))
    return { error: "required" };
  const [from, to] = range.map((value) => value.startOf("second"));
  if (from.valueOf() > to.valueOf()) return { error: "order" };
  if (to.valueOf() - from.valueOf() > 86_400_000) return { error: "duration" };
  if (to.valueOf() > now + 300_000) return { error: "future" };
  return { bounds: { from: from.toISOString(), to: to.toISOString() } };
}
