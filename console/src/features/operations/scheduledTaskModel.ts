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

export function formatTaskInterval(
  minutes: number,
  text: (zh: string, en: string) => string,
) {
  if (minutes % (24 * 60) === 0) {
    const days = minutes / (24 * 60);
    return text(`每 ${days} 天`, `Every ${days} day${days === 1 ? "" : "s"}`);
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60;
    return text(
      `每 ${hours} 小时`,
      `Every ${hours} hour${hours === 1 ? "" : "s"}`,
    );
  }
  return text(`每 ${minutes} 分钟`, `Every ${minutes} minutes`);
}
