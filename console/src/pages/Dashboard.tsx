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

const FORMAT_COLORS: Record<string, string> = {
  oci: "#22d3ee",
  maven: "#fbbf24",
  conan: "#a78bfa",
  raw: "#38bdf8",
};
const FORMAT_ORDER = ["oci", "maven", "conan", "raw"] as const;

export function DashboardPage() {
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
        <PageHeader title="总览" />
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
      title: "名称",
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
      title: "格式",
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (value: Repository["format"]) => <FormatBadge format={value} />,
    },
    {
      title: "状态",
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
      title: "时间",
      dataIndex: "occurredAt",
      key: "occurredAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap font-mono text-xs text-zinc-400">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "操作",
      dataIndex: "operation",
      key: "operation",
      width: 140,
      render: (value: string | undefined) => (
        <span className="text-xs text-zinc-300">{value ?? "—"}</span>
      ),
    },
    {
      title: "结果",
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
        title="总览"
        description="Artifact Gateway 运行状态一览"
        actions={
          <Button
            type="primary"
            icon={<DatabaseOutlined />}
            onClick={() => navigate("/repositories")}
          >
            管理仓库
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
              {inactive > 0 ? "平台需要关注" : "平台运行正常"}
            </div>
            <div className="mt-0.5 text-xs text-zinc-500">
              {active} 个活跃仓库
              {inactive > 0 ? `，${inactive} 个需要关注` : "，暂无待处理异常"}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-5 text-xs text-zinc-500">
          <span>
            <span className="mr-1.5 text-zinc-300">API</span>v2
          </span>
          <span>
            <span className="mr-1.5 text-zinc-300">采样</span>
            {formatDate(new Date().toISOString())}
          </span>
        </div>
      </div>
      <MetricStrip
        items={[
          { label: "仓库总数", value: repos.length, hint: `${active} 个活跃` },
          {
            label: "分组",
            value: groups.length,
            hint: `共 ${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} 个成员引用`,
          },
          {
            label: "格式分布",
            value: FORMAT_ORDER.map(
              (format) => `${format} ${formatCount(format)}`,
            ).join(" · "),
            hint: "OCI · Maven · Conan · Raw",
          },
          {
            label: "存储占用",
            value: totalBytes !== null ? formatBytes(totalBytes) : "—",
            hint:
              totalObjects !== null
                ? `${formatNumber(totalObjects)} 个对象`
                : "容量未启用",
          },
          { label: "最近审计", value: audits.length, hint: "最新记录条数" },
        ]}
      />

      <div className="mt-6 grid grid-cols-1 gap-4 xl:grid-cols-5">
        <Card className="xl:col-span-2">
          <CardHeader
            title="存储占用（按格式）"
            extra={
              <Link
                to="/repositories"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                查看仓库 →
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
                centerSub="合计"
              />
            ) : (
              <div className="py-8 text-center text-sm text-zinc-600">
                容量统计未启用或暂无数据
              </div>
            )}
          </div>
        </Card>

        <Card className="xl:col-span-3">
          <CardHeader
            title="近期趋势"
            extra={
              history.length > 0 ? (
                <span className="text-[11px] text-zinc-600">
                  自 {formatDate(new Date(history[0].t).toISOString())}
                </span>
              ) : undefined
            }
          />
          <div className="grid grid-cols-1 gap-6 px-5 py-6 sm:grid-cols-2">
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
                仓库数
              </div>
              <Sparkline
                data={history.map((s) => s.repos)}
                color="#22d3ee"
                format={(n) => `${n}`}
              />
            </div>
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">
                存储占用
              </div>
              <Sparkline
                data={history
                  .map((s) => s.bytes)
                  .filter((b): b is number => b !== null)}
                color="#fbbf24"
                format={(n) => formatBytes(n)}
                label="容量未启用"
              />
            </div>
          </div>
          <p className="border-t border-zinc-800/60 px-5 py-3 text-[11px] text-zinc-600">
            基于浏览器本地的访问采样，仅反映本机记录的近期变化；完整时序需后端
            metrics 端点。
          </p>
        </Card>
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title="仓库"
            extra={
              <Link
                to="/repositories"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                查看全部 →
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
            locale={{ emptyText: "暂无仓库" }}
            scroll={{ x: 520 }}
          />
        </Card>

        <Card>
          <CardHeader
            title="最近审计事件"
            extra={
              <Link
                to="/audits"
                className="text-xs text-cyan-400 hover:text-cyan-300"
              >
                查看全部 →
              </Link>
            }
          />
          <Table<AuditRecord>
            className="ag-console-table"
            rowKey={(record, index) =>
              `${record.requestId ?? "audit"}-${index}`
            }
            size="middle"
            dataSource={audits}
            columns={auditColumns}
            pagination={false}
            locale={{ emptyText: "暂无审计记录" }}
            scroll={{ x: 520 }}
          />
        </Card>
      </div>
    </div>
  );
}
