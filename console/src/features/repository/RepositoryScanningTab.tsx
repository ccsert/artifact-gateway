import type { LifecycleJob } from "../../client";
import { Alert, Space, Tag } from "antd";
import type { Repository, RepositoryCapabilities } from "../../client";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";

import { ScanJobsCard } from "./ScanJobsCard";
import { ScanRequestCard } from "./ScanRequestCard";
import { useScanJobs } from "./repositoryQueries";
import { useQueryClient } from "@tanstack/react-query";
import { repositoryKeys } from "../../lib/query/keys";

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
  const jobsQuery = useScanJobs(repo.id, canViewJobs);
  // Without permission to view jobs the list is simply empty.
  const jobs = canViewJobs ? (jobsQuery.data ?? null) : [];
  const loadError = jobsQuery.error;
  const load = () => jobsQuery.refetch();
  const queryClient = useQueryClient();
  // A queued scan shows at the top of the list before the next poll.
  const showSubmittedJob = (job: LifecycleJob) =>
    queryClient.setQueryData<LifecycleJob[]>(
      repositoryKeys.scanJobs(repo.id),
      (current) => [
        job,
        ...(current ?? []).filter((item) => item.id !== job.id),
      ],
    );

  const artifactScanning = capabilities?.artifactScanning === true;
  const publicationScanning = capabilities?.publicationScanning === true;

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
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div>
          <h2 className="text-base font-semibold text-fg-strong">
            {text("制品扫描", "Artifact scanning")}
          </h2>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-fg-tertiary">
            {text(
              "扫描将 SBOM、许可证和漏洞结果写入制品情报；扫描失败不回滚上传，结果也不会自动隔离或阻断读取。",
              "Scanning writes SBOM, license, and vulnerability results to artifact intelligence. Failures do not roll back uploads, and results do not automatically quarantine artifacts or block reads.",
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

      <ScanRequestCard
        repo={repo}
        artifactScanning={artifactScanning}
        canManage={canManage}
        jobs={jobs}
        onSubmitted={showSubmittedJob}
      />

      <ScanJobsCard
        repo={repo}
        jobs={jobs}
        loadError={loadError}
        canManage={canManage}
        canViewJobs={canViewJobs}
        publicationScanning={publicationScanning}
        onReload={load}
      />
    </div>
  );
}
