import { formatDescriptor, hasPackageVersions } from "../../formats";
import { formatViews } from "../../formats/views";
import { LinkOutlined } from "@ant-design/icons";
import { Button, Descriptions, Space, Tooltip } from "antd";
import type { GlobalArtifactSearchHit } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { formatBytes, formatDate } from "../../lib/format";
import { MavenArtifactDetail } from "../artifact-detail/MavenArtifactDetail";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "../repository/RepositoryFeatureUnavailable";

import {
  type SearchTableRow,
  isMavenGroup,
  mavenVersionLabel,
} from "./searchModel";
/** One search result opened: its repository detail, or a Maven version list. */
export function SearchHitDetail({
  tableRow,
  openHit,
  selectedVersion,
  setSelectedVersion,
}: {
  tableRow: SearchTableRow;
  openHit: (hit: GlobalArtifactSearchHit) => void;
  selectedVersion: string | null;
  setSelectedVersion: (key: string | null) => void;
}) {
  const { locale, t } = usePreferences();

  const row = tableRow.row;
  if (!formatDescriptor(row.format)) {
    return (
      <RepositoryFeatureUnavailable
        feature={t("search.artifactDetail")}
        unsupportedFormat={row.format}
      />
    );
  }
  if (isMavenGroup(row)) {
    const hit =
      row.hits.find(
        (item) =>
          `${row.key}:${item.coordinate}:${item.buildNumber ?? 0}` ===
          selectedVersion,
      ) ?? row.hits[0];
    return (
      <div className="px-2 py-1">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <div className="text-sm font-medium text-fg">
              {t("search.versionsAndBuilds")}
            </div>
            <div className="mt-1 text-xs text-fg-tertiary">
              {t("search.selectAVersionToViewItsPOMDigest")}
            </div>
          </div>
          <Button
            size="small"
            icon={<LinkOutlined />}
            onClick={() => openHit(tableRow.representative)}
          >
            {t("search.openLatestVersion")}
          </Button>
        </div>
        <div className="max-h-64 overflow-y-auto rounded-md border border-line">
          {row.hits.map((versionHit) => {
            const versionKey = `${row.key}:${versionHit.coordinate}:${versionHit.buildNumber ?? 0}`;
            const selected = selectedVersion === versionKey;
            return (
              <div
                key={versionKey}
                className={`flex items-center justify-between gap-4 border-b border-line px-3 py-2 last:border-b-0 ${selected ? "bg-[var(--ag-navigation-selected-start)]" : ""}`}
              >
                <button
                  type="button"
                  className="min-w-0 flex-1 text-left"
                  onClick={() => setSelectedVersion(versionKey)}
                >
                  <div className="truncate font-mono text-xs text-fg">
                    {mavenVersionLabel(versionHit)}
                  </div>
                  <div className="mt-0.5 text-xs text-fg-tertiary">
                    {formatDate(versionHit.createdAt, locale)} ·{" "}
                    {formatBytes(versionHit.size)} ·{" "}
                    {versionHit.publisher ?? t("search.publisherNotRecorded")}
                  </div>
                </button>
                <Space size={2}>
                  <Tooltip title={versionHit.digest ?? t("search.noDigest")}>
                    <span>
                      {versionHit.digest ? (
                        <CopyableValue
                          value={versionHit.digest}
                          label={versionHit.digest.slice(0, 14)}
                        />
                      ) : (
                        <span className="text-xs text-fg-disabled">
                          {t("search.noDigest2")}
                        </span>
                      )}
                    </span>
                  </Tooltip>
                  <Button
                    type="text"
                    size="small"
                    icon={<LinkOutlined />}
                    aria-label={t("search.open", {
                      coordinate: versionHit.coordinate,
                    })}
                    onClick={() => openHit(versionHit)}
                  />
                </Space>
              </div>
            );
          })}
        </div>
        <Descriptions
          size="small"
          column={4}
          className="my-4"
          items={[
            {
              key: "publisher",
              label: t("search.publisher"),
              children: hit.publisher ?? t("search.notRecorded"),
            },
            {
              key: "createdAt",
              label: t("search.published"),
              children: formatDate(hit.createdAt, locale),
            },
            {
              key: "size",
              label: t("search.size"),
              children: formatBytes(hit.size),
            },
            {
              key: "matchKind",
              label: t("search.matchedBy"),
              children:
                hit.matchKind === "digest"
                  ? t("search.sHA256Digest")
                  : t("search.artifactCoordinate2"),
            },
          ]}
        />
        {hit.digest && (
          <div className="mb-4 rounded-md border border-line bg-surface-translucent px-3 py-2.5">
            <div className="mb-1 text-xs font-medium text-fg-tertiary">
              SHA-256 Digest
            </div>
            <CopyableValue
              value={hit.digest}
              label={hit.digest}
              className="w-full text-xs text-fg-secondary"
            />
          </div>
        )}
        <MavenArtifactDetail
          repoId={hit.repositoryId}
          repoName={hit.repositoryName}
          meta={{
            coordinate: hit.coordinate,
            digest: hit.digest,
            size: hit.size,
            createdAt: hit.createdAt,
            publisher: hit.publisher,
            buildNumber: hit.buildNumber,
          }}
        />
        <div className="mt-4 flex justify-end">
          <Button
            type="primary"
            icon={<LinkOutlined />}
            onClick={() => openHit(hit)}
          >
            {t("search.openThisVersionInRepository")}
          </Button>
        </div>
      </div>
    );
  }
  // Maven hits are grouped above; formats without a search detail show
  // only the summary.
  const SearchDetail = formatDescriptor(row.format)?.searchDetail
    ? formatViews(row.format).ArtifactDetail
    : undefined;
  return (
    <div className="px-2 py-1">
      <Descriptions
        size="small"
        column={4}
        className="mb-4"
        items={[
          {
            key: "repository",
            label: t("search.repository"),
            children: row.repositoryName,
          },
          {
            key: "publisher",
            label: t("search.publisher"),
            children: row.publisher ?? t("search.notRecorded"),
          },
          {
            key: "contentType",
            label: t("search.contentType"),
            children: row.contentType ?? t("search.notRecorded"),
          },
          {
            key: "createdAt",
            label: t("search.published"),
            children: formatDate(row.createdAt, locale),
          },
        ]}
      />
      {row.digest && (
        <div className="mb-4 rounded-md border border-line bg-surface-translucent px-3 py-2.5">
          <div className="mb-1 flex items-center gap-2 text-xs font-medium text-fg-tertiary">
            <span>SHA-256 Digest</span>
            <Badge
              tone={row.matchKind === "digest" ? "visualization-1" : "neutral"}
            >
              {row.matchKind === "digest"
                ? t("search.exactMatch")
                : t("search.checksum")}
            </Badge>
          </div>
          <CopyableValue
            value={row.digest}
            label={row.digest}
            className="w-full text-xs text-fg-secondary"
          />
        </div>
      )}
      {SearchDetail && (
        <SearchDetail
          repositoryId={row.repositoryId}
          repositoryName={row.repositoryName}
          artifact={row}
          initialVersion={
            hasPackageVersions(row.format) ? row.version : undefined
          }
          initialReference={row.digest}
          managed
        />
      )}
      <div className="mt-4 flex justify-end">
        <Button
          type="primary"
          icon={<LinkOutlined />}
          onClick={() => openHit(row)}
        >
          {t("search.openInRepository2")}
        </Button>
      </div>
    </div>
  );
}
