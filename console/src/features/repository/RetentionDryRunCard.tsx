import { Button, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { DownloadOutlined } from "@ant-design/icons";
import type { Repository, RetentionDryRun } from "../../client";
import { EmptyState } from "../../components/ui/Feedback";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { artifactCoordinateForDisplay } from "../../formats";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import {
  retentionCandidateTypeLabel,
  retentionFormatCopy,
} from "./retentionCopy";

/** A dry run's candidates, summary, and the page loader for more. */
export function RetentionDryRunCard({
  repo,
  dryRun,
  copy,
  dryRunLoadingMore,
  loadMoreDryRun,
  isRaw,
  exporting,
  exportDryRun,
}: {
  repo: Repository;
  dryRun: RetentionDryRun;
  copy: ReturnType<typeof retentionFormatCopy>;
  dryRunLoadingMore: boolean;
  loadMoreDryRun: () => void;
  /** Raw retention counts stored paths, not versions. */
  isRaw: boolean;
  exporting: boolean;
  exportDryRun: () => unknown;
}) {
  const { t } = usePreferences();
  const dryRunColumns: ColumnsType<RetentionDryRun["candidates"][number]> = [
    {
      title: t("repository.cleanupUnit"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => {
        const displayCoordinate = artifactCoordinateForDisplay(
          repo.format,
          value,
        );
        return (
          <span
            className="block max-w-md truncate font-mono text-xs text-fg"
            title={displayCoordinate}
            dir="auto"
          >
            {displayCoordinate}
          </span>
        );
      },
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
      title: t("repository.type"),
      key: "versionType",
      width: 140,
      render: (_, candidate) => (
        <span className="text-xs text-fg-secondary">
          {retentionCandidateTypeLabel(
            candidate.versionType,
            copy.candidateType,
            t,
          )}
        </span>
      ),
    },
    {
      title: t("repository.reason"),
      key: "reasons",
      width: 280,
      render: (_, candidate) => (
        <span className="text-xs text-fg-secondary">
          {candidate.reasons
            .map((reason) =>
              reason === "maximum_versions"
                ? t("repository.exceededVersionLimit")
                : candidate.versionType === "asset"
                  ? t("repository.notUpdatedForDays", {
                      ageDays: candidate.ageDays,
                    })
                  : t("repository.retainedForDays", {
                      ageDays: candidate.ageDays,
                    }),
            )
            .join("、")}
        </span>
      ),
    },
    {
      title: t("repository.downloadEvidence"),
      key: "downloadEvidence",
      width: 220,
      render: (_, candidate) =>
        candidate.downloadCount && candidate.downloadCount > 0 ? (
          <span className="text-xs text-fg-secondary">
            {t("repository.last", {
              downloadCount: candidate.downloadCount,
              date: formatDate(candidate.lastDownloadedAt),
            })}
          </span>
        ) : (
          <span className="text-xs text-fg-disabled">
            {t("repository.neverDownloaded")}
          </span>
        ),
    },
    {
      title: isRaw ? t("repository.lastUpdated") : t("repository.created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value)}
        </span>
      ),
    },
  ];

  return (
    <Card>
      <CardHeader
        title={t("repository.dryRunResultsLoadedOfCandidatePolicyVersion", {
          length: dryRun.candidates.length,
          totalCandidates: dryRun.totalCandidates,
          candidateName: copy.candidateName,
          policyVersion: dryRun.policyVersion,
        })}
        extra={
          dryRun.totalCandidates > 0 ? (
            <Tooltip
              title={t(
                "repository.exportTheCompleteCandidateSetIndependentOfThe",
              )}
            >
              <Button
                size="small"
                icon={<DownloadOutlined />}
                loading={exporting}
                onClick={() => void exportDryRun()}
              >
                {t("repository.exportCSV")}
              </Button>
            </Tooltip>
          ) : undefined
        }
      />
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2 border-b border-line px-4 py-3 text-xs text-fg-secondary">
        <span>
          {t("repository.byAge")}{" "}
          <strong className="font-medium text-fg">
            {dryRun.summary.reasonCounts.age}
          </strong>
        </span>
        <span>
          {t("repository.exceededVersionLimit")}{" "}
          <strong className="font-medium text-fg">
            {dryRun.summary.reasonCounts.maximumVersions}
          </strong>
        </span>
        <span>
          {t("repository.types")}
          {[
            [t("repository.release"), dryRun.summary.versionTypeCounts.release],
            [
              t("repository.snapshot"),
              dryRun.summary.versionTypeCounts.snapshot,
            ],
            [t("repository.version"), dryRun.summary.versionTypeCounts.version],
            [t("repository.asset"), dryRun.summary.versionTypeCounts.asset],
          ]
            .filter(([, count]) => Number(count) > 0)
            .map(([label, count]) => `${label} ${count}`)
            .join(", ") || t("repository.none")}
        </span>
        <span>
          {t("repository.oldestCandidate")}{" "}
          {formatDate(dryRun.summary.oldestCandidateAt)}
        </span>
      </div>
      {dryRun.candidates.length === 0 ? (
        <EmptyState
          title={t("repository.noToClean", {
            candidateName: copy.candidateName,
          })}
        />
      ) : (
        <ConsoleTable<RetentionDryRun["candidates"][number]>
          rowKey={(candidate) =>
            `${candidate.format}:${candidate.coordinate}:${candidate.digest}`
          }
          dataSource={dryRun.candidates}
          columns={dryRunColumns}
          pagination={false}
          scroll={{ x: 1080 }}
        />
      )}
      <Pagination
        hasMore={!!dryRun.nextPageToken}
        onMore={() => void loadMoreDryRun()}
        loading={dryRunLoadingMore}
        label={t("repository.loadMoreCandidates")}
      />
    </Card>
  );
}
