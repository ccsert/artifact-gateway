import { useEffect, useState } from "react";
import { DatabaseOutlined } from "@ant-design/icons";
import { Button, Segmented } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useNavigate } from "react-router-dom";
import { listGroups, listAudits, getOverviewStatistics } from "../../client";
import type {
  Group,
  AuditRecord,
  OverviewStatistics,
  OverviewRepositoryStatistics,
  OverviewWindowCounts,
} from "../../client";
import { PageHeader, Card, CardHeader } from "../../components/ui/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../../components/ui/Feedback";
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
import { preloadDashboardPiePlot } from "../../components/ui/dashboard-charts/loadDashboardPiePlot";

type StatisticsWindow = keyof OverviewWindowCounts;

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

export function sortRepositoryStatistics(
  repositories: OverviewRepositoryStatistics[],
  window: StatisticsWindow,
) {
  return [...repositories].sort(
    (left, right) =>
      right.requests[window] - left.requests[window] ||
      left.name.localeCompare(right.name),
  );
}

export function DashboardPage() {
  const { locale, text } = usePreferences();
  const navigate = useNavigate();
  const groupsResource = useDashboardResource(readGroups);
  const auditsResource = useDashboardResource(readAudits);
  const statisticsResource = useDashboardResource(readStatistics);
  const groups = groupsResource.data;
  const audits = auditsResource.data;
  const statistics = statisticsResource.data;
  const [window, setWindow] = useState<StatisticsWindow>("sevenDays");

  useEffect(() => {
    preloadDashboardPiePlot();
  }, []);

  const bytesByFormat = statistics?.repositories.reduce<Record<string, number>>(
    (byFormat, item) => {
      byFormat[item.format] = (byFormat[item.format] ?? 0) + item.usedBytes;
      return byFormat;
    },
    {},
  );
  const rankedRepositories = sortRepositoryStatistics(
    statistics?.repositories ?? [],
    window,
  );
  const repositoryColumns: ColumnsType<OverviewRepositoryStatistics> = [
    {
      title: text("名称", "Name"),
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
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (value: OverviewRepositoryStatistics["format"]) => (
        <FormatBadge format={value} />
      ),
    },
    {
      title: text("对象数", "Objects"),
      dataIndex: "objectCount",
      key: "objectCount",
      width: 120,
      render: (value: number) => formatNumber(value, locale),
    },
    {
      title: text("请求量", "Requests"),
      key: "requests",
      width: 165,
      render: (_, repository) => (
        <span>
          {formatNumber(repository.requests[window], locale)}
          {repository.denied[window] > 0 && (
            <span className="ml-2 text-xs text-[var(--ag-status-warning)]">
              {text(
                `拒绝 ${formatNumber(repository.denied[window], locale)}`,
                `${formatNumber(repository.denied[window], locale)} denied`,
              )}
            </span>
          )}
        </span>
      ),
    },
    {
      title: text("存储", "Storage"),
      dataIndex: "usedBytes",
      key: "usedBytes",
      width: 125,
      render: (value: number) => formatBytes(value),
    },
  ];

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
      <MetricStrip
        items={[
          {
            label: text("总请求量", "Total requests"),
            value: statistics
              ? formatNumber(statistics.totals.requests[window], locale)
              : "—",
            hint: statistics
              ? text(
                  `其中拒绝 ${formatNumber(statistics.totals.denied[window], locale)}`,
                  `${formatNumber(statistics.totals.denied[window], locale)} denied`,
                )
              : text(
                  statisticsResource.loading
                    ? "正在加载仓库统计…"
                    : "仓库统计不可用",
                  statisticsResource.loading
                    ? "Loading repository statistics…"
                    : "Repository statistics unavailable",
                ),
          },
          {
            label: text("总对象数", "Total objects"),
            value: statistics
              ? formatNumber(statistics.totals.objectCount, locale)
              : "—",
            hint: text("当前仓库容量口径", "Current repository capacity basis"),
          },
          {
            label: text("存储占用", "Storage used"),
            value: statistics ? formatBytes(statistics.totals.usedBytes) : "—",
          },
          {
            label: text("仓库总数", "Repositories"),
            value: statistics?.repositories.length ?? "—",
          },
          {
            label: text("分组", "Groups"),
            value: groups?.length ?? "—",
            hint: groups
              ? text(
                  `共 ${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} 个成员引用`,
                  `${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} member references`,
                )
              : text(
                  groupsResource.loading ? "正在加载分组…" : "分组统计不可用",
                  groupsResource.loading
                    ? "Loading groups…"
                    : "Group statistics unavailable",
                ),
          },
        ]}
      />

      <Card className="ag-page-primary">
        <CardHeader
          title={text("仓库活动", "Repository activity")}
          extra={
            <Link
              to="/repositories"
              className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
            >
              {text("查看全部 →", "View all →")}
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
                  title={text("暂无仓库", "No repositories")}
                  hint={text(
                    "创建仓库后，这里会展示请求量、对象数与存储占用。",
                    "Create a repository to see requests, objects, and storage here.",
                  )}
                />
              ),
            }}
            scroll={{ x: 700 }}
          />
        ) : statisticsResource.loading ? (
          <Loading
            label={text("正在加载仓库统计…", "Loading repository statistics…")}
          />
        ) : (
          <EmptyState
            compact
            title={text("仓库统计不可用", "Repository statistics unavailable")}
          />
        )}
      </Card>

      <div className="grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title={text("存储占用（按格式）", "Storage by format")}
            extra={
              <Link
                to="/repositories"
                className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
              >
                {text("查看仓库 →", "View repositories →")}
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
              <Loading
                label={text(
                  "正在加载容量统计…",
                  "Loading capacity statistics…",
                )}
              />
            ) : (
              <EmptyState
                compact
                title={text(
                  "容量统计不可用",
                  "Capacity statistics unavailable",
                )}
              />
            )}
          </div>
        </Card>

        <Card>
          <CardHeader
            title={text("最近审计事件", "Recent audit events")}
            extra={
              <div className="flex items-center gap-3 text-xs">
                <span className="text-fg-tertiary">
                  {text(
                    audits ? `${audits.length} 条最新记录` : "",
                    audits ? `${audits.length} latest` : "",
                  )}
                </span>
                <Link
                  to="/audits"
                  className="text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
                >
                  {text("查看全部 →", "View all →")}
                </Link>
              </div>
            }
          />
          {auditsResource.error ? (
            <ErrorBanner
              title={text("审计事件加载失败", "Audit events unavailable")}
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
                title={text("暂无审计记录", "No audit records")}
                hint={text(
                  "产生访问或发布行为后，这里会列出最近的判定记录。",
                  "Recent decisions appear here once access or publishing is recorded.",
                )}
              />
            )
          ) : auditsResource.loading ? (
            <Loading
              label={text("正在加载审计事件…", "Loading audit events…")}
            />
          ) : null}
        </Card>
      </div>
    </div>
  );
}
