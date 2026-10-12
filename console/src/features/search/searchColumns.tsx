import { artifactCoordinateForDisplay } from "../../formats";
import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button, Space, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { GlobalArtifactSearchHit } from "../../client";
import { Badge, FormatBadge } from "../../components/ui/Badge";
import { ArtifactSecurityBadge } from "../artifact-detail/ArtifactSecurityBadge";
import { formatDate } from "../../lib/format";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";

import {
  type SearchTableRow,
  isMavenGroup,
  artifactTarget,
  artifactVersionSizeLabel,
  mavenIntelligence,
} from "./searchModel";
type Localize = (chinese: string, english: string) => string;

/** Search result columns: artifact, repository, match, size, time, action. */
export function searchColumns({
  expandedRow,
  locale,
  openHit,
  setRowExpanded,
  text,
}: {
  expandedRow: string | null;
  locale: string;
  openHit: (hit: GlobalArtifactSearchHit) => void;
  setRowExpanded: (tableRow: SearchTableRow, next: boolean) => void;
  text: Localize;
}): ColumnsType<SearchTableRow> {
  return [
    {
      title: text("制品 / 坐标", "Artifact / coordinate"),
      key: "coordinate",
      width: 420,
      render: (_, tableRow) => {
        const expanded = expandedRow === tableRow.key;
        const row = tableRow.row;
        const displayCoordinate = artifactCoordinateForDisplay(
          row.format,
          row.coordinate,
        );
        return (
          <div>
            <div className="flex min-w-0 items-center gap-2">
              <span
                className="truncate font-mono text-xs text-fg"
                title={displayCoordinate}
                dir="auto"
              >
                {displayCoordinate}
              </span>
              <Badge
                tone={
                  tableRow.representative.matchKind === "digest"
                    ? "visualization-1"
                    : "neutral"
                }
              >
                {tableRow.representative.matchKind === "digest"
                  ? text("SHA-256 匹配", "SHA-256 match")
                  : text("坐标匹配", "Coordinate match")}
              </Badge>
            </div>
            {!isMavenGroup(row) && row.digest && (
              <div className="mt-1">
                <CopyableValue
                  value={row.digest}
                  label={row.digest.slice(0, 20)}
                />
              </div>
            )}
            <span className="sr-only">
              {expanded
                ? text("已展开", "Expanded")
                : text("已收起", "Collapsed")}
            </span>
          </div>
        );
      },
    },
    {
      title: text("仓库", "Repository"),
      key: "repository",
      width: 200,
      render: (_, tableRow) => (
        <Link
          to={artifactTarget(tableRow.representative)}
          onClick={(event) => event.stopPropagation()}
          className="text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
        >
          {tableRow.row.repositoryName}
        </Link>
      ),
    },
    {
      title: text("格式", "Format"),
      key: "format",
      width: 110,
      render: (_, tableRow) => <FormatBadge format={tableRow.row.format} />,
    },
    {
      title: text("版本 / 大小", "Versions / size"),
      key: "versionOrSize",
      width: 150,
      render: (_, tableRow) => (
        <span className="text-xs text-fg-secondary">
          {isMavenGroup(tableRow.row)
            ? text(
                `${tableRow.row.hits.length} 个版本`,
                `${tableRow.row.hits.length} versions`,
              )
            : artifactVersionSizeLabel(tableRow.row)}
        </span>
      ),
    },
    {
      title: text("安全情报", "Security"),
      key: "intelligence",
      width: 150,
      render: (_, tableRow) => (
        <ArtifactSecurityBadge
          summary={
            isMavenGroup(tableRow.row)
              ? mavenIntelligence(tableRow.row.hits)
              : tableRow.row.intelligence
          }
          text={text}
        />
      ),
    },
    {
      title: text("更新时间", "Updated"),
      key: "createdAt",
      width: 180,
      render: (_, tableRow) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(tableRow.representative.createdAt, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, tableRow) => {
        const expanded = expandedRow === tableRow.key;
        return (
          <Space size={2}>
            <Tooltip
              title={
                expanded
                  ? text("收起详情", "Collapse details")
                  : text("展开详情", "Expand details")
              }
            >
              <Button
                type="text"
                size="small"
                aria-label={`${expanded ? text("收起", "Collapse") : text("展开", "Expand")} ${tableRow.row.coordinate}`}
                icon={expanded ? <UpOutlined /> : <DownOutlined />}
                onClick={(event) => {
                  event.stopPropagation();
                  setRowExpanded(tableRow, !expanded);
                }}
              />
            </Tooltip>
            <Tooltip
              title={text("在仓库中精确定位", "Open exact repository location")}
            >
              <Button
                type="text"
                size="small"
                icon={<LinkOutlined />}
                aria-label={text(
                  `在仓库中打开 ${tableRow.row.coordinate}`,
                  `Open ${tableRow.row.coordinate} in repository`,
                )}
                onClick={(event) => {
                  event.stopPropagation();
                  openHit(tableRow.representative);
                }}
              />
            </Tooltip>
          </Space>
        );
      },
    },
  ];
}
