import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Input, Popconfirm, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createRepositoryPromotion,
  createRepositoryReplication,
  deleteRepositoryReplication,
  evaluateSecurityPolicy,
  getRepositoryReplication,
  listRepositories,
  listRepositoryReplications,
} from "../../client";
import type {
  ReplicationPlan,
  ReplicationPlanDetail,
  Repository,
  SecurityPolicyEvaluation,
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
import {
  RepositoryArtifactSelect,
  type RepositoryArtifactIdentity,
} from "../../components/RepositoryArtifactSelect";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

function securityReason(
  reason: string,
  text: (zh: string, en: string) => string,
) {
  const labels: Record<string, [string, string]> = {
    artifact_quarantined: [
      "制品已隔离；请先解除隔离",
      "Artifact is quarantined; release it first",
    ],
    policy_disabled: ["策略未启用", "Policy is disabled"],
    signature_required: ["缺少签名", "Signature is required"],
    verified_signature_required: [
      "缺少已验证签名",
      "A verified signature is required",
    ],
    sbom_required: ["缺少 SBOM", "An SBOM is required"],
    provenance_required: ["缺少 provenance", "Provenance is required"],
    vulnerability_scan_required: [
      "缺少漏洞扫描结果",
      "A vulnerability scan is required",
    ],
    license_required: ["缺少许可证信息", "License information is required"],
    license_not_allowed: [
      "许可证不在白名单中",
      "A license is not allow-listed",
    ],
    vulnerability_scan_error: ["漏洞扫描失败", "The vulnerability scan failed"],
    low_vulnerabilities: ["存在低危漏洞", "Low vulnerabilities found"],
    medium_vulnerabilities: ["存在中危漏洞", "Medium vulnerabilities found"],
    high_vulnerabilities: ["存在高危漏洞", "High vulnerabilities found"],
    critical_vulnerabilities: [
      "存在严重漏洞",
      "Critical vulnerabilities found",
    ],
    unknown_vulnerabilities: [
      "存在未知等级漏洞",
      "Unknown-severity vulnerabilities found",
    ],
  };
  const label = labels[reason];
  return label ? text(label[0], label[1]) : reason;
}

export function RepositoryDistributionTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [repos, setRepos] = useState<Repository[]>([]);
  const [targetId, setTargetId] = useState("");
  const [coordinate, setCoordinate] = useState("");
  const [digest, setDigest] = useState("");
  const [selectedArtifact, setSelectedArtifact] =
    useState<RepositoryArtifactIdentity | null>(null);
  const [manualIdentity, setManualIdentity] = useState(false);
  const [plans, setPlans] = useState<ReplicationPlan[] | null>(null);
  const [detail, setDetail] = useState<ReplicationPlanDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<"promote" | "replicate" | null>(null);
  const [evaluation, setEvaluation] = useState<SecurityPolicyEvaluation | null>(
    null,
  );
  const [evaluating, setEvaluating] = useState(false);

  const targets = repos.filter(
    (r) =>
      r.id !== repo.id &&
      r.type === "hosted" &&
      r.format === repo.format &&
      r.state === "active",
  );
  const coordinatePlaceholder: Record<string, string> = {
    maven: "org.example:gateway-widget:1.2.3",
    oci: "library/nginx",
    conan: "zlib/1.3.1@company/stable#recipe-revision",
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
    setSelectedArtifact(null);
    setManualIdentity(false);
    setEvaluation(null);
    void load();
  };

  const evaluate = async () => {
    if (!targetId || !coordinate.trim() || !digest.trim()) return;
    setEvaluating(true);
    setActionError(null);
    const { data, error: err } = await evaluateSecurityPolicy({
      path: { repositoryId: targetId },
      body: {
        sourceRepositoryId: repo.id,
        coordinate: coordinate.trim(),
        digest: digest.trim(),
      },
    });
    setEvaluating(false);
    if (err) {
      setActionError(err);
      setEvaluation(null);
      return;
    }
    setEvaluation(data ?? null);
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
  const promotionBlocked = evaluation?.allowed === false;
  const artifactQuarantined =
    evaluation?.reasons.includes("artifact_quarantined") ?? false;

  const chooseArtifact = (artifact: RepositoryArtifactIdentity | null) => {
    setSelectedArtifact(artifact);
    setManualIdentity(false);
    setEvaluation(null);
    setCoordinate(artifact?.coordinate ?? "");
    setDigest(artifact?.digest ?? "");
  };

  const toggleManualIdentity = () => {
    setManualIdentity((current) => {
      const next = !current;
      if (!next && !selectedArtifact) {
        setCoordinate("");
        setDigest("");
      }
      if (!next && selectedArtifact) {
        setCoordinate(selectedArtifact.coordinate);
        setDigest(selectedArtifact.digest);
      }
      setEvaluation(null);
      return next;
    });
  };

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
        <div className="mb-4">
          <div className="text-sm font-medium text-zinc-200">
            {text("发起晋升 / 复制", "Start promotion / replication")}
          </div>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-zinc-500">
            {text(
              "先从当前仓库选择一个不可变制品，再选择同格式的目标 Hosted 仓库。坐标和摘要会自动锁定。",
              "Select an immutable artifact from this repository, then choose a target Hosted repository with the same format. Its coordinate and digest are locked automatically.",
            )}
          </p>
        </div>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(280px,1.6fr)_minmax(220px,1fr)]">
          <Field
            group
            label={text("源制品", "Source artifact")}
            hint={text(
              "最多显示 50 条；输入包名、路径或坐标前缀可缩小范围。",
              "Up to 50 items are shown. Type a package, path, or coordinate prefix to narrow the results.",
            )}
          >
            <RepositoryArtifactSelect
              repo={repo}
              purpose="distribution"
              value={selectedArtifact}
              onChange={chooseArtifact}
              disabled={busy !== null}
              ariaLabel={text(
                "搜索并选择源制品",
                "Search and select a source artifact",
              )}
            />
          </Field>
          <Field
            group
            label={text("目标 Hosted 仓库", "Target Hosted repository")}
          >
            <Select
              aria-label={text("选择目标仓库", "Select target repository")}
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder={text(
                "选择同格式仓库…",
                "Select a repository with the same format…",
              )}
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              notFoundContent={text(
                "没有可用的同格式 Hosted 仓库",
                "No compatible Hosted repository is available",
              )}
              disabled={busy !== null}
              onChange={(value) => {
                setTargetId(value);
                setEvaluation(null);
              }}
            />
          </Field>
        </div>

        <div className="mt-2 flex justify-end">
          <Button
            type="link"
            size="small"
            className="h-auto px-0 py-0 text-xs"
            onClick={toggleManualIdentity}
          >
            {manualIdentity
              ? text("收起高级手动输入", "Hide advanced manual input")
              : text("高级手动输入", "Advanced manual input")}
          </Button>
        </div>

        {selectedArtifact && !manualIdentity && (
          <div className="mt-3 rounded-md border border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] px-3 py-2.5">
            <p className="truncate font-mono text-xs text-zinc-200">
              {selectedArtifact.coordinate}
            </p>
            <p className="mt-0.5 break-all font-mono text-xs text-zinc-500">
              {selectedArtifact.digest}
            </p>
          </div>
        )}

        {manualIdentity && (
          <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field
              label={text("制品坐标", "Artifact coordinate")}
              hint={text(
                `${repo.format} 生命周期使用的不可变版本标识`,
                `Immutable version identity used by ${repo.format} lifecycle operations`,
              )}
            >
              <Input
                aria-label={text("制品坐标", "Artifact coordinate")}
                className="font-mono"
                placeholder={coordinatePlaceholder[repo.format] ?? "coordinate"}
                value={coordinate}
                onChange={(event) => {
                  setSelectedArtifact(null);
                  setCoordinate(event.target.value);
                  setEvaluation(null);
                }}
              />
            </Field>
            <Field label={text("摘要 digest", "Digest")}>
              <Input
                aria-label={text("摘要 digest", "Digest")}
                className="font-mono"
                placeholder="sha256:…"
                value={digest}
                onChange={(event) => {
                  setSelectedArtifact(null);
                  setDigest(event.target.value);
                  setEvaluation(null);
                }}
              />
            </Field>
          </div>
        )}

        <div className="mt-4 grid gap-3 border-t border-zinc-800/70 pt-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <p className="max-w-3xl text-xs leading-5 text-zinc-500">
            {text(
              "晋升用于环境或成熟度流转：目标仓安全准入通过后发布可见副本；复制用于镜像或分发：创建可续传计划，但不代表通过普通安全策略。",
              "Promotion advances an artifact between environments or maturity stages and publishes a visible copy after target admission. Replication creates a resumable mirror or distribution plan and does not represent ordinary security-policy approval.",
            )}
          </p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              loading={evaluating}
              onClick={() => void evaluate()}
              disabled={
                busy !== null ||
                evaluating ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("评估准入", "Evaluate admission")}
            </Button>
            <Button
              type="primary"
              loading={busy === "promote"}
              onClick={() => submit("promote")}
              disabled={
                busy !== null ||
                promotionBlocked ||
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
                artifactQuarantined ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("复制", "Replicate")}
            </Button>
          </div>
        </div>
        {evaluation && (
          <Alert
            className="mt-4"
            type={evaluation.allowed ? "success" : "error"}
            showIcon
            title={text(
              artifactQuarantined
                ? "制品已隔离，无法晋升或复制"
                : evaluation.allowed
                  ? "安全策略允许晋升"
                  : "安全策略阻止晋升",
              artifactQuarantined
                ? "Artifact is quarantined and cannot be promoted or replicated"
                : evaluation.allowed
                  ? "Security policy allows promotion"
                  : "Security policy blocks promotion",
            )}
            description={
              <div className="space-y-2 text-xs">
                {artifactQuarantined && (
                  <p>
                    {text(
                      "请先在制品详情中解除隔离，然后重新评估准入。",
                      "Release the artifact from its details, then evaluate admission again.",
                    )}
                  </p>
                )}
                <div className="flex flex-wrap gap-x-5 gap-y-1 text-zinc-500">
                  <span>
                    {text("策略版本", "Policy version")}:{" "}
                    {evaluation.policyVersion}
                  </span>
                  <span>
                    {text("策略状态", "Enforcement")}:{" "}
                    {evaluation.enforced
                      ? text("已启用", "enabled")
                      : text("未启用", "disabled")}
                  </span>
                  <span>
                    {text("制品情报", "Artifact intelligence")}:{" "}
                    {evaluation.intelligencePresent
                      ? text("已找到", "found")
                      : text("未找到", "not found")}
                  </span>
                </div>
                {evaluation.reasons.length > 0 && (
                  <div className="flex flex-wrap gap-2">
                    {evaluation.reasons.map((reason) => (
                      <span
                        className="rounded border border-current/20 px-2 py-1 font-mono"
                        key={reason}
                      >
                        {securityReason(reason, text)}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            }
          />
        )}
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
                <span className="text-[var(--ag-status-danger)]">
                  {detail.lastError}
                </span>
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
