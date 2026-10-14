import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type {
  Group,
  AuditRecord,
  OverviewStatistics,
  OverviewRepositoryStatistics,
} from "../../client";
import { Card, CardHeader } from "../../components/ui/Layout";
import { Loading, ErrorBanner, EmptyState } from "../../components/ui/Feedback";
import { FormatBadge } from "../../components/ui/Badge";
import { formatBytes, formatNumber } from "../../lib/format";
import {
  ConsoleTable,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { StorageByFormatChart } from "../../components/ui/DashboardCharts";
import { usePreferences } from "../../lib/preferences";
import { useDashboardResource } from "./useDashboardResource";
import { RecentAuditList } from "./RecentAuditList";

import type { StatisticsWindow } from "./dashboardModel";
import { sortRepositoryStatistics } from "./dashboardModel";

type DashboardResource<T> = ReturnType<typeof useDashboardResource<T>>;

export function DashboardMetrics({
  statisticsResource,
  groupsResource,
  window,
}: {
  statisticsResource: DashboardResource<OverviewStatistics>;
  groupsResource: DashboardResource<Group[]>;
  window: StatisticsWindow;
}) {
  const { locale, t } = usePreferences();
  const statistics = statisticsResource.data;
  const groups = groupsResource.data;
  // Every figure from the statistics read says the same thing while it is
  // missing, so the cards keep one height.
  const statisticsHint = statisticsResource.loading
    ? t("dashboard.loadingRepositoryStatistics")
    : t("dashboard.repositoryStatisticsUnavailable");
  const largest = statistics?.repositories.reduce<
    (typeof statistics.repositories)[number] | undefined
  >(
    (top, repository) =>
      !top || repository.usedBytes > top.usedBytes ? repository : top,
    undefined,
  );
  return (
    <MetricStrip
      items={[
        {
          label: t("dashboard.totalRequests"),
          value: statistics
            ? formatNumber(statistics.totals.requests[window], locale)
            : "—",
          hint: statistics
            ? t("dashboard.denied", {
                number: formatNumber(statistics.totals.denied[window], locale),
              })
            : statisticsHint,
        },
        {
          label: t("dashboard.totalObjects"),
          value: statistics
            ? formatNumber(statistics.totals.objectCount, locale)
            : "—",
          hint: t("dashboard.currentRepositoryCapacityBasis"),
        },
        {
          label: t("dashboard.storageUsed"),
          value: statistics ? formatBytes(statistics.totals.usedBytes) : "—",
          hint: largest
            ? t("dashboard.largestRepository", {
                name: largest.name,
                size: formatBytes(largest.usedBytes),
              })
            : statisticsHint,
        },
        {
          label: t("dashboard.repositories"),
          value: statistics?.repositories.length ?? "—",
          hint: statistics
            ? t("dashboard.formatsCovered", {
                count: new Set(
                  statistics.repositories.map(
                    (repository) => repository.format,
                  ),
                ).size,
              })
            : statisticsHint,
        },
        {
          label: t("dashboard.groups"),
          value: groups?.length ?? "—",
          hint: groups
            ? t("dashboard.memberReferences", {
                groups: groups.reduce(
                  (n, g) => n + (g.members?.length ?? 0),
                  0,
                ),
              })
            : groupsResource.loading
              ? t("dashboard.loadingGroups")
              : t("dashboard.groupStatisticsUnavailable"),
        },
      ]}
    />
  );
}

export function RepositoryActivity({
  statisticsResource,
  window,
}: {
  statisticsResource: DashboardResource<OverviewStatistics>;
  window: StatisticsWindow;
}) {
  const { locale, t } = usePreferences();
  const statistics = statisticsResource.data;
  const rankedRepositories = statistics
    ? sortRepositoryStatistics(statistics.repositories, window)
    : [];
  const repositoryColumns: ColumnsType<OverviewRepositoryStatistics> = [
    {
      title: t("dashboard.name"),
      dataIndex: "name",
      key: "name",
      render: (value: string, repository) => (
        <Link
          to={`/repositories/${repository.repositoryId}`}
          className="font-medium text-[var(--ag-content-strong)] hover:text-[var(--ag-link-hover)]"
        >
          {value}
        </Link>
      ),
    },
    {
      title: t("dashboard.format"),
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (value: OverviewRepositoryStatistics["format"]) => (
        <FormatBadge format={value} />
      ),
    },
    {
      title: t("dashboard.objects"),
      dataIndex: "objectCount",
      key: "objectCount",
      width: 120,
      render: (value: number) => formatNumber(value, locale),
    },
    {
      title: t("dashboard.requests"),
      key: "requests",
      width: 165,
      render: (_, repository) => (
        <span>
          {formatNumber(repository.requests[window], locale)}
          {repository.denied[window] > 0 && (
            <span className="ml-2 text-xs text-[var(--ag-status-warning)]">
              {t("dashboard.denied2", {
                number: formatNumber(repository.denied[window], locale),
              })}
            </span>
          )}
        </span>
      ),
    },
    {
      title: t("dashboard.storage"),
      dataIndex: "usedBytes",
      key: "usedBytes",
      width: 125,
      render: (value: number) => formatBytes(value),
    },
  ];

  return (
    <Card className="ag-page-primary">
      <CardHeader
        title={t("dashboard.repositoryActivity")}
        extra={
          <Link
            to="/repositories"
            className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
          >
            {t("dashboard.viewAll")}
          </Link>
        }
      />
      {statistics ? (
        <ConsoleTable<OverviewRepositoryStatistics>
          rowKey="repositoryId"
          dataSource={rankedRepositories.slice(0, 10)}
          columns={repositoryColumns}
          pagination={false}
          locale={{
            emptyText: (
              <EmptyState
                title={t("dashboard.noRepositories")}
                hint={t("dashboard.createARepositoryToSeeRequestsObjectsAnd")}
              />
            ),
          }}
          scroll={{ x: 700 }}
        />
      ) : statisticsResource.loading ? (
        <Loading label={t("dashboard.loadingRepositoryStatistics")} />
      ) : (
        <EmptyState
          compact
          title={t("dashboard.repositoryStatisticsUnavailable")}
        />
      )}
    </Card>
  );
}

export function StorageByFormat({
  statisticsResource,
}: {
  statisticsResource: DashboardResource<OverviewStatistics>;
}) {
  const { t } = usePreferences();
  const statistics = statisticsResource.data;
  const bytesByFormat = statistics?.repositories.reduce<Record<string, number>>(
    (byFormat, item) => {
      byFormat[item.format] = (byFormat[item.format] ?? 0) + item.usedBytes;
      return byFormat;
    },
    {},
  );

  return (
    <Card>
      <CardHeader
        title={t("dashboard.storageByFormat")}
        extra={
          <Link
            to="/repositories"
            className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
          >
            {t("dashboard.viewRepositories")}
          </Link>
        }
      />
      <div className="px-5 py-6">
        {statistics ? (
          <StorageByFormatChart
            bytesByFormat={bytesByFormat ?? {}}
            totalBytes={statistics.totals.usedBytes}
          />
        ) : statisticsResource.loading ? (
          <Loading label={t("dashboard.loadingCapacityStatistics")} />
        ) : (
          <EmptyState
            compact
            title={t("dashboard.capacityStatisticsUnavailable")}
          />
        )}
      </div>
    </Card>
  );
}

export function RecentAudits({
  auditsResource,
}: {
  auditsResource: DashboardResource<AuditRecord[]>;
}) {
  const { t } = usePreferences();
  const audits = auditsResource.data;
  return (
    <Card>
      <CardHeader
        title={t("dashboard.recentAuditEvents")}
        extra={
          <div className="flex items-center gap-3 text-xs">
            <span className="text-fg-tertiary">
              {audits ? t("dashboard.latest", { length: audits.length }) : ""}
            </span>
            <Link
              to="/audits"
              className="text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
            >
              {t("dashboard.viewAll")}
            </Link>
          </div>
        }
      />
      {auditsResource.error ? (
        <ErrorBanner
          title={t("dashboard.auditEventsUnavailable")}
          error={auditsResource.error}
          onRetry={auditsResource.reload}
          tone={audits ? "warning" : "error"}
        />
      ) : null}
      {audits ? (
        audits.length > 0 ? (
          <RecentAuditList records={audits} />
        ) : (
          <EmptyState
            title={t("dashboard.noAuditRecords")}
            hint={t(
              "dashboard.recentDecisionsAppearHereOnceAccessOrPublishing",
            )}
          />
        )
      ) : auditsResource.loading ? (
        <Loading label={t("dashboard.loadingAuditEvents")} />
      ) : null}
    </Card>
  );
}
