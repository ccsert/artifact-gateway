import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ArtifactSummary } from "../../client";
import { formatBytes, formatDate } from "../../lib/format";
import { usageFor, type UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import {
  mavenVersionKey,
  type MavenArtifactGroup,
} from "../../lib/publicBrowseModel";
import type { PublicRepository } from "./types";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

interface MavenGroupTableProps {
  groups: MavenArtifactGroup[];
  repository: PublicRepository;
  artifactParam: string;
  buildParam: string;
  expandedGroup: string | null;
  selectedVersionKey: string | null;
  copiedCoordinate: string | null;
  onExpand: (groupKey: string, versionKey: string) => void;
  onCollapse: () => void;
  onSelectVersion: (group: MavenArtifactGroup, versionKey: string) => void;
  artifactHref: (coordinate: string, buildNumber?: number) => string;
  onCopyPageLink: (href: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

interface MavenTableRow {
  key: string;
  group: MavenArtifactGroup;
  latest: ArtifactSummary;
  expanded: boolean;
  selectedKey: string;
  selectedVersion: ArtifactSummary;
  snippets: UsageSnippet[];
}

export function MavenGroupTable({
  groups,
  repository,
  artifactParam,
  buildParam,
  expandedGroup,
  selectedVersionKey,
  copiedCoordinate,
  onExpand,
  onCollapse,
  onSelectVersion,
  artifactHref,
  onCopyPageLink,
  onCopyUsage,
}: MavenGroupTableProps) {
  const { locale, t } = usePreferences();
  const tableRows: MavenTableRow[] = groups.map((group) => {
    const latest = group.versions[0];
    const urlVersion = group.versions.find(
      (version) =>
        version.coordinate === artifactParam &&
        (!buildParam || String(version.buildNumber ?? 0) === buildParam),
    );
    const expanded = expandedGroup === group.key || Boolean(urlVersion);
    const preferredKey =
      selectedVersionKey &&
      group.versions.some(
        (version, index) =>
          mavenVersionKey(version, index) === selectedVersionKey,
      )
        ? selectedVersionKey
        : urlVersion
          ? mavenVersionKey(urlVersion, group.versions.indexOf(urlVersion))
          : mavenVersionKey(latest, 0);
    const selectedKey = group.versions.some(
      (version, index) => mavenVersionKey(version, index) === preferredKey,
    )
      ? preferredKey
      : mavenVersionKey(latest, 0);
    const selectedVersion =
      group.versions.find(
        (version, index) => mavenVersionKey(version, index) === selectedKey,
      ) ?? latest;
    return {
      key: group.key,
      group,
      latest,
      expanded,
      selectedKey,
      selectedVersion,
      snippets: usageFor(
        t,
        repository.format,
        repository.name,
        selectedVersion.coordinate,
        undefined,
        {
          buildNumber: selectedVersion.buildNumber,
          createdAt: selectedVersion.createdAt,
        },
      ),
    };
  });

  const columns: ColumnsType<MavenTableRow> = [
    {
      title: t("publicBrowse.artifact"),
      dataIndex: "key",
      key: "key",
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-strong">{value}</span>
      ),
    },
    {
      title: t("publicBrowse.latestVersion"),
      key: "latest",
      width: 180,
      render: (_, row) => (
        <span className="font-mono text-xs text-fg-secondary">
          {row.latest.coordinate.split(":").slice(2).join(":")}
        </span>
      ),
    },
    {
      title: t("publicBrowse.versions2"),
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-fg-tertiary">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: t("publicBrowse.actions"),
      key: "actions",
      fixed: "right",
      width: 130,
      render: (_, row) => (
        <div className="text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() =>
              row.expanded
                ? onCollapse()
                : onExpand(row.key, mavenVersionKey(row.latest, 0))
            }
          >
            {row.expanded
              ? t("publicBrowse.collapse")
              : t("publicBrowse.selectVersion")}
          </Button>
        </div>
      ),
    },
  ];

  const expandedRowRender = (row: MavenTableRow) => {
    const href = artifactHref(
      row.selectedVersion.coordinate,
      row.selectedVersion.buildNumber,
    );
    return (
      <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
        <div>
          <label className="mb-1.5 block text-xs font-medium text-fg-tertiary">
            {t("publicBrowse.selectVersion")}{" "}
            <span className="font-normal text-fg-disabled">
              ({row.group.versions.length})
            </span>
          </label>
          <SearchableVersionSelect
            value={row.selectedKey}
            options={row.group.versions.map((version, index) => ({
              value: mavenVersionKey(version, index),
              label: `${version.coordinate.split(":").slice(2).join(":")}${
                version.buildNumber ? ` · SNAPSHOT #${version.buildNumber}` : ""
              }`,
            }))}
            onChange={(value) => onSelectVersion(row.group, value)}
            placeholder={t("publicBrowse.searchMavenVersions")}
          />
          <p className="mt-2 text-xs leading-5 text-fg-disabled">
            {t("publicBrowse.searchByVersionOrSNAPSHOTBuildNumberWithout")}
          </p>
        </div>
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs text-fg-strong">
              {row.selectedVersion.coordinate}
            </span>
            {row.selectedVersion.buildNumber ? (
              <span className="rounded bg-[var(--ag-status-warning-soft)] px-1.5 py-0.5 text-xs text-[var(--ag-status-warning)]">
                SNAPSHOT #{row.selectedVersion.buildNumber}
              </span>
            ) : null}
            <Button
              type="link"
              size="small"
              icon={<LinkOutlined />}
              href={href}
            >
              {t("publicBrowse.openVersionPage")}
            </Button>
            <Button
              type="link"
              size="small"
              onClick={() => onCopyPageLink(href)}
            >
              {copiedCoordinate === href
                ? t("publicBrowse.linkCopied")
                : t("publicBrowse.copyLink")}
            </Button>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-fg-disabled">
            <span>{formatDate(row.selectedVersion.createdAt, locale)}</span>
            <span
              className="max-w-[min(70vw,560px)] truncate font-mono text-fg-tertiary"
              title={row.selectedVersion.digest}
            >
              {row.selectedVersion.digest ?? "—"}
            </span>
            <span>{formatBytes(row.selectedVersion.size)}</span>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-line py-3 text-xs sm:grid-cols-4">
            <MetadataItem
              label={t("publicBrowse.published")}
              value={formatDate(row.selectedVersion.createdAt, locale)}
            />
            <MetadataItem
              label={t("publicBrowse.publisher")}
              value={
                row.selectedVersion.publisher ?? t("publicBrowse.notRecorded")
              }
              mono
            />
            <MetadataItem
              label={t("publicBrowse.checksum")}
              value={
                row.selectedVersion.digest ?? t("publicBrowse.notRecorded")
              }
              mono
            />
            <MetadataItem
              label={t("publicBrowse.buildType")}
              value={
                row.selectedVersion.buildNumber
                  ? `SNAPSHOT #${row.selectedVersion.buildNumber}`
                  : t("publicBrowse.release")
              }
            />
          </div>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {row.snippets.map((snippet) => (
              <UsageSnippetBlock
                key={snippet.label}
                snippet={snippet}
                copied={copiedCoordinate === snippet.code}
                onCopy={() => onCopyUsage(snippet)}
              />
            ))}
          </div>
        </div>
      </div>
    );
  };

  return (
    <ConsoleTable<MavenTableRow>
      rowKey="key"
      dataSource={tableRows}
      columns={columns}
      pagination={false}
      scroll={{ x: 720 }}
      expandable={{
        expandedRowKeys: tableRows
          .filter((row) => row.expanded)
          .map((row) => row.key),
        expandedRowRender,
        showExpandColumn: false,
      }}
    />
  );
}
