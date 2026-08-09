import { useCallback, useEffect, useState } from "react";
import { DatabaseOutlined } from "@ant-design/icons";
import { Button, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useNavigate } from "react-router-dom";
import {
  listRepositories,
  listGroups,
  listAudits,
  listRepositoryCapacities,
} from "../client";
import type { Repository, Group, AuditRecord } from "../client";
import { PageHeader, Card, CardHeader } from "../components/Layout";
import { Loading, ErrorBanner, isNotFound } from "../components/Feedback";
import { FormatBadge, StateBadge } from "../components/Badge";
import { Donut } from "../components/Donut";
import { Sparkline } from "../components/Sparkline";
import { formatBytes, formatDate, formatNumber } from "../lib/format";
import {
  loadDashboardHistory,
  recordDashboardSample,
  type DashboardSample,
} from "../lib/history";
import { MetricStrip } from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

const FORMAT_COLORS: Record<string, string> = {
  oci: "#22d3ee",
  maven: "#fbbf24",
  conan: "#a78bfa",
  raw: "#38bdf8",
  npm: "#f43f5e",
  pypi: "#34d399",
};
const FORMAT_ORDER = ["oci", "maven", "npm", "pypi", "conan", "raw"] as const;

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

  if (error)
    return (
      <div>
        <PageHeader title={text("总览", "Overview")} />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  if (!repos || !groups || !audits) return <Loading />;

  const formatCount = (f: string) => repos.filter((r) => r.format === f).length;
  const active = repos.filter((r) => r.state === "active").length;
  const inactive = repos.length - active;
  const healthTone = inactive > 0 ? "text-amber-300" : "text-emerald-300";
  const repositoryColumns: ColumnsType<Repository> = [
    {
      title: text("名称", "Name"),
      dataIndex: "name",
      key: "name",
      render: (value: string, repository) => (
        <Link
          to={`/repositories/${repository.id}`}
          className="font-medium text-zinc-100 hover:text-cyan-300"
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
    <div>
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
      <div className="mb-4 flex items-center justify-between gap-6 border-y border-zinc-800/80 py-3">
        <div className="flex items-center gap-3">
          <span
            className={`flex h-7 w-7 items-center justify-center rounded-full ${inactive > 0 ? "bg-amber-400/10" : "bg-emerald-400/10"} ${healthTone}`}
          >
            <span className="h-2 w-2 rounded-full bg-current" />
          </span>
          <div>
            <div className="text-sm font-semibold text-zinc-100">
              {inactive > 0
                ? text("平台需要关注", "Platform needs attention")
                : text("平台运行正常", "Platform is healthy")}
            </div>
            <div className="mt-0.5 text-xs text-zinc-500">
              {text(`${active} 个活跃仓库`, `${active} active repositories`)}
              {inactive > 0
                ? text(
                    `，${inactive} 个需要关注`,
                    `, ${inactive} need attention`,
                  )
                : text("，暂无待处理异常", ", no pending issues")}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-5 text-xs text-zinc-500">
          <span>
            <span className="mr-1.5 text-zinc-300">API</span>v2
          </span>
          <span>
            <span className="mr-1.5 text-zinc-300">
              {text("采样", "Sampled")}
            </span>
            {formatDate(new Date().toISOString(), locale)}
          </span>
        </div>
      </div>
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
            label: text("格式分布", "Format distribution"),
            value: FORMAT_ORDER.map(
              (format) => `${format} ${formatCount(format)}`,
            ).join(" · "),
            hint: "OCI · Maven · npm · PyPI · Conan · Raw",
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
          {
            label: text("最近审计", "Recent audits"),
            value: audits.length,
            hint: text("最新记录条数", "Latest records"),
          },
        ]}
      />

      <div className="mt-6 grid grid-cols-1 gap-4 xl:grid-cols-5">
        <Card className="xl:col-span-2">
          <CardHeader
            title={text("存储占用（按格式）", "Storage by format")}
            extra={
              <Link
                to="/repositories"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                {text("查看仓库 →", "View repositories →")}
              </Link>
            }
          />
          <div className="px-5 py-6">
            {bytesByFormat && totalBytes ? (
              <Donut
                segments={FORMAT_ORDER.map((f) => ({
                  label: f,
                  value: bytesByFormat[f] ?? 0,
                  color: FORMAT_COLORS[f] ?? "#71717a",
                }))}
                format={(n) => formatBytes(n)}
                centerLabel={formatBytes(totalBytes)}
                centerSub={text("合计", "Total")}
              />
            ) : (
              <div className="py-8 text-center text-sm text-zinc-600">
                {text(
                  "容量统计未启用或暂无数据",
                  "Capacity metrics are unavailable or empty",
                )}
              </div>
            )}
          </div>
        </Card>

        <Card className="xl:col-span-3">
          <CardHeader
            title={text("近期趋势", "Recent trend")}
            extra={
              history.length > 0 ? (
                <span className="text-[11px] text-zinc-600">
                  {text("自", "Since")}{" "}
                  {formatDate(new Date(history[0].t).toISOString(), locale)}
                </span>
              ) : undefined
            }
          />
          <div className="grid grid-cols-1 gap-6 px-5 py-6 sm:grid-cols-2">
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
                {text("仓库数", "Repositories")}
              </div>
              <Sparkline
                data={history.map((s) => s.repos)}
                color="#22d3ee"
                format={(n) => `${n}`}
              />
            </div>
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
                {text("存储占用", "Storage used")}
              </div>
              <Sparkline
                data={history
                  .map((s) => s.bytes)
                  .filter((b): b is number => b !== null)}
                color="#fbbf24"
                format={(n) => formatBytes(n)}
                label={text("容量未启用", "Capacity unavailable")}
              />
            </div>
          </div>
          <p className="border-t border-zinc-800/60 px-5 py-3 text-[11px] text-zinc-600">
            {text(
              "基于浏览器本地的访问采样，仅反映本机记录的近期变化；完整时序需后端 metrics 端点。",
              "Browser-local samples only reflect recent changes recorded on this device. Full time series require a backend metrics endpoint.",
            )}
          </p>
        </Card>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title={text("仓库", "Repositories")}
            extra={
              <Link
                to="/repositories"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                {text("查看全部 →", "View all →")}
              </Link>
            }
          />
          <Table<Repository>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={repos.slice(0, 6)}
            columns={repositoryColumns}
            pagination={false}
            locale={{ emptyText: text("暂无仓库", "No repositories") }}
            scroll={{ x: 520 }}
          />
        </Card>

        <Card>
          <CardHeader
            title={text("最近审计事件", "Recent audit events")}
            extra={
              <Link
                to="/audits"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                {text("查看全部 →", "View all →")}
              </Link>
            }
          />
          <Table<AuditRecord>
            className="ag-console-table"
            rowKey={(record) =>
              record.requestId ??
              record.traceId ??
              `${record.occurredAt}-${record.actor ?? ""}-${record.operation ?? ""}-${record.resource ?? ""}`
            }
            size="middle"
            dataSource={audits}
            columns={auditColumns}
            pagination={false}
            locale={{ emptyText: text("暂无审计记录", "No audit records") }}
            scroll={{ x: 520 }}
          />
        </Card>
      </div>
    </div>
  );
}
