import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CheckCircleOutlined,
  ScanOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { Alert, Button, Form, Input, Space, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createRepositoryArtifactScan,
  listRepositoryLifecycleJobs,
  reconcileRepositoryArtifactScans,
} from "../../client";
import type {
  LifecycleJob,
  Repository,
  RepositoryCapabilities,
} from "../../client";
import { StateBadge } from "../../components/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/Feedback";
import { Card, CardHeader } from "../../components/Layout";
import {
  RepositoryArtifactSelect,
  type RepositoryArtifactIdentity,
} from "../../components/RepositoryArtifactSelect";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

type ScanForm = {
  coordinate: string;
  digest: string;
};

function scanStatus(
  option: RepositoryArtifactIdentity,
  jobs: LifecycleJob[] | null,
  text: (zh: string, en: string) => string,
): { color?: string; label: string } {
  const job = jobs?.find(
    (candidate) =>
      candidate.details?.coordinate === option.coordinate &&
      candidate.details.digest === option.digest,
  );
  switch (job?.state) {
    case "completed":
      return { color: "success", label: text("扫描完成", "Scan completed") };
    case "pending":
    case "running":
    case "retrying":
      return { color: "processing", label: text("扫描中", "Scanning") };
    case "failed":
    case "cancelled":
      return { color: "error", label: text("扫描失败", "Scan failed") };
  }
  switch (option.intelligence?.vulnerabilityStatus) {
    case "clean":
      return { color: "success", label: text("已扫描", "Scanned") };
    case "affected":
      return { color: "warning", label: text("发现漏洞", "Affected") };
    case "error":
      return { color: "error", label: text("扫描异常", "Scan error") };
  }
  if (
    (option.intelligence?.sbomCount ?? 0) > 0 ||
    (option.intelligence?.licenseCount ?? 0) > 0
  ) {
    return { color: "processing", label: text("已有情报", "Has evidence") };
  }
  return { label: text("未扫描", "Not scanned") };
}

function coordinatePlaceholder(format: Repository["format"]): string {
  switch (format) {
    case "maven":
      return "com.example:widget:1.2.3";
    case "oci":
      return "team/widget";
    case "npm":
      return "@team/widget@1.2.3";
    case "pypi":
      return "widget-core@1.2.3";
    case "conan":
      return "widget/1.2.3@team/stable#recipe-revision";
    case "go":
      return "example.com/team/widget@v1.2.3";
    case "raw":
      return "releases/widget-1.2.3.tar.gz";
    default:
      return "制品的规范坐标";
  }
}

function manualScanIdempotencyKey(repositoryId: string): string {
  const suffix =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `manual-scan:${repositoryId}:${suffix}`;
}

export function RepositoryScanningTab({
  repo,
  capabilities,
  capabilitiesLoading,
  capabilitiesError,
  canManage,
  canViewJobs,
}: {
  repo: Repository;
  capabilities: RepositoryCapabilities | null;
  capabilitiesLoading: boolean;
  capabilitiesError: unknown;
  canManage: boolean;
  canViewJobs: boolean;
}) {
  const { text } = usePreferences();
  const [form] = Form.useForm<ScanForm>();
  const [jobs, setJobs] = useState<LifecycleJob[] | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [submitError, setSubmitError] = useState<unknown>(null);
  const [submittedJob, setSubmittedJob] = useState<LifecycleJob | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [reconcileError, setReconcileError] = useState<unknown>(null);
  const [reconcileNotice, setReconcileNotice] = useState("");
  const [reconciling, setReconciling] = useState(false);
  const [selectedArtifact, setSelectedArtifact] =
    useState<RepositoryArtifactIdentity | null>(null);
  const [manualIdentity, setManualIdentity] = useState(false);

  const artifactScanning = capabilities?.artifactScanning === true;
  const publicationScanning = capabilities?.publicationScanning === true;

  const load = useCallback(async () => {
    if (!canViewJobs) {
      setJobs([]);
      return;
    }
    setLoadError(null);
    const { data, error } = await listRepositoryLifecycleJobs({
      path: { repositoryId: repo.id },
    });
    if (error) {
      setLoadError(error);
      return;
    }
    setJobs((data ?? []).filter((job) => job.kind === "scan"));
  }, [canViewJobs, repo.id]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 10000);
    return () => clearInterval(timer);
  }, [load]);

  const submitScan = async (values: ScanForm) => {
    setSubmitting(true);
    setSubmitError(null);
    setSubmittedJob(null);
    const { data, error } = await createRepositoryArtifactScan({
      path: { repositoryId: repo.id },
      headers: { "Idempotency-Key": manualScanIdempotencyKey(repo.id) },
      body: {
        coordinate: values.coordinate.trim(),
        digest: values.digest.trim(),
      },
    });
    setSubmitting(false);
    if (error) {
      setSubmitError(error);
      return;
    }
    if (!data) return;
    setSubmittedJob(data);
    setJobs((current) => [
      data,
      ...(current ?? []).filter((job) => job.id !== data.id),
    ]);
    form.resetFields();
    setSelectedArtifact(null);
    setManualIdentity(false);
  };

  const reconcileScans = async () => {
    setReconciling(true);
    setReconcileError(null);
    setReconcileNotice("");
    const { data, error } = await reconcileRepositoryArtifactScans({
      path: { repositoryId: repo.id },
      query: { limit: 500 },
    });
    setReconciling(false);
    if (error) {
      setReconcileError(error);
      return;
    }
    setReconcileNotice(
      text(
        `已检查 ${data?.inspected ?? 0} 个制品，补入 ${data?.enqueued ?? 0} 个，重试 ${data?.retried ?? 0} 个`,
        `Inspected ${data?.inspected ?? 0} artifacts, queued ${data?.enqueued ?? 0}, retried ${data?.retried ?? 0}`,
      ),
    );
    void load();
  };

  const columns = useMemo<ColumnsType<LifecycleJob>>(
    () => [
      {
        title: text("制品", "Artifact"),
        key: "artifact",
        width: 320,
        render: (_, job) => (
          <div className="space-y-0.5">
            <div
              className="truncate font-mono text-xs text-zinc-200"
              title={job.details?.coordinate}
            >
              {job.details?.coordinate ?? text("未知制品", "Unknown artifact")}
            </div>
            <div className="font-mono text-[11px] text-zinc-600">
              {job.details ? shortDigest(job.details.digest) : job.id}
            </div>
            {job.details && (
              <div className="font-mono text-[11px] text-zinc-700">
                {job.id}
              </div>
            )}
          </div>
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
        title: text("进度", "Progress"),
        key: "progress",
        width: 170,
        render: (_, job) => (
          <span className="text-xs text-zinc-500">
            {job.progressTotal > 0
              ? `${job.progressCurrent} / ${job.progressTotal}`
              : job.progressMessage || "—"}
          </span>
        ),
      },
      {
        title: text("创建时间", "Created"),
        dataIndex: "createdAt",
        key: "createdAt",
        width: 190,
        render: (value: string) => (
          <span className="whitespace-nowrap text-xs text-zinc-500">
            {formatDate(value)}
          </span>
        ),
      },
      {
        title: text("最近错误", "Last error"),
        dataIndex: "lastError",
        key: "lastError",
        render: (value?: string) => (
          <span className="text-xs text-rose-400">{value ?? "—"}</span>
        ),
      },
    ],
    [text],
  );

  const toggleManualIdentity = () => {
    setManualIdentity((current) => {
      const next = !current;
      if (!next) {
        if (selectedArtifact) {
          form.setFieldsValue({
            coordinate: selectedArtifact.coordinate,
            digest: selectedArtifact.digest,
          });
        } else {
          form.resetFields();
        }
      }
      return next;
    });
  };

  if (capabilitiesLoading) {
    return (
      <Loading label={text("检查扫描能力…", "Checking scan capability…")} />
    );
  }
  if (capabilitiesError !== null) {
    return <ErrorBanner error={capabilitiesError} />;
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-800/80 pb-4">
        <div>
          <h2 className="text-base font-semibold text-zinc-100">
            {text("制品扫描", "Artifact scanning")}
          </h2>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-zinc-500">
            {text(
              "扫描不可变制品并将 SBOM、开源许可证和漏洞结果写入制品情报；签名与 Provenance 仍由发布流程提供。",
              "Scan immutable artifacts and write SBOM, open-source license, and vulnerability results into artifact intelligence. Signatures and provenance still come from the publication pipeline.",
            )}
          </p>
        </div>
        <Space wrap>
          <Tag color={artifactScanning ? "success" : "default"}>
            {text("手动扫描", "Manual scan")}:{" "}
            {artifactScanning
              ? text("可用", "Available")
              : text("不可用", "Unavailable")}
          </Tag>
          <Tag color={publicationScanning ? "success" : "default"}>
            {text("发布后自动扫描能力", "Scan-on-publish capability")}:{" "}
            {publicationScanning
              ? text("可用", "Available")
              : text("不可用", "Unavailable")}
          </Tag>
        </Space>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        {!artifactScanning && (
          <Alert
            type="warning"
            showIcon
            title={text(
              "当前仓库未配置可用扫描器",
              "No scanner is configured for this repository",
            )}
            description={
              <span>
                {text(
                  "请由平台管理员在 Gateway 部署环境中配置 ",
                  "Ask a platform administrator to configure ",
                )}
                <code>GATEWAY_SCANNER_ENDPOINT</code>
                {text(" 和 ", " and ")}
                <code>GATEWAY_SCANNER_FORMATS</code>
                {text(
                  "，并确保 Worker 节点启用 scan 任务。Endpoint 与令牌不会在 Console 中暴露。",
                  " in the Gateway deployment and enable scan jobs on worker nodes. The endpoint and token are not exposed in the Console.",
                )}
              </span>
            }
          />
        )}
        {artifactScanning && !canManage && (
          <Alert
            type="info"
            showIcon
            title={text(
              "当前身份没有制品扫描权限",
              "This identity cannot manage artifact scans",
            )}
            description={text(
              "提交扫描和历史对账需要 repositories:intelligence 权限。",
              "Queueing scans and historical reconciliation require repositories:intelligence.",
            )}
          />
        )}
        <Alert
          className={artifactScanning && canManage ? "xl:col-span-2" : ""}
          type="info"
          showIcon
          title={text(
            "扫描与处置是两个步骤",
            "Scanning and enforcement are separate",
          )}
          description={text(
            "扫描与发布后调度都是异步 best-effort：失败不会回滚已成功的上传。扫描结果不会自动隔离制品，也不会直接阻断读取；读取阻断需要管理员隔离制品并启用独立的隔离读取策略。",
            "Scanning and scan-on-publish scheduling are asynchronous and best effort; failures do not roll back a successful upload. Results do not automatically quarantine artifacts or block reads. Read blocking requires an administrator to quarantine the artifact and enable the separate quarantine-read policy.",
          )}
        />
      </div>

      {submittedJob && (
        <Alert
          type="success"
          showIcon
          icon={<CheckCircleOutlined />}
          title={text("扫描任务已提交", "Scan job queued")}
          description={
            <span>
              {text("任务 ID：", "Job ID: ")}
              <code>{submittedJob.id}</code>
            </span>
          }
        />
      )}
      {submitError !== null && <ErrorBanner error={submitError} />}

      <Card>
        <CardHeader
          title={text(
            "选择并扫描不可变制品",
            "Select and scan an immutable artifact",
          )}
        />
        <div className="px-5 py-4">
          <Form<ScanForm>
            form={form}
            layout="vertical"
            className="grid gap-4 md:grid-cols-2 md:gap-x-5"
            onFinish={(values) => void submitScan(values)}
            requiredMark={false}
          >
            <Form.Item
              className="md:col-span-2"
              label={text("搜索并选择制品", "Search and select an artifact")}
              style={{ marginBottom: 0 }}
            >
              <RepositoryArtifactSelect
                repo={repo}
                value={selectedArtifact}
                enabled={artifactScanning && canManage}
                ariaLabel={text(
                  "搜索并选择制品",
                  "Search and select an artifact",
                )}
                statusForOption={(artifact) => scanStatus(artifact, jobs, text)}
                onChange={(artifact) => {
                  setSelectedArtifact(artifact);
                  setManualIdentity(false);
                  if (artifact) {
                    form.setFieldsValue({
                      coordinate: artifact.coordinate,
                      digest: artifact.digest,
                    });
                  } else {
                    form.resetFields();
                  }
                }}
              />
              <div className="mt-1.5 flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
                <span className="text-[11px] leading-5 text-zinc-500">
                  {text(
                    "选择后会自动锁定规范坐标与完整摘要；最多显示 50 条，可输入前缀缩小范围。旧版本或 Conan 修订未列出时请使用高级手动输入。",
                    "Selection locks the canonical coordinate and full digest. Up to 50 matches are shown; type a prefix to narrow the list. Use advanced manual input for unlisted older versions or Conan revisions.",
                  )}
                </span>
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
            </Form.Item>

            {selectedArtifact && !manualIdentity && (
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-md border border-cyan-500/20 bg-cyan-500/5 px-3 py-2.5 md:col-span-2">
                <div className="min-w-0">
                  <p className="truncate font-mono text-xs text-zinc-200">
                    {selectedArtifact.coordinate}
                  </p>
                  <p className="mt-0.5 break-all font-mono text-[11px] text-zinc-500">
                    {selectedArtifact.digest}
                  </p>
                </div>
                <Tag color={scanStatus(selectedArtifact, jobs, text).color}>
                  {scanStatus(selectedArtifact, jobs, text).label}
                </Tag>
              </div>
            )}

            <Form.Item
              name="coordinate"
              label={text("制品坐标", "Artifact coordinate")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: text(
                    "请输入制品坐标",
                    "Enter an artifact coordinate",
                  ),
                },
                {
                  max: 1024,
                  message: text(
                    "制品坐标过长",
                    "Artifact coordinate is too long",
                  ),
                },
              ]}
            >
              <Input
                disabled={!artifactScanning || !canManage}
                placeholder={coordinatePlaceholder(repo.format)}
                autoComplete="off"
              />
            </Form.Item>
            <Form.Item
              name="digest"
              label={text("SHA-256 摘要", "SHA-256 digest")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: text(
                    "请输入 SHA-256 摘要",
                    "Enter a SHA-256 digest",
                  ),
                },
                {
                  pattern: /^sha256:[0-9a-f]{64}$/,
                  message: text(
                    "请输入 sha256: 开头的 64 位小写十六进制摘要",
                    "Enter sha256: followed by 64 lowercase hexadecimal characters",
                  ),
                },
              ]}
            >
              <Input
                className="font-mono"
                disabled={!artifactScanning || !canManage}
                placeholder="sha256:…"
                autoComplete="off"
                spellCheck={false}
              />
            </Form.Item>
            <div className="grid gap-3 border-t border-zinc-800/70 pt-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center md:col-span-2">
              <p className="max-w-2xl text-xs leading-5 text-zinc-500">
                {manualIdentity
                  ? text(
                      "高级模式要求坐标和摘要与仓库中的不可变制品完全一致。",
                      "Advanced mode requires an exact coordinate and digest match.",
                    )
                  : text(
                      "扫描只读取已选制品，不会拉取或修改上游内容。",
                      "The scan only reads the selected artifact and never fetches or modifies upstream content.",
                    )}
              </p>
              <Button
                className="w-full sm:w-auto"
                type="primary"
                htmlType={manualIdentity ? "submit" : "button"}
                aria-label={text("提交扫描", "Queue scan")}
                icon={<ScanOutlined />}
                loading={submitting}
                disabled={
                  !artifactScanning ||
                  !canManage ||
                  (!manualIdentity && !selectedArtifact)
                }
                onClick={
                  !manualIdentity && selectedArtifact
                    ? () =>
                        void submitScan({
                          coordinate: selectedArtifact.coordinate,
                          digest: selectedArtifact.digest,
                        })
                    : undefined
                }
              >
                {text("提交扫描", "Queue scan")}
              </Button>
            </div>
          </Form>
        </div>
      </Card>

      {reconcileNotice && (
        <Alert type="success" showIcon title={reconcileNotice} />
      )}
      {reconcileError !== null && <ErrorBanner error={reconcileError} />}

      <Card>
        <CardHeader
          title={text("最近扫描任务", "Recent scan jobs")}
          extra={
            <Button
              size="small"
              aria-label={text(
                "对账历史制品",
                "Reconcile historical artifacts",
              )}
              icon={<SyncOutlined />}
              loading={reconciling}
              disabled={!publicationScanning || !canManage}
              onClick={() => void reconcileScans()}
            >
              {text("对账历史制品", "Reconcile historical artifacts")}
            </Button>
          }
        />
        {!canViewJobs ? (
          <EmptyState
            compact
            title={text(
              "最近任务仅对仓库管理员可见",
              "Recent jobs are visible to repository administrators",
            )}
            hint={text(
              "扫描提交成功后仍会在上方返回本次任务 ID。",
              "A successful submission still returns its job ID above.",
            )}
          />
        ) : loadError !== null ? (
          <div className="p-5">
            <ErrorBanner error={loadError} onRetry={load} />
          </div>
        ) : jobs === null ? (
          <Loading />
        ) : jobs.length === 0 ? (
          <EmptyState
            compact
            title={text("暂无扫描任务", "No scan jobs")}
            hint={text(
              "手动提交或对账历史制品后，任务会显示在这里。",
              "Jobs appear here after a manual submission or historical reconciliation.",
            )}
          />
        ) : (
          <Table<LifecycleJob>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={jobs}
            columns={columns}
            pagination={{ pageSize: 10, hideOnSinglePage: true }}
            scroll={{ x: 900 }}
          />
        )}
      </Card>
    </div>
  );
}
