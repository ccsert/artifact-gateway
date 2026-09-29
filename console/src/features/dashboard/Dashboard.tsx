import { useCallback, useEffect, useState } from "react";
import { DatabaseOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useNavigate } from "react-router-dom";
import {
  listRepositories,
  listGroups,
  listAudits,
  listRepositoryCapacities,
} from "../../client";
import type { Repository, Group, AuditRecord } from "../../client";
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
  loadDashboardHistory,
  recordDashboardSample,
  type DashboardSample,
} from "../../lib/history";
import {
  ConsoleTable,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import {
  DashboardTrendCharts,
  StorageByFormatChart,
} from "../../components/ui/DashboardCharts";
import { usePreferences } from "../../lib/preferences";

export function DashboardPage() {
  const { locale, text } = usePreferences();
  const navigate = useNavigate();
  const [repos, setRepos] = useState<Repository[] | null>(null);
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [audits, setAudits] = useState<AuditRecord[] | null>(null);
  const [totalBytes, setTotalBytes] = useState<number | null>(null);
  const [totalObjects, setTotalObjects] = useState<number | null>(null);
  const [bytesByFormat, setBytesByFormat] = useState<Record<
    string,
    number
  > | null>(null);
  const [history, setHistory] = useState<DashboardSample[]>(() =>
    loadDashboardHistory(),
  );
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [r, g, a, c] = await Promise.all([
        listRepositories({ query: { pageSize: 200 } }),
        listGroups({ query: { pageSize: 200 } }),
        listAudits({ query: { limit: 8 } }),
        listRepositoryCapacities(),
      ]);
      if (r.error) throw r.error;
      // groups / audits 在当前后端构建中可能未启用（404），降级为空数据
      if (g.error && !isNotFound(g.error)) throw g.error;
      if (a.error && !isNotFound(a.error)) throw a.error;
      const repoList = r.data?.items ?? [];
      const operationalRepos = repoList.filter(
        (repository) => repository.state !== "deleted",
      );
      setRepos(operationalRepos);
      setGroups(g.data?.items ?? []);
      setAudits(a.data ?? []);

      // 汇总各 active 仓库容量（失败/404 的仓库跳过）
      const activeRepos = operationalRepos.filter(
        (repository) => repository.state === "active",
      );
      const activeRepositoryIds = new Set(activeRepos.map((repo) => repo.id));
      let bytes = 0;
      let objects = 0;
      let any = false;
      const byFormat: Record<string, number> = {};
      for (const capacity of c.data ?? []) {
        if (activeRepositoryIds.has(capacity.repositoryId)) {
          bytes += capacity.usedBytes;
          objects += capacity.objectCount;
          any = true;
          byFormat[capacity.format] =
            (byFormat[capacity.format] ?? 0) + capacity.usedBytes;
        }
      }
      setTotalBytes(any ? bytes : null);
      setTotalObjects(any ? objects : null);
      setBytesByFormat(any ? byFormat : null);
      setHistory(
        recordDashboardSample({
          t: Date.now(),
          repos: operationalRepos.length,
          bytes: any ? bytes : null,
          objects: any ? objects : null,
        }),
      );
    } catch (e) {
      setError(e);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A refresh failure keeps the loaded overview on screen and says what
  // happened above it; only a first load has nothing to fall back to.
  const firstLoad = !repos || !groups || !audits;
  if (firstLoad) {
    return (
      <div className="ag-page-stack">
        <PageHeader title={text("总览", "Overview")} />
        {error ? <ErrorBanner error={error} onRetry={load} /> : <Loading />}
      </div>
    );
  }

  const active = repos.filter((r) => r.state === "active").length;
  const repositoryColumns: ColumnsType<Repository> = [
    {
      title: text("名称", "Name"),
      dataIndex: "name",
      key: "name",
      render: (value: string, repository) => (
        <Link
          to={`/repositories/${repository.id}`}
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
      render: (value: Repository["format"]) => <FormatBadge format={value} />,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 130,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
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
          <Button
            type="primary"
            icon={<DatabaseOutlined />}
            onClick={() => navigate("/repositories")}
          >
            {text("管理仓库", "Manage repositories")}
          </Button>
        }
      />
      {error ? <ErrorBanner error={error} onRetry={load} /> : null}
      <MetricStrip
        items={[
          {
            label: text("仓库总数", "Repositories"),
            value: repos.length,
            hint: text(`${active} 个活跃`, `${active} active`),
          },
          {
            label: text("分组", "Groups"),
            value: groups.length,
            hint: text(
              `共 ${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} 个成员引用`,
              `${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} member references`,
            ),
          },
          {
            label: text("存储占用", "Storage used"),
            value: totalBytes !== null ? formatBytes(totalBytes) : "—",
            hint:
              totalObjects !== null
                ? text(
                    `${formatNumber(totalObjects, locale)} 个对象`,
                    `${formatNumber(totalObjects, locale)} objects`,
                  )
                : text("容量未启用", "Capacity unavailable"),
          },
        ]}
      />

      <div className="ag-page-primary grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-5 xl:items-stretch">
        <Card className="xl:col-span-2 xl:h-full">
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
              totalBytes={totalBytes}
            />
          </div>
        </Card>

        <Card className="xl:col-span-3 xl:h-full">
          <CardHeader
            title={text("近期趋势", "Recent trend")}
            extra={
              history.length > 0 ? (
                <span className="text-xs text-zinc-600">
                  {text("自", "Since")}{" "}
                  {formatDate(new Date(history[0].t).toISOString(), locale)}
                </span>
              ) : undefined
            }
          />
          <DashboardTrendCharts history={history} />
          <p className="border-t border-zinc-800/60 px-5 py-3 text-xs leading-5 text-zinc-600">
            {text(
              "基于浏览器本地的访问采样，仅反映本机记录的近期变化；完整时序需后端 metrics 端点。",
              "Browser-local samples only reflect recent changes recorded on this device. Full time series require a backend metrics endpoint.",
            )}
          </p>
        </Card>
      </div>

      <div className="grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title={text("仓库", "Repositories")}
            extra={
              <Link
                to="/repositories"
                className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
              >
                {text("查看全部 →", "View all →")}
              </Link>
            }
          />
          <ConsoleTable<Repository>
            rowKey="id"
            dataSource={repos.slice(0, 6)}
            columns={repositoryColumns}
            pagination={false}
            locale={{
              emptyText: (
                <EmptyState
                  title={text("暂无仓库", "No repositories")}
                  hint={text(
                    "创建第一个仓库后，这里会显示发布与下载概况。",
                    "Create the first repository to see publishing and downloads here.",
                  )}
                />
              ),
            }}
            scroll={{ x: 520 }}
          />
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
