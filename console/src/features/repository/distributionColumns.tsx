import { Button, Popconfirm, Space } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ReplicationPlan, ReplicationPlanDetail } from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";

type Localize = (chinese: string, english: string) => string;

/** Replication plans, with cancel and detail actions. */
export function replicationPlanColumns({
  suiteTarget,
  repoName,
  cancelPlan,
  showDetail,
  text,
}: {
  suiteTarget: boolean;
  repoName: (repositoryId: string) => string;
  cancelPlan: (planId: string) => unknown;
  showDetail: (planId: string) => unknown;
  text: Localize;
}): ColumnsType<ReplicationPlan> {
  return [
    ...(suiteTarget
      ? [
          {
            title: text("目标套件", "Target suite"),
            dataIndex: "aptTargetSuite",
            key: "aptTargetSuite",
            width: 140,
          },
        ]
      : []),
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-tertiary" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: text("目标仓库", "Target repository"),
      dataIndex: "targetRepositoryId",
      key: "targetRepositoryId",
      width: 220,
      render: (value: string) => (
        <span className="text-xs text-fg-secondary">{repoName(value)}</span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("完成时间", "Completed"),
      dataIndex: "completedAt",
      key: "completedAt",
      width: 180,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 180,
      render: (_, plan) => (
        <Space size="small">
          <Button size="small" onClick={() => showDetail(plan.id)}>
            {text("进度", "Progress")}
          </Button>
          {(plan.state === "pending" || plan.state === "failed") && (
            <Popconfirm
              title={text("确认取消复制计划？", "Cancel replication plan?")}
              description={text(
                "取消后工作进程将不再重试，已复制的字节不会自动删除。",
                "Workers will not retry after cancellation. Bytes already copied are not deleted automatically.",
              )}
              okText={text("确认取消", "Cancel plan")}
              cancelText={text("返回", "Back")}
              okButtonProps={{ danger: true }}
              onConfirm={() => cancelPlan(plan.id)}
            >
              <Button size="small" danger>
                {text("取消", "Cancel")}
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];
}

/** One plan's per-artifact checkpoints. */
export function replicationCheckpointColumns(text: Localize) {
  const columns: ColumnsType<ReplicationPlanDetail["checkpoints"][number]> = [
    {
      title: text("对象", "Object"),
      dataIndex: "objectKey",
      key: "objectKey",
      width: 280,
      render: (value: string) => (
        <span
          className="block max-w-64 truncate font-mono text-xs text-fg-secondary"
          title={value}
        >
          {value}
        </span>
      ),
    },
    {
      title: text("摘要", "Digest"),
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("大小", "Size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("进度", "Progress"),
      key: "progress",
      width: 110,
      render: (_, checkpoint) => (
        <span className="text-xs text-fg-secondary">
          {checkpoint.size > 0
            ? `${Math.round((checkpoint.byteOffset / checkpoint.size) * 100)}%`
            : "—"}
        </span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("重试", "Attempts"),
      dataIndex: "attempts",
      key: "attempts",
      width: 90,
      render: (value: number) => (
        <span className="text-xs text-fg-tertiary">{value}</span>
      ),
    },
  ];
  return columns;
}
