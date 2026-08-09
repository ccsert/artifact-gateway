import { useCallback, useEffect, useRef, useState } from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listRuntimeNodes } from "../client";
import type { RuntimeNode, RuntimeNodeList } from "../client";
import { formatDate } from "../lib/format";
import { FormatBadge, StateBadge } from "./Badge";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";
import { usePreferences } from "../lib/preferences";

function runtimeNodeColumns(
  locale: string,
  text: (chinese: string, english: string) => string,
): ColumnsType<RuntimeNode> {
  return [
    {
      title: text("实例", "Instance"),
      dataIndex: "instanceId",
      key: "instanceId",
      width: 190,
      render: (value: string, node) => (
        <div className="min-w-0">
          <div className="font-mono text-xs text-zinc-200">{value}</div>
          <div
            className="truncate text-[11px] text-zinc-600"
            title={node.sessionId}
          >
            {text("会话", "Session")} {node.sessionId.slice(0, 12)}…
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
        <span className="text-xs text-zinc-400">{roles.join(" · ")}</span>
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
            <span className="text-xs text-zinc-600">
              {text("无格式 Worker", "No format worker")}
            </span>
          )}
          {node.workerKinds.length > 0 && (
            <span className="ml-1 text-[11px] text-zinc-500">
              {node.workerKinds.join(" · ")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: text("最近心跳", "Last heartbeat"),
      dataIndex: "lastSeenAt",
      key: "lastSeenAt",
      width: 190,
      render: (value: string, node) =>
        node.stoppedAt ? (
          <span className="whitespace-nowrap text-xs text-zinc-500">
            {text("已退出", "Stopped")} {formatDate(node.stoppedAt, locale)}
          </span>
        ) : (
          <span className="whitespace-nowrap text-xs text-zinc-500">
            {formatDate(value, locale)}
          </span>
        ),
    },
  ];
}

// Older persisted node rows may contain NULL for fields introduced after the
// initial runtime inventory schema. Normalize those responses before they
// reach table renderers so a single legacy row cannot take down Operations.
function normalizeRuntimeNode(node: RuntimeNode): RuntimeNode {
  const raw = node as RuntimeNode & {
    instanceId?: string | null;
    sessionId?: string | null;
    roles?: string[] | null;
    workerFormats?: string[] | null;
    workerKinds?: string[] | null;
  };
  return {
    ...node,
    instanceId: raw.instanceId ?? "unknown",
    sessionId: raw.sessionId ?? raw.instanceId ?? "unknown",
    roles: raw.roles ?? [],
    workerFormats: raw.workerFormats ?? [],
    workerKinds: raw.workerKinds ?? [],
  };
}

export function RuntimeNodesPanel({
  pollIntervalMs = 15_000,
}: {
  pollIntervalMs?: number;
}) {
  const { locale, text } = usePreferences();
  const nodeColumns = runtimeNodeColumns(locale, text);
  const [nodes, setNodes] = useState<RuntimeNode[] | null>(null);
  const [health, setHealth] = useState<RuntimeNodeList["health"] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [refreshing, setRefreshing] = useState(false);
  const mounted = useRef(false);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    if (mounted.current) setRefreshing(true);
    try {
      const result = await listRuntimeNodes();
      if (!mounted.current) return;
      if (result.error) {
        setError(result.error);
        return;
      }
      setError(null);
      setNodes((result.data?.items ?? []).map(normalizeRuntimeNode));
      setHealth(
        result.data?.health
          ? {
              ...result.data.health,
              issues: result.data.health.issues ?? [],
            }
          : null,
      );
    } catch (nextError) {
      if (mounted.current) setError(nextError);
    } finally {
      inFlight.current = false;
      if (mounted.current) setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    void load();
    const timer = window.setInterval(() => void load(), pollIntervalMs);
    return () => {
      mounted.current = false;
      window.clearInterval(timer);
    };
  }, [load, pollIntervalMs]);

  return (
    <Card className="mt-4">
      <CardHeader
        title={text("运行节点", "Runtime nodes")}
        extra={
          <div className="flex items-center gap-2">
            {health && <StateBadge state={health.status} />}
            <span className="text-xs text-zinc-500">
              {nodes
                ? text(`${nodes.length} 个实例`, `${nodes.length} instances`)
                : text("加载中", "Loading")}
            </span>
            <Tooltip title={text("刷新运行节点", "Refresh runtime nodes")}>
              <Button
                aria-label={text("刷新运行节点", "Refresh runtime nodes")}
                type="text"
                size="small"
                icon={<ReloadOutlined />}
                loading={refreshing}
                onClick={() => void load()}
              />
            </Tooltip>
          </div>
        }
      />
      {health && (health.issues ?? []).length > 0 && (
        <Alert
          className="ag-feedback-enter rounded-none border-x-0 border-t-0"
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
                  <span className="font-mono text-[11px] text-zinc-500">
                    {issue.code}
                  </span>
                  <span className="ml-2">{issue.message}</span>
                </div>
              ))}
            </div>
          }
        />
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
        <Table<RuntimeNode>
          className="ag-console-table"
          rowKey={(node) => node.sessionId}
          size="small"
          dataSource={nodes}
          columns={nodeColumns}
          pagination={false}
          scroll={{ x: 960, y: 260 }}
        />
      )}
    </Card>
  );
}
