import type { Translate } from "../../lib/preferences";
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

/** Search result columns: artifact, repository, match, size, time, action. */
export function searchColumns({
  expandedRow,
  locale,
  openHit,
  setRowExpanded,
  t,
}: {
  expandedRow: string | null;
  locale: string;
  openHit: (hit: GlobalArtifactSearchHit) => void;
  setRowExpanded: (tableRow: SearchTableRow, next: boolean) => void;
  t: Translate;
}): ColumnsType<SearchTableRow> {
  return [
    {
      title: t("search.artifactCoordinate"),
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
                  ? t("search.sHA256Match")
                  : t("search.coordinateMatch")}
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
              {expanded ? t("search.expanded") : t("search.collapsed")}
            </span>
          </div>
        );
      },
    },
    {
      title: t("search.repository"),
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
      title: t("search.format"),
      key: "format",
      width: 110,
      render: (_, tableRow) => <FormatBadge format={tableRow.row.format} />,
    },
    {
      title: t("search.versionsSize"),
      key: "versionOrSize",
      width: 150,
      render: (_, tableRow) => (
        <span className="text-xs text-fg-secondary">
          {isMavenGroup(tableRow.row)
            ? t("search.versions", { length: tableRow.row.hits.length })
            : artifactVersionSizeLabel(tableRow.row)}
        </span>
      ),
    },
    {
      title: t("search.security"),
      key: "intelligence",
      width: 150,
      render: (_, tableRow) => (
        <ArtifactSecurityBadge
          summary={
            isMavenGroup(tableRow.row)
              ? mavenIntelligence(tableRow.row.hits)
              : tableRow.row.intelligence
          }
          t={t}
        />
      ),
    },
    {
      title: t("search.updated"),
      key: "createdAt",
      width: 180,
      render: (_, tableRow) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(tableRow.representative.createdAt, locale)}
        </span>
      ),
    },
    {
      title: t("search.actions"),
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
                  ? t("search.collapseDetails")
                  : t("search.expandDetails")
              }
            >
              <Button
                type="text"
                size="small"
                aria-label={`${expanded ? t("search.collapse") : t("search.expand")} ${tableRow.row.coordinate}`}
                icon={expanded ? <UpOutlined /> : <DownOutlined />}
                onClick={(event) => {
                  event.stopPropagation();
                  setRowExpanded(tableRow, !expanded);
                }}
              />
            </Tooltip>
            <Tooltip title={t("search.openExactRepositoryLocation")}>
              <Button
                type="text"
                size="small"
                icon={<LinkOutlined />}
                aria-label={t("search.openInRepository", {
                  coordinate: tableRow.row.coordinate,
                })}
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
