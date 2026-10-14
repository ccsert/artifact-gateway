import { useEffect, useState } from "react";
import { DatabaseOutlined } from "@ant-design/icons";
import { Button, Segmented } from "antd";
import { useNavigate } from "react-router-dom";
import { listGroups, listAudits, getOverviewStatistics } from "../../client";
import type { Group, AuditRecord, OverviewStatistics } from "../../client";
import { PageHeader } from "../../components/ui/Layout";
import { ErrorBanner, isNotFound } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { useDashboardResource } from "./useDashboardResource";
import { dashboardKeys } from "../../lib/query/keys";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { preloadDashboardPiePlot } from "../../components/ui/dashboard-charts/loadDashboardPiePlot";

import type { StatisticsWindow } from "./dashboardModel";
import {
  DashboardMetrics,
  RecentAudits,
  RepositoryActivity,
  StorageByFormat,
} from "./DashboardSections";

export { sortRepositoryStatistics } from "./dashboardModel";

async function readGroups(signal: AbortSignal): Promise<Group[]> {
  const result = await listGroups({ query: { pageSize: 200 }, signal });
  if (result.error && !isNotFound(result.error)) throw result.error;
  return result.data?.items ?? [];
}

async function readAudits(signal: AbortSignal): Promise<AuditRecord[]> {
  const result = await listAudits({ query: { limit: 8 }, signal });
  if (result.error && !isNotFound(result.error)) throw result.error;
  return result.data ?? [];
}

async function readStatistics(
  signal: AbortSignal,
): Promise<OverviewStatistics> {
  const result = await getOverviewStatistics({ signal });
  if (result.error || !result.data) {
    throw result.error ?? new Error("Overview statistics unavailable");
  }
  return result.data;
}

export function DashboardPage() {
  const { t } = usePreferences();
  const navigate = useNavigate();
  const groupsResource = useDashboardResource(
    dashboardKeys.groups(),
    readGroups,
  );
  const auditsResource = useDashboardResource(
    dashboardKeys.audits(),
    readAudits,
  );
  const statisticsResource = useDashboardResource(
    dashboardKeys.statistics(),
    readStatistics,
  );
  const groups = groupsResource.data;
  const statistics = statisticsResource.data;
  const [window, setWindow] = useState<StatisticsWindow>("sevenDays");

  useEffect(() => {
    preloadDashboardPiePlot();
  }, []);

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("dashboard.overview")}
        description={t("dashboard.artifactGatewayRuntimeAtAGlance")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              aria-label={t("dashboard.statisticsWindow")}
              value={window}
              onChange={(value) => setWindow(value as StatisticsWindow)}
              options={[
                { label: t("dashboard.1Day"), value: "oneDay" },
                { label: t("dashboard.7Days"), value: "sevenDays" },
                { label: t("dashboard.30Days"), value: "thirtyDays" },
              ]}
            />
            <Button
              type="primary"
              icon={<DatabaseOutlined />}
              onClick={() => navigate("/repositories")}
            >
              {t("dashboard.manageRepositories")}
            </Button>
          </div>
        }
      />
      {statisticsResource.error ? (
        <ErrorBanner
          title={t("dashboard.repositoryStatisticsUnavailable2")}
          error={statisticsResource.error}
          onRetry={statisticsResource.reload}
          tone={statistics ? "warning" : "error"}
        />
      ) : null}
      {groupsResource.error ? (
        <ErrorBanner
          title={t("dashboard.groupStatisticsUnavailable2")}
          error={groupsResource.error}
          onRetry={groupsResource.reload}
          tone={groups ? "warning" : "error"}
        />
      ) : null}
      <SectionBoundary
        title={t("dashboard.runtimeSummaryIsUnavailable")}
        recoveryKeys={[statisticsResource.data, groupsResource.data]}
        onReset={() =>
          Promise.all([statisticsResource.reload(), groupsResource.reload()])
        }
      >
        <DashboardMetrics
          statisticsResource={statisticsResource}
          groupsResource={groupsResource}
          window={window}
        />
      </SectionBoundary>
      <SectionBoundary
        title={t("dashboard.repositoryActivityIsUnavailable")}
        recoveryKeys={[statisticsResource.data]}
        onReset={statisticsResource.reload}
      >
        <RepositoryActivity
          statisticsResource={statisticsResource}
          window={window}
        />
      </SectionBoundary>
      <div className="grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-2">
        <SectionBoundary
          title={t("dashboard.storageByFormatIsUnavailable")}
          recoveryKeys={[statisticsResource.data]}
          onReset={statisticsResource.reload}
        >
          <StorageByFormat statisticsResource={statisticsResource} />
        </SectionBoundary>
        <SectionBoundary
          title={t("dashboard.recentAuditEventsAreUnavailable")}
          recoveryKeys={[auditsResource.data]}
          onReset={auditsResource.reload}
        >
          <RecentAudits auditsResource={auditsResource} />
        </SectionBoundary>
      </div>
    </div>
  );
}
