import {
  artifactCoordinateForDisplay,
  hasPackageVersions,
} from "../../formats";
import {
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LinkOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import { PublicArtifactDetail } from "./PublicArtifactDetail";
import type { PublicArtifactTableRow } from "./publicArtifactRows";
import type { PublicRepository } from "./types";

type ArtifactLink = (
  coordinate: string,
  buildNumber?: number,
  tag?: string,
  revision?: string,
  version?: string,
) => string;
type OpenArtifact = (
  coordinate: string,
  buildNumber?: number,
  tag?: string,
  revision?: string,
  version?: string,
) => void;

/**
 * Artifacts of the formats without version grouping (OCI, npm, PyPI, Go,
 * Cargo, APT, Raw). Expanding a row shows the format's own detail or, for
 * OCI, the version picker.
 */
export function PublicArtifactTable({
  rows,
  repository,
  artifactParam,
  versionParam,
  copiedCoordinate,
  artifactHref,
  onToggleRow,
  onOpenArtifact,
  onSelectOciVersion,
  onLoadOciTags,
  onLoadOciManifest,
  onCopyCoordinate,
  onCopyPageLink,
  onCopyUsage,
}: {
  rows: PublicArtifactTableRow[];
  repository: PublicRepository;
  artifactParam: string;
  versionParam: string;
  copiedCoordinate: string | null;
  artifactHref: ArtifactLink;
  onToggleRow: (row: PublicArtifactTableRow) => void;
  onOpenArtifact: OpenArtifact;
  onSelectOciVersion: (coordinate: string, value: string) => void;
  onLoadOciTags: (coordinate: string, after?: string) => Promise<void>;
  onLoadOciManifest: (coordinate: string, tag: string) => Promise<void>;
  onCopyCoordinate: (coordinate: string) => unknown;
  onCopyPageLink: (href: string) => unknown;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { locale, t } = usePreferences();
  const selectedRepository: PublicRepository | undefined = repository;
  const toggleArtifactRow = onToggleRow;
  const copyCoordinate = onCopyCoordinate;

  const artifactColumns: ColumnsType<PublicArtifactTableRow> = [
    {
      title:
        selectedRepository?.format === "oci"
          ? t("publicBrowse.image")
          : selectedRepository?.format === "apt"
            ? t("publicBrowse.aptPath")
            : t("publicBrowse.artifactCoordinate"),
      key: "coordinate",
      width: 320,
      render: (_, row) => {
        const displayCoordinate = artifactCoordinateForDisplay(
          selectedRepository?.format ?? "",
          row.item.coordinate,
        );
        return (
          <span
            className="block max-w-md truncate font-mono text-xs text-fg-strong"
            title={displayCoordinate}
            dir="auto"
          >
            {displayCoordinate}
          </span>
        );
      },
    },
    ...(selectedRepository?.format === "oci"
      ? [
          {
            title: t("publicBrowse.loadedVersions"),
            key: "loadedVersions",
            width: 130,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-fg-tertiary">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? t("publicBrowse.loading")
                  : !row.protocolVersionsLoaded
                    ? t("publicBrowse.loadWhenExpanded")
                    : row.protocolVersions.length > 0
                      ? t("publicBrowse.versionCount", {
                          versionCount: row.protocolVersions.length,
                          more: row.nextProtocolPage ? "+" : "",
                        })
                      : "—"}
              </span>
            ),
          },
          {
            title: t("publicBrowse.selectedVersion"),
            key: "selectedVersion",
            width: 190,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="block max-w-[180px] truncate font-mono text-xs text-fg-tertiary">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? t("publicBrowse.loading")
                  : !row.protocolVersionsLoaded
                    ? "—"
                    : (row.selectedProtocolVersionItem?.label ?? "—")}
              </span>
            ),
          },
          {
            title: t("publicBrowse.imageDigest"),
            key: "digest",
            width: 160,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="font-mono text-xs text-fg-tertiary">
                {shortDigest(
                  row.ociDetail?.digest ??
                    row.selectedProtocolVersionItem?.digest ??
                    row.item.digest,
                )}
              </span>
            ),
          },
        ]
      : [
          {
            title: t("publicBrowse.digest"),
            key: "digest",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="font-mono text-xs text-fg-tertiary">
                {shortDigest(row.item.digest)}
              </span>
            ),
          },
          {
            title: t("publicBrowse.size"),
            key: "size",
            width: 120,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="text-xs text-fg-secondary">
                {formatBytes(row.item.size)}
              </span>
            ),
          },
          ...(selectedRepository?.format === "apt"
            ? [
                {
                  title: t("publicBrowse.contentType"),
                  key: "contentType",
                  width: 220,
                  render: (_: unknown, row: PublicArtifactTableRow) => (
                    <span className="block max-w-[210px] truncate font-mono text-xs text-fg-tertiary">
                      {row.item.contentType ?? "—"}
                    </span>
                  ),
                },
              ]
            : []),
          {
            title:
              selectedRepository?.format === "apt"
                ? t("publicBrowse.firstCached")
                : t("publicBrowse.created"),
            key: "createdAt",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-fg-tertiary">
                {formatDate(row.item.createdAt, locale)}
              </span>
            ),
          },
        ]),
    {
      title: t("publicBrowse.actions"),
      key: "actions",
      fixed: "right",
      width: 230,
      render: (_, row) => (
        <div className="whitespace-nowrap text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => toggleArtifactRow(row)}
          >
            {row.expanded
              ? t("publicBrowse.collapse")
              : row.isOci
                ? t("publicBrowse.selectVersion")
                : t("publicBrowse.usage")}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={
              row.isOci
                ? row.selectedProtocolHref
                : artifactHref(
                    row.item.coordinate,
                    undefined,
                    undefined,
                    undefined,
                    hasPackageVersions(selectedRepository?.format)
                      ? (row.item.version ?? versionParam)
                      : undefined,
                  )
            }
          >
            {row.isOci && row.selectedProtocolVersionItem
              ? t("publicBrowse.openVersion")
              : t("publicBrowse.open")}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.item.coordinate
                ? t("publicBrowse.copied")
                : t("publicBrowse.copyCoordinate")
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={t("publicBrowse.copy", {
                coordinate: artifactCoordinateForDisplay(
                  selectedRepository?.format ?? "",
                  row.item.coordinate,
                ),
              })}
              onClick={() => void copyCoordinate(row.item.coordinate)}
              icon={
                copiedCoordinate === row.item.coordinate ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )
              }
            />
          </Tooltip>
        </div>
      ),
    },
  ];

  const expandedArtifactRowRender = (row: PublicArtifactTableRow) => (
    <PublicArtifactDetail
      row={row}
      repository={repository}
      artifactParam={artifactParam}
      versionParam={versionParam}
      copiedCoordinate={copiedCoordinate}
      onOpenArtifact={onOpenArtifact}
      onSelectOciVersion={onSelectOciVersion}
      onLoadOciTags={onLoadOciTags}
      onLoadOciManifest={onLoadOciManifest}
      onCopyPageLink={onCopyPageLink}
      onCopyUsage={onCopyUsage}
    />
  );

  return (
    <ConsoleTable<PublicArtifactTableRow>
      rowKey="key"
      dataSource={rows}
      columns={artifactColumns}
      pagination={false}
      scroll={{
        x: selectedRepository?.format === "oci" ? 1100 : 900,
      }}
      expandable={{
        expandedRowKeys: rows
          .filter((row) => row.expanded)
          .map((row) => row.key),
        expandedRowRender: expandedArtifactRowRender,
        showExpandColumn: false,
      }}
    />
  );
}
