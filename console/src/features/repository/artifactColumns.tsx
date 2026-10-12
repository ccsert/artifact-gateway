import type { ColumnsType } from "antd/es/table";
import type { Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import type { FormatDescriptor } from "../../formats";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { decodeRawPathForDisplay } from "../../lib/rawPath";
import { ArtifactSecurityBadge } from "../artifact-detail/ArtifactSecurityBadge";
import type { ArtifactRow } from "./artifactRow";

type Localize = (chinese: string, english: string) => string;

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
  text,
}: {
  descriptor: FormatDescriptor | undefined;
  repo: Repository;
  proxyMaven: boolean;
  showSecurityColumn: boolean;
  text: Localize;
}): ColumnsType<ArtifactRow> {
  const securityColumn: ColumnsType<ArtifactRow>[number] = {
    title: text("安全", "Security"),
    key: "intelligence",
    width: 150,
    render: (_, record) => (
      <ArtifactSecurityBadge summary={record.intelligence} text={text} />
    ),
  };

  return descriptor?.nameListing
    ? [
        {
          title: text("名称", "Name"),
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
            title: text("Maven 坐标", "Maven coordinate"),
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
            title: text("文件", "Files"),
            key: "fileCount",
            width: 120,
            render: (_, record) => (
              <Badge tone="neutral">
                {text(
                  `${record.fileCount ?? record.files?.length ?? 0} 个文件`,
                  `${record.fileCount ?? record.files?.length ?? 0} files`,
                )}
              </Badge>
            ),
          },
          {
            title: text("主文件大小", "Primary size"),
            key: "size",
            width: 140,
            render: (_, record) => (
              <span className="text-xs text-fg-secondary">
                {formatBytes(record.size)}
              </span>
            ),
          },
          {
            title: text("类型", "Type"),
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
              title: text("APT 路径", "APT path"),
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
              title: text("内容类型", "Content type"),
              dataIndex: "contentType",
              key: "contentType",
              width: 220,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span className="text-xs text-fg-tertiary">{value ?? "—"}</span>
              ),
            },
            {
              title: text("大小", "Size"),
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
              title: text("摘要", "Digest"),
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
                  ? text("首次缓存", "First cached")
                  : text("已发布", "Published"),
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
                title: text(...descriptor.artifactColumn),
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
                title: text("版本", "Versions"),
                key: "versionCount",
                width: 120,
                render: (_, record) => (
                  <Badge tone="neutral">
                    {record.versionCount !== undefined
                      ? text(
                          `${record.versionCount} 个版本`,
                          `${record.versionCount} versions`,
                        )
                      : text("展开查看", "Open to inspect")}
                  </Badge>
                ),
              },
              {
                title: text("最新版本", "Latest version"),
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
                title: text("更新时间", "Updated"),
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
                title: text("文件路径", "File path"),
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
                title: text("摘要", "Digest"),
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
                title: text("大小", "Size"),
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
                title: text("最后更新时间", "Last updated"),
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
