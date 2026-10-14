import type { Translate } from "../../lib/preferences";
import type { ColumnsType } from "antd/es/table";
import type { Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import type { FormatDescriptor } from "../../formats";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { decodeRawPathForDisplay } from "../../lib/rawPath";
import { ArtifactSecurityBadge } from "../artifact-detail/ArtifactSecurityBadge";
import type { ArtifactRow } from "./artifactRow";

/**
 * The artifact table's columns for one repository: name-listed formats,
 * Maven proxy cache entries, repository assets, versioned packages, or
 * stored paths.
 */
export function artifactColumns({
  descriptor,
  repo,
  proxyMaven,
  showSecurityColumn,
  t,
}: {
  descriptor: FormatDescriptor | undefined;
  repo: Repository;
  proxyMaven: boolean;
  showSecurityColumn: boolean;
  t: Translate;
}): ColumnsType<ArtifactRow> {
  const securityColumn: ColumnsType<ArtifactRow>[number] = {
    title: t("repository.security"),
    key: "intelligence",
    width: 150,
    render: (_, record) => (
      <ArtifactSecurityBadge summary={record.intelligence} t={t} />
    ),
  };

  return descriptor?.nameListing
    ? [
        {
          title: t("repository.name"),
          dataIndex: "coordinate",
          key: "coordinate",
          ellipsis: true,
          render: (value: string, record) => (
            <span
              className="font-mono text-xs text-fg"
              title={record.coordinate}
            >
              {value}
            </span>
          ),
        },
      ]
    : proxyMaven
      ? [
          {
            title: t("repository.mavenCoordinate"),
            dataIndex: "coordinate",
            key: "coordinate",
            ellipsis: true,
            render: (value: string, record) => (
              <span
                className="font-mono text-xs text-fg"
                title={record.coordinate}
              >
                {value}
              </span>
            ),
          },
          {
            title: t("repository.files2"),
            key: "fileCount",
            width: 120,
            render: (_, record) => (
              <Badge tone="neutral">
                {t("repository.files3", {
                  count: record.fileCount ?? record.files?.length ?? 0,
                })}
              </Badge>
            ),
          },
          {
            title: t("repository.primarySize"),
            key: "size",
            width: 140,
            render: (_, record) => (
              <span className="text-xs text-fg-secondary">
                {formatBytes(record.size)}
              </span>
            ),
          },
          {
            title: t("repository.type"),
            dataIndex: "contentType",
            key: "contentType",
            width: 180,
            ellipsis: true,
            render: (value: string | undefined) => (
              <span className="text-xs text-fg-tertiary">{value ?? "—"}</span>
            ),
          },
        ]
      : descriptor?.assetColumns
        ? [
            {
              title: t("repository.aPTPath"),
              dataIndex: "coordinate",
              key: "coordinate",
              ellipsis: true,
              render: (value: string, record: ArtifactRow) => (
                <span
                  className="font-mono text-xs text-fg"
                  title={record.coordinate}
                >
                  {value}
                </span>
              ),
            },
            {
              title: t("repository.contentType"),
              dataIndex: "contentType",
              key: "contentType",
              width: 220,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span className="text-xs text-fg-tertiary">{value ?? "—"}</span>
              ),
            },
            {
              title: t("repository.size"),
              dataIndex: "size",
              key: "size",
              width: 120,
              render: (value: number | undefined) => (
                <span className="text-xs text-fg-secondary">
                  {formatBytes(value)}
                </span>
              ),
            },
            {
              title: t("repository.digest"),
              dataIndex: "digest",
              key: "digest",
              width: 180,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span
                  className="font-mono text-xs text-fg-tertiary"
                  title={value}
                >
                  {shortDigest(value)}
                </span>
              ),
            },
            {
              title:
                repo.type === "proxy"
                  ? t("repository.firstCached")
                  : t("repository.published"),
              dataIndex: "createdAt",
              key: "createdAt",
              width: 180,
              render: (value: string | undefined) => (
                <span className="whitespace-nowrap text-xs text-fg-tertiary">
                  {formatDate(value)}
                </span>
              ),
            },
          ]
        : descriptor?.versionColumns
          ? [
              {
                title: t(descriptor.artifactColumn),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string, record) => (
                  <span
                    className="font-mono text-xs text-fg"
                    title={record.coordinate}
                  >
                    {value}
                  </span>
                ),
              },
              {
                title: t("repository.versions"),
                key: "versionCount",
                width: 120,
                render: (_, record) => (
                  <Badge tone="neutral">
                    {record.versionCount !== undefined
                      ? t("repository.versions2", {
                          versionCount: record.versionCount,
                        })
                      : t("repository.openToInspect")}
                  </Badge>
                ),
              },
              {
                title: t("repository.latestVersion"),
                dataIndex: "latestVersion",
                key: "latestVersion",
                width: 160,
                render: (value: string | undefined) => (
                  <span className="font-mono text-xs text-[var(--ag-content-secondary)]">
                    {value ?? "—"}
                  </span>
                ),
              },
              {
                title: t("repository.updated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-fg-tertiary">
                    {formatDate(value)}
                  </span>
                ),
              },
              ...(showSecurityColumn ? [securityColumn] : []),
            ]
          : [
              {
                title: t("repository.filePath"),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string) => {
                  const displayPath = decodeRawPathForDisplay(value);
                  return (
                    <span
                      className="font-mono text-xs text-fg"
                      title={displayPath}
                    >
                      {displayPath}
                    </span>
                  );
                },
              },
              {
                title: t("repository.digest"),
                dataIndex: "digest",
                key: "digest",
                width: 180,
                ellipsis: true,
                render: (value: string | undefined) => (
                  <span
                    className="font-mono text-xs text-fg-tertiary"
                    title={value}
                  >
                    {shortDigest(value)}
                  </span>
                ),
              },
              {
                title: t("repository.size"),
                dataIndex: "size",
                key: "size",
                width: 120,
                render: (value: number | undefined) => (
                  <span className="text-xs text-fg-secondary">
                    {formatBytes(value)}
                  </span>
                ),
              },
              {
                title: t("repository.lastUpdated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-fg-tertiary">
                    {formatDate(value)}
                  </span>
                ),
              },
              ...(showSecurityColumn ? [securityColumn] : []),
            ];
}
