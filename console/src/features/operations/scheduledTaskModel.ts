import type { Translate } from "../../lib/preferences";
import type { CreateScheduledTask } from "../../client";

export type TaskForm = CreateScheduledTask;

export const DEFAULT_FORM: TaskForm = {
  name: "",
  description: "",
  kind: "repository-retention",
  intervalMinutes: 1440,
  enabled: true,
};

export const RETENTION_FORMATS = new Set([
  "maven",
  "oci",
  "conan",
  "raw",
  "npm",
  "pypi",
  "go",
]);

export function formatTaskInterval(minutes: number, t: Translate) {
  if (minutes % (24 * 60) === 0) {
    const days = minutes / (24 * 60);
    return t(days === 1 ? "operations.everyDay" : "operations.everyDays", {
      days,
    });
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60;
    return t(hours === 1 ? "operations.everyHour" : "operations.everyHours", {
      hours,
    });
  }
  return t("operations.everyMinutes", { minutes });
}
