import { useCallback, useEffect, useState } from "react";
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
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { formatBytes, formatDate, formatNumber } from "../../lib/format";
import {
  ConsoleTable,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { StorageByFormatChart } from "../../components/ui/DashboardCharts";
import { usePreferences } from "../../lib/preferences";

type StatisticsWindow = keyof OverviewWindowCounts;

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
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [audits, setAudits] = useState<AuditRecord[] | null>(null);
  const [statistics, setStatistics] = useState<OverviewStatistics | null>(null);
  const [window, setWindow] = useState<StatisticsWindow>("sevenDays");
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [g, a, s] = await Promise.all([
        listGroups({ query: { pageSize: 200 } }),
        listAudits({ query: { limit: 8 } }),
        getOverviewStatistics(),
      ]);
      // groups / audits 在当前后端构建中可能未启用（404），降级为空数据
      if (g.error && !isNotFound(g.error)) throw g.error;
      if (a.error && !isNotFound(a.error)) throw a.error;
      if (s.error) throw s.error;
      setGroups(g.data?.items ?? []);
      setAudits(a.data ?? []);
      setStatistics(s.data ?? null);
    } catch (e) {
      setError(e);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A refresh failure keeps the loaded overview on screen and says what
  // happened above it; only a first load has nothing to fall back to.
  const firstLoad = !groups || !audits || !statistics;
  if (firstLoad) {
    return (
      <div className="ag-page-stack">
        <PageHeader title={text("总览", "Overview")} />
        {error ? <ErrorBanner error={error} onRetry={load} /> : <Loading />}
      </div>
    );
  }

  const bytesByFormat = statistics.repositories.reduce<Record<string, number>>(
    (byFormat, item) => {
      byFormat[item.format] = (byFormat[item.format] ?? 0) + item.usedBytes;
      return byFormat;
    },
    {},
  );
  const rankedRepositories = sortRepositoryStatistics(
    statistics.repositories,
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
  const auditColumns: ColumnsType<AuditRecord> = [
    {
      title: text("时间", "Time"),
      dataIndex: "occurredAt",
      key: "occurredAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap font-mono text-xs text-zinc-400">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Operation"),
      dataIndex: "operation",
      key: "operation",
      width: 140,
      render: (value: string | undefined) => (
        <span className="text-xs text-zinc-300">{value ?? "—"}</span>
      ),
    },
    {
      title: text("结果", "Outcome"),
      dataIndex: "outcome",
      key: "outcome",
      width: 120,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "Actor",
      dataIndex: "actor",
      key: "actor",
      render: (value: string | undefined) => (
        <span
          className="block max-w-32 truncate text-xs text-zinc-500"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
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
      {error ? <ErrorBanner error={error} onRetry={load} /> : null}
      <MetricStrip
        items={[
          {
            label: text("总请求量", "Total requests"),
            value: formatNumber(statistics.totals.requests[window], locale),
            hint: text(
              `其中拒绝 ${formatNumber(statistics.totals.denied[window], locale)}`,
              `${formatNumber(statistics.totals.denied[window], locale)} denied`,
            ),
          },
          {
            label: text("总对象数", "Total objects"),
            value: formatNumber(statistics.totals.objectCount, locale),
            hint: text("当前仓库容量口径", "Current repository capacity basis"),
          },
          {
            label: text("存储占用", "Storage used"),
            value: formatBytes(statistics.totals.usedBytes),
          },
          {
            label: text("仓库总数", "Repositories"),
            value: statistics.repositories.length,
          },
          {
            label: text("分组", "Groups"),
            value: groups.length,
            hint: text(
              `共 ${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} 个成员引用`,
              `${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} member references`,
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
            <StorageByFormatChart
              bytesByFormat={bytesByFormat}
              totalBytes={statistics.totals.usedBytes}
            />
          </div>
        </Card>

        <Card>
          <CardHeader
            title={text("最近审计事件", "Recent audit events")}
            extra={
              <div className="flex items-center gap-3 text-xs">
                <span className="text-zinc-500">
                  {text(
                    `${audits.length} 条最新记录`,
                    `${audits.length} latest`,
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
          <ConsoleTable<AuditRecord>
            rowKey={(record) =>
              record.requestId ??
              record.traceId ??
              `${record.occurredAt}-${record.actor ?? ""}-${record.operation ?? ""}-${record.resource ?? ""}`
            }
            dataSource={audits}
            columns={auditColumns}
            pagination={false}
            locale={{
              emptyText: (
                <EmptyState
                  title={text("暂无审计记录", "No audit records")}
                  hint={text(
                    "产生访问或发布行为后，这里会列出最近的判定记录。",
                    "Recent decisions appear here once access or publishing is recorded.",
                  )}
                />
              ),
            }}
            scroll={{ x: 520 }}
          />
        </Card>
      </div>
    </div>
  );
}
