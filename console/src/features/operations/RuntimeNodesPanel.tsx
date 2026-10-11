import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { RuntimeNode } from "../../client";
import { formatDate } from "../../lib/format";
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { useRuntimeNodes } from "./systemQueries";

function runtimeNodeColumns(
  locale: string,
  text: (chinese: string, english: string) => string,
  currentSessionId: string | null,
): ColumnsType<RuntimeNode> {
  return [
    {
      title: text("实例", "Instance"),
      dataIndex: "instanceId",
      key: "instanceId",
      width: 190,
      render: (value: string, node) => (
        <div className="min-w-0">
          <div className="font-mono text-xs text-fg">{value}</div>
          {node.sessionId === currentSessionId && (
            <div className="text-xs text-[var(--ag-status-success)]">
              {text("当前连接节点", "Connected node")}
            </div>
          )}
          <div
            className="truncate text-xs text-fg-disabled"
            title={node.sessionId}
          >
            {text("会话", "Session")} {node.sessionId.slice(0, 12)}…
          </div>
        </div>
      ),
    },
    {
      title: text("构建版本", "Build version"),
      key: "build",
      width: 190,
      render: (_, node) => (
        <div className="text-xs">
          <div className="text-fg">
            {node.version && node.version !== "unknown"
              ? node.version === "dev"
                ? text("开发构建", "Development build")
                : node.version
              : text("版本未知", "Version unknown")}
          </div>
          <div className="font-mono text-fg-tertiary">
            {node.revision && node.revision !== "unknown"
              ? node.revision.slice(0, 12)
              : text("修订号未知", "Revision unknown")}
          </div>
        </div>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "status",
      key: "status",
      width: 100,
      render: (value: RuntimeNode["status"]) => <StateBadge state={value} />,
    },
    {
      title: text("角色", "Roles"),
      dataIndex: "roles",
      key: "roles",
      width: 150,
      render: (roles: string[]) => (
        <span className="text-xs text-fg-secondary">{roles.join(" · ")}</span>
      ),
    },
    {
      title: text("Worker 能力", "Worker capabilities"),
      key: "capabilities",
      width: 330,
      render: (_, node) => (
        <div className="flex flex-wrap items-center gap-1.5">
          {node.workerFormats.length > 0 ? (
            node.workerFormats.map((format) => (
              <FormatBadge key={format} format={format} />
            ))
          ) : (
            <span className="text-xs text-fg-disabled">
              {text("无格式 Worker", "No format worker")}
            </span>
          )}
          {node.workerKinds.length > 0 && (
            <span className="ml-1 text-xs text-fg-tertiary">
              {node.workerKinds.join(" · ")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: text("启动时间", "Started"),
      dataIndex: "startedAt",
      key: "startedAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("最近心跳", "Last heartbeat"),
      dataIndex: "lastSeenAt",
      key: "lastSeenAt",
      width: 190,
      render: (value: string, node) =>
        node.stoppedAt ? (
          <span className="whitespace-nowrap text-xs text-fg-tertiary">
            {text("已退出", "Stopped")} {formatDate(node.stoppedAt, locale)}
          </span>
        ) : (
          <span className="whitespace-nowrap text-xs text-fg-tertiary">
            {formatDate(value, locale)}
          </span>
        ),
    },
  ];
}

// Older persisted node rows may contain NULL for fields introduced after the
// initial runtime inventory schema. Normalize those responses before they
// reach table renderers so a single legacy row cannot take down Operations.
export function RuntimeNodesPanel({
  pollIntervalMs = 15_000,
}: {
  pollIntervalMs?: number;
}) {
  const { locale, text } = usePreferences();
  const nodesQuery = useRuntimeNodes(pollIntervalMs);
  const nodes = nodesQuery.data?.nodes ?? null;
  const health = nodesQuery.data?.health ?? null;
  const nodeColumns = runtimeNodeColumns(
    locale,
    text,
    nodesQuery.data?.currentSessionId ?? null,
  );
  const error = nodesQuery.error;
  const refreshing = nodesQuery.isFetching && nodes !== null;
  const load = () => void nodesQuery.refetch();

  return (
    <Card className="ag-runtime-nodes-card">
      <CardHeader
        title={text("运行节点", "Runtime nodes")}
        extra={
          <div className="flex items-center gap-2">
            {health && <StateBadge state={health.status} />}
            <span className="text-xs text-fg-tertiary">
              {nodes
                ? text(`${nodes.length} 个实例`, `${nodes.length} instances`)
                : text("加载中", "Loading")}
            </span>
            <Tooltip title={text("刷新运行节点", "Refresh runtime nodes")}>
              <Button
                aria-label={text("刷新运行节点", "Refresh runtime nodes")}
                type="text"
                icon={<ReloadOutlined />}
                loading={refreshing}
                onClick={() => void load()}
              />
            </Tooltip>
          </div>
        }
      />
      {health && (health.issues ?? []).length > 0 && (
        <div className="px-5 pt-4">
          <Alert
            className="ag-feedback-enter"
            type={health.status === "critical" ? "error" : "warning"}
            showIcon
            title={text(
              "集群运行能力需要关注",
              "Cluster capabilities need attention",
            )}
            description={
              <div className="space-y-1">
                {(health.issues ?? []).map((issue) => (
                  <div key={issue.code}>
                    <span className="font-mono text-xs text-fg-tertiary">
                      {issue.code}
                    </span>
                    <span className="ml-2">{issue.message}</span>
                    {issue.affectedNodes?.length ? (
                      <div className="break-all font-mono text-xs text-fg-tertiary">
                        {text("涉及会话", "Sessions")}:{" "}
                        {issue.affectedNodes.join(", ")}
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            }
          />
        </div>
      )}
      {nodes !== null && (
        <p className="px-5 pt-3 text-xs text-fg-tertiary">
          {text(
            "发布版本源未配置，无法判断当前构建是否为最新版本。",
            "No release source is configured, so the latest version cannot be determined.",
          )}
        </p>
      )}
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : nodes === null ? (
        <div className="px-5 py-6">
          <Loading label={text("加载节点清单…", "Loading runtime nodes…")} />
        </div>
      ) : nodes.length === 0 ? (
        <div className="px-5 py-6">
          <EmptyState
            title={text("暂未收到节点心跳", "No node heartbeats received")}
            hint={text(
              "节点启动后会自动出现在这里。",
              "Nodes appear automatically after startup.",
            )}
          />
        </div>
      ) : (
        <ConsoleTable<RuntimeNode>
          rowKey={(node) => node.sessionId}
          dataSource={nodes}
          columns={nodeColumns}
          pagination={false}
          scroll={{ x: 1260, y: 260 }}
        />
      )}
    </Card>
  );
}
