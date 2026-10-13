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
  const { text } = usePreferences();
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
        title={text("总览", "Overview")}
        description={text(
          "Artifact Gateway 运行状态一览",
          "Artifact Gateway runtime at a glance",
        )}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              aria-label={text("统计窗口", "Statistics window")}
              value={window}
              onChange={(value) => setWindow(value as StatisticsWindow)}
              options={[
                { label: text("1 天", "1 day"), value: "oneDay" },
                { label: text("7 天", "7 days"), value: "sevenDays" },
                { label: text("30 天", "30 days"), value: "thirtyDays" },
              ]}
            />
            <Button
              type="primary"
              icon={<DatabaseOutlined />}
              onClick={() => navigate("/repositories")}
            >
              {text("管理仓库", "Manage repositories")}
            </Button>
          </div>
        }
      />
      {statisticsResource.error ? (
        <ErrorBanner
          title={text("仓库统计加载失败", "Repository statistics unavailable")}
          error={statisticsResource.error}
          onRetry={statisticsResource.reload}
          tone={statistics ? "warning" : "error"}
        />
      ) : null}
      {groupsResource.error ? (
        <ErrorBanner
          title={text("分组统计加载失败", "Group statistics unavailable")}
          error={groupsResource.error}
          onRetry={groupsResource.reload}
          tone={groups ? "warning" : "error"}
        />
      ) : null}
      <SectionBoundary
        title={text("运行统计暂时无法显示", "Runtime summary is unavailable")}
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
        title={text(
          "仓库活动暂时无法显示",
          "Repository activity is unavailable",
        )}
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
          title={text(
            "存储占用暂时无法显示",
            "Storage by format is unavailable",
          )}
          recoveryKeys={[statisticsResource.data]}
          onReset={statisticsResource.reload}
        >
          <StorageByFormat statisticsResource={statisticsResource} />
        </SectionBoundary>
        <SectionBoundary
          title={text(
            "最近审计事件暂时无法显示",
            "Recent audit events are unavailable",
          )}
          recoveryKeys={[auditsResource.data]}
          onReset={auditsResource.reload}
        >
          <RecentAudits auditsResource={auditsResource} />
        </SectionBoundary>
      </div>
    </div>
  );
}
