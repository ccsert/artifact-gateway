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
  const { t } = usePreferences();
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
    return <Loading label={t("repository.checkingScanCapability")} />;
  }
  if (capabilitiesError !== null) {
    return <ErrorBanner error={capabilitiesError} />;
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line pb-4">
        <div>
          <h2 className="text-base font-semibold text-fg-strong">
            {t("repository.artifactScanning")}
          </h2>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-fg-tertiary">
            {t("repository.scanningWritesSBOMLicenseAndVulnerabilityResultsTo")}
          </p>
        </div>
        <Space wrap>
          <Tag color={artifactScanning ? "success" : "default"}>
            {t("repository.manualScan")}:{" "}
            {artifactScanning
              ? t("repository.available")
              : t("repository.unavailable")}
          </Tag>
          <Tag color={publicationScanning ? "success" : "default"}>
            {t("repository.scanOnPublishCapability")}:{" "}
            {publicationScanning
              ? t("repository.available")
              : t("repository.unavailable")}
          </Tag>
        </Space>
      </div>

      {!artifactScanning && (
        <Alert
          type="warning"
          showIcon
          title={t("repository.noScannerIsConfiguredForThisRepository")}
          description={
            <span>
              {t("repository.askAPlatformAdministratorToConfigure")}
              <code>GATEWAY_SCANNER_ENDPOINT</code>
              {t("repository.and")}
              <code>GATEWAY_SCANNER_FORMATS</code>
              {t("repository.inTheGatewayDeploymentAndEnableScanJobs")}
            </span>
          }
        />
      )}
      {artifactScanning && !canManage && (
        <Alert
          type="info"
          showIcon
          title={t("repository.thisIdentityCannotManageArtifactScans")}
          description={t(
            "repository.queueingScansAndHistoricalReconciliationRequireRepositoriesIntelligence",
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
