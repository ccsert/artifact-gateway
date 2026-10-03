import type {
  DiagnosticLocalCapacity,
  LocalCapacityAlias,
  LocalCapacityReason,
} from "../../client";
import { Badge } from "../../components/ui/Badge";
import { Card, CardHeader } from "../../components/ui/Layout";
import { formatBytes, formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

const aliases: LocalCapacityAlias[] = ["temporary", "logs", "backups"];
const aliasLabels: Record<LocalCapacityAlias, [string, string]> = {
  temporary: ["临时", "Temporary"],
  logs: ["日志", "Logs"],
  backups: ["备份", "Backups"],
};
const reasons: Record<LocalCapacityReason, [string, string]> = {
  not_configured: [
    "未配置观察目录，容量未知",
    "No observation directory configured; capacity unknown",
  ],
  remote_filesystem: [
    "远端文件系统，容量未知",
    "Remote filesystem; capacity unknown",
  ],
  unsupported_filesystem: [
    "不支持此文件系统，容量未知",
    "Unsupported filesystem; capacity unknown",
  ],
  unsupported_platform: [
    "不支持此平台，容量未知",
    "Unsupported platform; capacity unknown",
  ],
  read_failed: [
    "无法读取目录元数据，容量未知",
    "Directory metadata unavailable; capacity unknown",
  ],
  filesystem_identity_unknown: [
    "文件系统身份未知，容量未知",
    "Filesystem identity unavailable; capacity unknown",
  ],
  invalid_measurement: [
    "样本无效，容量未知",
    "Invalid sample; capacity unknown",
  ],
  unit_overflow: [
    "字节数超出支持范围，容量未知",
    "Byte count exceeds supported range; capacity unknown",
  ],
  timeout: ["刷新超时，容量未知", "Refresh timed out; capacity unknown"],
  cancelled: ["查询已取消，容量未知", "Query cancelled; capacity unknown"],
};

export function LocalCapacityPanel({
  capacity,
}: {
  capacity?: DiagnosticLocalCapacity;
}) {
  const { text, locale } = usePreferences();
  return (
    <Card className="ag-local-capacity-card">
      <CardHeader title={text("本地挂载容量", "Local mount capacity")} />
      <div className="ag-local-capacity-content">
        <p className="text-xs leading-5 text-zinc-500">
          {text(
            "当前进程的挂载视角，不代表 S3 或 NAS 后端物理池，也不是仓库逻辑配额。共享别名的容量不能相加。",
            "This process's mount view is not the S3 or NAS backend physical pool or a Repository logical quota. Do not add shared aliases' capacity.",
          )}
        </p>
        {!capacity ? (
          <p className="text-sm text-zinc-400">
            {text(
              "此节点未提供本地容量观察，容量未知",
              "This node does not report local capacity; capacity unknown",
            )}
          </p>
        ) : (
          <>
            <p className="text-xs leading-5 text-zinc-500">
              {text("快照检查", "Snapshot checked")}:{" "}
              {formatDate(capacity.checkedAt, locale)} · statfs · bytes ·{" "}
              {text("刷新间隔", "Refresh interval")}{" "}
              {capacity.refreshIntervalSeconds}s ·{" "}
              {text("最大样本年龄", "Maximum sample age")}{" "}
              {capacity.maxSampleAgeSeconds}s
            </p>
            <div className="ag-local-capacity-grid">
              {aliases.map((alias) => {
                const mount = capacity.mounts.find((m) => m.alias === alias);
                const valid =
                  mount?.status === "available" &&
                  typeof mount.totalBytes === "number" &&
                  Number.isFinite(mount.totalBytes) &&
                  mount.totalBytes > 0 &&
                  typeof mount.availableBytes === "number" &&
                  Number.isFinite(mount.availableBytes) &&
                  mount.availableBytes >= 0 &&
                  mount.availableBytes <= mount.totalBytes;
                const stale = mount?.status === "stale";
                const reason = mount?.reason
                  ? reasons[mount.reason]
                  : undefined;
                const shared = aliases.filter(
                  (other) =>
                    other !== alias && mount?.sharedWith.includes(other),
                );
                return (
                  <section
                    key={alias}
                    className="ag-local-capacity-mount"
                    aria-label={text(...aliasLabels[alias])}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <h3 className="text-sm font-medium text-zinc-200">
                        {text(...aliasLabels[alias])}
                      </h3>
                      <Badge
                        tone={valid ? "success" : stale ? "warning" : "neutral"}
                      >
                        {valid
                          ? text("有效样本", "Valid sample")
                          : stale
                            ? text("已过期", "Stale")
                            : text("未知", "Unknown")}
                      </Badge>
                    </div>
                    {!valid && (
                      <p className="text-xs leading-5 text-zinc-500">
                        {reason
                          ? text(...reason)
                          : text(
                              "样本未报告或无效，容量未知",
                              "Sample unavailable or invalid; capacity unknown",
                            )}
                      </p>
                    )}
                    <dl className="ag-local-capacity-values">
                      <div>
                        <dt>{text("总容量", "Total")}</dt>
                        <dd>{valid ? formatBytes(mount?.totalBytes) : "—"}</dd>
                      </div>
                      <div>
                        <dt>{text("可用容量", "Available")}</dt>
                        <dd>
                          {valid ? formatBytes(mount?.availableBytes) : "—"}
                        </dd>
                      </div>
                      <div>
                        <dt>
                          {stale
                            ? text("旧采样时间", "Previous sample")
                            : text("采样时间", "Sample time")}
                        </dt>
                        <dd>{formatDate(mount?.sampleAt, locale)}</dd>
                      </div>
                    </dl>
                    {valid && shared.length > 0 && (
                      <p className="text-xs leading-5 text-zinc-500">
                        {text(
                          `与${shared.map((other) => text(...aliasLabels[other])).join("、")}共享可见文件系统`,
                          `Shares the visible filesystem with ${shared.map((other) => text(...aliasLabels[other])).join(", ")}`,
                        )}
                      </p>
                    )}
                  </section>
                );
              })}
            </div>
          </>
        )}
      </div>
    </Card>
  );
}
