import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Input, Popconfirm, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createRepositoryPromotion,
  createRepositoryReplication,
  deleteRepositoryReplication,
  getRepositoryReplication,
  listRepositories,
  listRepositoryReplications,
} from "../../client";
import type {
  ReplicationPlan,
  ReplicationPlanDetail,
  Repository,
} from "../../client";
import { StateBadge } from "../../components/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/Feedback";
import { Field } from "../../components/Layout";
import { Modal } from "../../components/Modal";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

export function RepositoryDistributionTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [repos, setRepos] = useState<Repository[]>([]);
  const [targetId, setTargetId] = useState("");
  const [coordinate, setCoordinate] = useState("");
  const [digest, setDigest] = useState("");
  const [plans, setPlans] = useState<ReplicationPlan[] | null>(null);
  const [detail, setDetail] = useState<ReplicationPlanDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<"promote" | "replicate" | null>(null);

  const targets = repos.filter(
    (r) => r.id !== repo.id && r.format === repo.format && r.state === "active",
  );
  const coordinatePlaceholder: Record<string, string> = {
    maven: "org.example:gateway-widget:1.2.3",
    oci: "library/nginx:1.27",
    conan: "zlib/1.3.1@company/stable",
    raw: "releases/gateway-widget-1.2.3.zip",
    npm: "@company/gateway-widget@1.2.3",
    pypi: "gateway-widget@1.2.3",
  };

  const load = useCallback(async () => {
    setError(null);
    const [allRepos, p] = await Promise.all([
      listRepositories({ query: { pageSize: 200 } }),
      listRepositoryReplications({ path: { repositoryId: repo.id } }),
    ]);
    setRepos(allRepos.data?.items ?? []);
    if (p.error) {
      if (!isNotFound(p.error)) setError(p.error);
      setPlans([]);
      return;
    }
    setPlans(p.data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const cancelPlan = async (planId: string) => {
    setActionError(null);
    const { error: err } = await deleteRepositoryReplication({
      path: { repositoryId: repo.id, replicationPlanId: planId },
    });
    if (err) {
      setActionError(err);
      return;
    }
    setNotice(
      text(
        "已取消复制计划，工作进程不再重试。",
        "Replication plan canceled. Workers will not retry it.",
      ),
    );
    void load();
  };

  const submit = async (kind: "promote" | "replicate") => {
    setBusy(kind);
    setActionError(null);
    setNotice("");
    const body = {
      targetRepositoryId: targetId,
      coordinate: coordinate.trim(),
      digest: digest.trim(),
    };
    const headers = { "Idempotency-Key": crypto.randomUUID() };
    const { error: err } =
      kind === "promote"
        ? await createRepositoryPromotion({
            path: { repositoryId: repo.id },
            body,
            headers,
          })
        : await createRepositoryReplication({
            path: { repositoryId: repo.id },
            body,
            headers,
          });
    setBusy(null);
    if (err) {
      setActionError(err);
      return;
    }
    setNotice(
      kind === "promote"
        ? text(
            "晋升任务已提交，请在「生命周期任务」查看进度",
            "Promotion task submitted. Track it on the Lifecycle jobs tab.",
          )
        : text(
            "复制计划已创建，下方查看进度",
            "Replication plan created. Track its progress below.",
          ),
    );
    setCoordinate("");
    setDigest("");
    void load();
  };

  const showDetail = async (planId: string) => {
    const { data, error: err } = await getRepositoryReplication({
      path: { repositoryId: repo.id, replicationPlanId: planId },
    });
    if (err) {
      setActionError(err);
      return;
    }
    setDetail(data ?? null);
  };

  const repoName = (id: string) =>
    repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + "…";

  if (error !== null) return <ErrorBanner error={error} onRetry={load} />;

  const planColumns: ColumnsType<ReplicationPlan> = [
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
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
        <span className="text-xs text-zinc-300">{repoName(value)}</span>
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
        <span className="whitespace-nowrap text-xs text-zinc-500">
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
        <span className="whitespace-nowrap text-xs text-zinc-500">
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
  const checkpointColumns: ColumnsType<
    ReplicationPlanDetail["checkpoints"][number]
  > = [
    {
      title: text("对象", "Object"),
      dataIndex: "objectKey",
      key: "objectKey",
      width: 280,
      render: (value: string) => (
        <span
          className="block max-w-64 truncate font-mono text-xs text-zinc-300"
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
        <span className="font-mono text-xs text-zinc-500">
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
        <span className="text-xs text-zinc-400">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("进度", "Progress"),
      key: "progress",
      width: 110,
      render: (_, checkpoint) => (
        <span className="text-xs text-zinc-400">
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
        <span className="text-xs text-zinc-500">{value}</span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {actionError !== null && <ErrorBanner error={actionError} />}
      {notice && <Alert type="success" showIcon title={notice} />}

      {/* 发起表单 */}
      <div className="rounded-lg border border-zinc-800 p-4">
        <div className="mb-3 text-sm font-medium text-zinc-200">
          {text("发起晋升 / 复制", "Start promotion / replication")}
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
          <Field label={text("目标仓库", "Target repository")}>
            <Select
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder={text(
                "选择同格式仓库…",
                "Select a repository with the same format…",
              )}
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              onChange={setTargetId}
            />
          </Field>
          <Field
            label={text("制品坐标", "Artifact coordinate")}
            hint={text(
              `${repo.format} 生命周期使用的不可变版本标识`,
              `Immutable version identity used by ${repo.format} lifecycle operations`,
            )}
          >
            <Input
              className="font-mono"
              placeholder={coordinatePlaceholder[repo.format] ?? "coordinate"}
              value={coordinate}
              onChange={(e) => setCoordinate(e.target.value)}
            />
          </Field>
          <Field label={text("摘要 digest", "Digest")}>
            <Input
              className="font-mono"
              placeholder="sha256:…"
              value={digest}
              onChange={(e) => setDigest(e.target.value)}
            />
          </Field>
          <div className="flex items-end gap-2">
            <Button
              type="primary"
              loading={busy === "promote"}
              onClick={() => submit("promote")}
              disabled={
                busy !== null ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("晋升", "Promote")}
            </Button>
            <Button
              loading={busy === "replicate"}
              onClick={() => submit("replicate")}
              disabled={
                busy !== null ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("复制", "Replicate")}
            </Button>
          </div>
        </div>
        <p className="mt-2 text-xs text-zinc-600">
          {text(
            "晋升：在目标仓库创建同一制品的可见副本（审计追踪）；复制：异步、带断点地拷贝制品字节到目标仓库。",
            "Promotion creates a visible copy of the artifact in the target repository with an audit trail. Replication copies artifact bytes asynchronously with checkpoints.",
          )}
        </p>
      </div>

      {/* 复制计划列表 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">
          {text(
            `复制计划（${plans?.length ?? 0}）`,
            `Replication plans (${plans?.length ?? 0})`,
          )}
        </div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title={text("暂无复制计划", "No replication plans")} />
        ) : (
          <Table<ReplicationPlan>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={plans}
            columns={planColumns}
            pagination={false}
            scroll={{ x: 1040 }}
          />
        )}
      </div>

      {/* 复制进度详情 */}
      <Modal
        open={!!detail}
        title={text("复制进度详情", "Replication progress")}
        onClose={() => setDetail(null)}
        wide
      >
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-zinc-400">
              <span>
                {text("状态：", "Status: ")}
                <StateBadge state={detail.state} />
              </span>
              <span>
                {text("目标：", "Target: ")}
                {repoName(detail.targetRepositoryId)}
              </span>
              <span>
                {text("创建：", "Created: ")}
                {formatDate(detail.createdAt)}
              </span>
              {detail.lastError && (
                <span className="text-rose-400">{detail.lastError}</span>
              )}
            </div>
            {detail.checkpoints.length === 0 ? (
              <p className="py-4 text-center text-sm text-zinc-500">
                {text("暂无检查点", "No checkpoints")}
              </p>
            ) : (
              <Table<ReplicationPlanDetail["checkpoints"][number]>
                className="ag-console-table"
                rowKey={(checkpoint, index) =>
                  `${checkpoint.objectKey}-${index}`
                }
                size="small"
                dataSource={detail.checkpoints}
                columns={checkpointColumns}
                pagination={false}
                scroll={{ x: 900 }}
              />
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
