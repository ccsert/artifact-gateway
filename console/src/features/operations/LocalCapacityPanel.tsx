import type { MessageKey } from "../../messages";
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
const aliasLabels: Record<LocalCapacityAlias, MessageKey> = {
  temporary: "operations.temporary",
  logs: "operations.logs",
  backups: "operations.backups",
};
const reasons: Record<LocalCapacityReason, MessageKey> = {
  not_configured: "operations.noObservationDirectoryConfiguredCapacityUnknown",
  remote_filesystem: "operations.remoteFilesystemCapacityUnknown",
  unsupported_filesystem: "operations.unsupportedFilesystemCapacityUnknown",
  unsupported_platform: "operations.unsupportedPlatformCapacityUnknown",
  read_failed: "operations.directoryMetadataUnavailableCapacityUnknown",
  filesystem_identity_unknown:
    "operations.filesystemIdentityUnavailableCapacityUnknown",
  invalid_measurement: "operations.invalidSampleCapacityUnknown",
  unit_overflow: "operations.byteCountExceedsSupportedRangeCapacityUnknown",
  timeout: "operations.refreshTimedOutCapacityUnknown",
  cancelled: "operations.queryCancelledCapacityUnknown",
};

export function LocalCapacityPanel({
  capacity,
}: {
  capacity?: DiagnosticLocalCapacity;
}) {
  const { t, locale } = usePreferences();
  return (
    <Card className="ag-local-capacity-card">
      <CardHeader title={t("operations.localMountCapacity")} />
      <div className="ag-local-capacity-content">
        <p className="text-xs leading-5 text-fg-tertiary">
          {t("operations.thisProcessSMountViewIsNotThe")}
        </p>
        {!capacity ? (
          <p className="text-sm text-fg-secondary">
            {t("operations.thisNodeDoesNotReportLocalCapacityCapacity")}
          </p>
        ) : (
          <>
            <p className="text-xs leading-5 text-fg-tertiary">
              {t("operations.snapshotChecked")}:{" "}
              {formatDate(capacity.checkedAt, locale)} · statfs · bytes ·{" "}
              {t("operations.refreshInterval")}{" "}
              {capacity.refreshIntervalSeconds}s ·{" "}
              {t("operations.maximumSampleAge")} {capacity.maxSampleAgeSeconds}s
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
                    aria-label={t(aliasLabels[alias])}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <h3 className="text-sm font-medium text-fg">
                        {t(aliasLabels[alias])}
                      </h3>
                      <Badge
                        tone={valid ? "success" : stale ? "warning" : "neutral"}
                      >
                        {valid
                          ? t("operations.validSample")
                          : stale
                            ? t("operations.stale")
                            : t("operations.unknown")}
                      </Badge>
                    </div>
                    {!valid && (
                      <p className="text-xs leading-5 text-fg-tertiary">
                        {reason
                          ? t(reason)
                          : t(
                              "operations.sampleUnavailableOrInvalidCapacityUnknown",
                            )}
                      </p>
                    )}
                    <dl className="ag-local-capacity-values">
                      <div>
                        <dt>{t("operations.total")}</dt>
                        <dd>{valid ? formatBytes(mount?.totalBytes) : "—"}</dd>
                      </div>
                      <div>
                        <dt>{t("operations.available")}</dt>
                        <dd>
                          {valid ? formatBytes(mount?.availableBytes) : "—"}
                        </dd>
                      </div>
                      <div>
                        <dt>
                          {stale
                            ? t("operations.previousSample")
                            : t("operations.sampleTime")}
                        </dt>
                        <dd>{formatDate(mount?.sampleAt, locale)}</dd>
                      </div>
                    </dl>
                    {valid && shared.length > 0 && (
                      <p className="text-xs leading-5 text-fg-tertiary">
                        {t("operations.sharesTheVisibleFilesystemWith", {
                          aliases: shared
                            .map((other) => t(aliasLabels[other]))
                            .join(locale === "zh-CN" ? "、" : ", "),
                        })}
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
