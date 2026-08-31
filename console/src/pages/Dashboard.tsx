import { useCallback, useEffect, useState } from "react";
import {
  CloudDownloadOutlined,
  DatabaseOutlined,
  FileSearchOutlined,
  InboxOutlined,
  RocketOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import { Button, Grid, Steps, Table } from "antd";
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
import { formatBytes, formatDate, formatNumber } from "../lib/format";
import {
  loadDashboardHistory,
  recordDashboardSample,
  type DashboardSample,
} from "../lib/history";
import { MetricStrip } from "../components/ConsolePrimitives";
import {
  DashboardTrendCharts,
  StorageByFormatChart,
} from "../components/DashboardCharts";
import { usePreferences } from "../lib/preferences";

export function DashboardPage() {
  const { locale, text } = usePreferences();
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
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
      <div className="ag-page-stack">
        <PageHeader title={text("总览", "Overview")} />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  if (!repos || !groups || !audits) return <Loading />;

  const active = repos.filter((r) => r.state === "active").length;
  const inactive = repos.length - active;
  const healthTone =
    inactive > 0
      ? "text-[var(--ag-status-warning)]"
      : "text-[var(--ag-status-success)]";
  const lifecycleStages = [
    {
      key: "source",
      icon: <InboxOutlined />,
      title: text("来源", "Source"),
      eyebrow: text("仓库入口", "Repository entry"),
      description: text(
        "托管、代理与分组仓库定义可信来源。",
        "Hosted, proxy, and group repositories define trusted sources.",
      ),
      meta: text(`${active} 个活跃仓库`, `${active} active repositories`),
      to: "/repositories",
    },
    {
      key: "scan",
      icon: <FileSearchOutlined />,
      title: text("扫描", "Scan"),
      eyebrow: text("摘要与风险", "Digest and risk"),
      description: text(
        "按坐标和摘要定位制品，在仓库详情中执行扫描与重检。",
        "Resolve artifacts by coordinate and digest, then scan or rescan from repository details.",
      ),
      to: "/search",
    },
    {
      key: "quarantine",
      icon: <SafetyCertificateOutlined />,
      title: text("隔离闸门", "Quarantine gate"),
      eyebrow: text("仅风险命中时", "Only when risk matches"),
      description: text(
        "风险制品进入条件隔离；未命中的可信制品继续流转。",
        "Risky artifacts enter conditional quarantine; trusted artifacts continue.",
      ),
      to: "/repositories",
      conditional: true,
    },
    {
      key: "promote",
      icon: <RocketOutlined />,
      title: text("晋级与复制", "Promote and replicate"),
      eyebrow: text("生命周期任务", "Lifecycle jobs"),
      description: text(
        "通过可审计任务把可信版本推进到目标仓库。",
        "Move trusted versions to target repositories through auditable jobs.",
      ),
      to: "/operations",
    },
    {
      key: "distribute",
      icon: <CloudDownloadOutlined />,
      title: text("分发", "Distribute"),
      eyebrow: text("原生协议", "Native protocols"),
      description: text(
        "通过原生客户端和公开目录提供受控读取。",
        "Provide governed reads through native clients and the public catalog.",
      ),
      to: "/browse",
    },
  ];
  const wideLifecycle = screens.lg === true;
  const lifecycleItems = lifecycleStages.map((stage, index) => ({
    key: stage.key,
    className: stage.conditional ? "ag-lifecycle-step-conditional" : undefined,
    status: "wait" as const,
    icon: (
      <span className="ag-lifecycle-stage-icon" aria-hidden="true">
        {stage.icon}
      </span>
    ),
    title: (
      <span className="ag-lifecycle-step-heading">
        <span className="ag-lifecycle-stage-eyebrow">
          <span className="ag-lifecycle-stage-index" aria-hidden="true">
            {String(index + 1).padStart(2, "0")}
          </span>
          {stage.eyebrow}
        </span>
        <span className="ag-lifecycle-stage-title">{stage.title}</span>
      </span>
    ),
    content: (
      <span className="ag-lifecycle-step-copy">
        <span className="ag-lifecycle-stage-description">
          {stage.description}
        </span>
        {stage.meta && (
          <span className="ag-lifecycle-stage-meta">{stage.meta}</span>
        )}
      </span>
    ),
  }));
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
      <div className="ag-health-strip flex items-center justify-between gap-6 border-y border-zinc-800/80 py-3">
        <div className="flex items-center gap-3">
          <span
            className={`flex h-7 w-7 items-center justify-center rounded-full ${inactive > 0 ? "bg-[var(--ag-status-warning-soft)]" : "bg-[var(--ag-status-success-soft)]"} ${healthTone}`}
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
        <div className="ag-health-meta flex items-center gap-5 text-xs text-zinc-500">
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
      <section
        className="ag-lifecycle"
        aria-labelledby="artifact-lifecycle-title"
      >
        <div className="ag-lifecycle-heading">
          <div>
            <h2
              id="artifact-lifecycle-title"
              className="text-base font-semibold tracking-tight text-zinc-100"
            >
              {text("可信制品路径", "Trusted artifact path")}
            </h2>
            <p className="mt-1 max-w-2xl text-sm leading-6 text-zinc-500">
              {text(
                "从来源接入到受控分发，按运营任务组织治理入口。",
                "Govern artifacts from source intake to controlled distribution through operational entry points.",
              )}
            </p>
          </div>
          <span className="ag-lifecycle-legend text-xs text-zinc-500">
            <span aria-hidden="true" />
            {text("条件闸门", "Conditional gate")}
          </span>
        </div>
        <Steps
          className="ag-lifecycle-steps"
          type={wideLifecycle ? "navigation" : "default"}
          orientation={wideLifecycle ? "horizontal" : "vertical"}
          variant="outlined"
          responsive={false}
          current={-1}
          items={lifecycleItems}
          onChange={(index) => navigate(lifecycleStages[index].to)}
          classNames={{
            item: "ag-lifecycle-step",
            itemWrapper: "ag-lifecycle-step-wrapper",
            itemIcon: "ag-lifecycle-step-icon",
            itemSection: "ag-lifecycle-step-section",
            itemHeader: "ag-lifecycle-step-header",
            itemTitle: "ag-lifecycle-step-title-slot",
            itemContent: "ag-lifecycle-step-content",
            itemRail: "ag-lifecycle-step-rail",
          }}
          style={{ alignItems: "stretch" }}
        />
      </section>
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
