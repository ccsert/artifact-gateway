import type { Translate } from "../../lib/preferences";
import { Button, Popconfirm, Space } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ReplicationPlan, ReplicationPlanDetail } from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";

/** Replication plans, with cancel and detail actions. */
export function replicationPlanColumns({
  suiteTarget,
  repoName,
  cancelPlan,
  showDetail,
  t,
}: {
  suiteTarget: boolean;
  repoName: (repositoryId: string) => string;
  cancelPlan: (planId: string) => unknown;
  showDetail: (planId: string) => unknown;
  t: Translate;
}): ColumnsType<ReplicationPlan> {
  return [
    ...(suiteTarget
      ? [
          {
            title: t("repository.targetSuite2"),
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
      title: t("repository.targetRepository"),
      dataIndex: "targetRepositoryId",
      key: "targetRepositoryId",
      width: 220,
      render: (value: string) => (
        <span className="text-xs text-fg-secondary">{repoName(value)}</span>
      ),
    },
    {
      title: t("repository.status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: t("repository.created"),
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
      title: t("repository.completed"),
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
      title: t("repository.actions"),
      key: "actions",
      fixed: "right",
      width: 180,
      render: (_, plan) => (
        <Space size="small">
          <Button size="small" onClick={() => showDetail(plan.id)}>
            {t("repository.progress")}
          </Button>
          {(plan.state === "pending" || plan.state === "failed") && (
            <Popconfirm
              title={t("repository.cancelReplicationPlan")}
              description={t(
                "repository.workersWillNotRetryAfterCancellationBytesAlready",
              )}
              okText={t("repository.cancelPlan")}
              cancelText={t("repository.back")}
              okButtonProps={{ danger: true }}
              onConfirm={() => cancelPlan(plan.id)}
            >
              <Button size="small" danger>
                {t("repository.cancel")}
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];
}

/** One plan's per-artifact checkpoints. */
export function replicationCheckpointColumns(t: Translate) {
  const columns: ColumnsType<ReplicationPlanDetail["checkpoints"][number]> = [
    {
      title: t("repository.object"),
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
      title: t("repository.digest"),
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
      title: t("repository.size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: t("repository.progress"),
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
      title: t("repository.status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: t("repository.attempts"),
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
