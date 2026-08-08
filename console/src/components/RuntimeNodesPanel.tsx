import { useCallback, useEffect, useRef, useState } from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { Button, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listRuntimeNodes } from "../client";
import type { RuntimeNode } from "../client";
import { formatDate } from "../lib/format";
import { FormatBadge, StateBadge } from "./Badge";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";

const nodeColumns: ColumnsType<RuntimeNode> = [
  {
    title: "实例",
    dataIndex: "instanceId",
    key: "instanceId",
    width: 190,
    render: (value: string) => (
      <span className="font-mono text-xs text-zinc-200">{value}</span>
    ),
  },
  {
    title: "状态",
    dataIndex: "status",
    key: "status",
    width: 100,
    render: (value: RuntimeNode["status"]) => <StateBadge state={value} />,
  },
  {
    title: "角色",
    dataIndex: "roles",
    key: "roles",
    width: 150,
    render: (roles: string[]) => (
      <span className="text-xs text-zinc-400">{roles.join(" · ")}</span>
    ),
  },
  {
    title: "Worker 能力",
    key: "capabilities",
    width: 330,
    render: (_, node) => (
      <div className="flex flex-wrap items-center gap-1.5">
        {node.workerFormats.length > 0 ? (
          node.workerFormats.map((format) => (
            <FormatBadge key={format} format={format} />
          ))
        ) : (
          <span className="text-xs text-zinc-600">无格式 Worker</span>
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
    title: "最近心跳",
    dataIndex: "lastSeenAt",
    key: "lastSeenAt",
    width: 190,
    render: (value: string) => (
      <span className="whitespace-nowrap text-xs text-zinc-500">
        {formatDate(value)}
      </span>
    ),
  },
];

export function RuntimeNodesPanel({
  pollIntervalMs = 15_000,
}: {
  pollIntervalMs?: number;
}) {
  const [nodes, setNodes] = useState<RuntimeNode[] | null>(null);
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
      setNodes(result.data?.items ?? []);
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
        title="运行节点"
        extra={
          <div className="flex items-center gap-2">
            <span className="text-xs text-zinc-500">
              {nodes ? `${nodes.length} 个实例` : "加载中"}
            </span>
            <Tooltip title="刷新运行节点">
              <Button
                aria-label="刷新运行节点"
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
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : nodes === null ? (
        <div className="px-5 py-6">
          <Loading label="加载节点清单…" />
        </div>
      ) : nodes.length === 0 ? (
        <div className="px-5 py-6">
          <EmptyState
            title="暂未收到节点心跳"
            hint="节点启动后会自动出现在这里。"
          />
        </div>
      ) : (
        <Table<RuntimeNode>
          className="ag-console-table"
          rowKey={(node) => node.instanceId}
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
