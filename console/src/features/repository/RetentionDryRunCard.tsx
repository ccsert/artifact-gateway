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
  const { text } = usePreferences();
  const dryRunColumns: ColumnsType<RetentionDryRun["candidates"][number]> = [
    {
      title: text("清理单位", "Cleanup unit"),
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
      title: text("类型", "Type"),
      key: "versionType",
      width: 140,
      render: (_, candidate) => (
        <span className="text-xs text-fg-secondary">
          {retentionCandidateTypeLabel(
            candidate.versionType,
            copy.candidateType,
            text,
          )}
        </span>
      ),
    },
    {
      title: text("原因", "Reason"),
      key: "reasons",
      width: 280,
      render: (_, candidate) => (
        <span className="text-xs text-fg-secondary">
          {candidate.reasons
            .map((reason) =>
              reason === "maximum_versions"
                ? text("超过版本上限", "Exceeded version limit")
                : candidate.versionType === "asset"
                  ? text(
                      `已 ${candidate.ageDays} 天未更新`,
                      `Not updated for ${candidate.ageDays} days`,
                    )
                  : text(
                      `已保留 ${candidate.ageDays} 天`,
                      `Retained for ${candidate.ageDays} days`,
                    ),
            )
            .join("、")}
        </span>
      ),
    },
    {
      title: text("下载依据", "Download evidence"),
      key: "downloadEvidence",
      width: 220,
      render: (_, candidate) =>
        candidate.downloadCount && candidate.downloadCount > 0 ? (
          <span className="text-xs text-fg-secondary">
            {text(
              `${candidate.downloadCount} 次 · 最近 ${formatDate(candidate.lastDownloadedAt)}`,
              `${candidate.downloadCount} · last ${formatDate(candidate.lastDownloadedAt)}`,
            )}
          </span>
        ) : (
          <span className="text-xs text-fg-disabled">
            {text("无下载记录", "Never downloaded")}
          </span>
        ),
    },
    {
      title: isRaw
        ? text("最后更新时间", "Last updated")
        : text("创建时间", "Created"),
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
        title={text(
          `试运行结果：已加载 ${dryRun.candidates.length} / 共 ${dryRun.totalCandidates} 个候选${copy.candidateName}（策略版本 ${dryRun.policyVersion}）`,
          `Dry-run results: loaded ${dryRun.candidates.length} of ${dryRun.totalCandidates} candidate ${copy.candidateName} (policy version ${dryRun.policyVersion})`,
        )}
        extra={
          dryRun.totalCandidates > 0 ? (
            <Tooltip
              title={text(
                "导出完整候选集，不受当前分页影响",
                "Export the complete candidate set, independent of the current page",
              )}
            >
              <Button
                size="small"
                icon={<DownloadOutlined />}
                loading={exporting}
                onClick={() => void exportDryRun()}
              >
                {text("导出 CSV", "Export CSV")}
              </Button>
            </Tooltip>
          ) : undefined
        }
      />
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2 border-b border-line px-4 py-3 text-xs text-fg-secondary">
        <span>
          {text("按期限", "By age")}{" "}
          <strong className="font-medium text-fg">
            {dryRun.summary.reasonCounts.age}
          </strong>
        </span>
        <span>
          {text("超过版本上限", "Exceeded version limit")}{" "}
          <strong className="font-medium text-fg">
            {dryRun.summary.reasonCounts.maximumVersions}
          </strong>
        </span>
        <span>
          {text("类型：", "Types: ")}
          {[
            [text("发布", "Release"), dryRun.summary.versionTypeCounts.release],
            [
              text("快照", "Snapshot"),
              dryRun.summary.versionTypeCounts.snapshot,
            ],
            [text("版本", "Version"), dryRun.summary.versionTypeCounts.version],
            [text("资产", "Asset"), dryRun.summary.versionTypeCounts.asset],
          ]
            .filter(([, count]) => Number(count) > 0)
            .map(([label, count]) => `${label} ${count}`)
            .join(", ") || text("无", "None")}
        </span>
        <span>
          {text("最早候选", "Oldest candidate")}{" "}
          {formatDate(dryRun.summary.oldestCandidateAt)}
        </span>
      </div>
      {dryRun.candidates.length === 0 ? (
        <EmptyState
          title={text(
            `没有需要清理的${copy.candidateName}`,
            `No ${copy.candidateName} to clean`,
          )}
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
        label={text("加载更多候选", "Load more candidates")}
      />
    </Card>
  );
}
