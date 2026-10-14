import {
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LinkOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { usageFor } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import { conanReferenceParts } from "../../lib/publicBrowseModel";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { ConanRevisionPanel } from "./ConanRevisionPanel";
import type { ConanGroupTableProps, ConanTableRow } from "./conanGroupTypes";

export type { ConanRevision, ConanRevisionPage } from "./conanGroupTypes";

export function ConanGroupTable(props: ConanGroupTableProps) {
  const {
    groups,
    repository,
    artifactParam,
    revisionParam,
    expandedGroup,
    selectedReferences,
    selectedRevisions,
    revisionPages,
    versionFilter,
    copiedCoordinate,
    onExpand,
    onCollapse,
    onFilterChange,
    onLoadRevisions,
    onOpenArtifact,
    onClearArtifactParams,
    artifactHref,
    onCopyCoordinate,
  } = props;
  const { t } = usePreferences();
  const tableRows: ConanTableRow[] = groups.map((group) => {
    const latest = group.versions[0];
    const urlReference = group.versions.some(
      (version) => version.coordinate === artifactParam,
    )
      ? artifactParam
      : "";
    const selectedReference =
      urlReference || selectedReferences[group.key] || latest.coordinate;
    const expanded = expandedGroup === group.key || Boolean(urlReference);
    const page = revisionPages[selectedReference];
    const revisions = page?.items ?? [];
    const normalizedFilter = versionFilter.trim().toLowerCase();
    const visibleRevisions = normalizedFilter
      ? revisions.filter((revision) =>
          `${revision.revision} ${revision.digest ?? ""} ${revision.createdAt ?? ""}`
            .toLowerCase()
            .includes(normalizedFilter),
        )
      : revisions;
    const selectedRevision = selectedRevisions[selectedReference];
    const requestedRevision =
      artifactParam === selectedReference ? revisionParam : "";
    const preferredRevision =
      (selectedRevision &&
        revisions.some((revision) => revision.revision === selectedRevision) &&
        selectedRevision) ||
      (requestedRevision &&
        revisions.some((revision) => revision.revision === requestedRevision) &&
        requestedRevision) ||
      revisions[0]?.revision ||
      "";
    const selectedRevisionValue = visibleRevisions.some(
      (revision) => revision.revision === preferredRevision,
    )
      ? preferredRevision
      : visibleRevisions[0]?.revision || preferredRevision;
    const selectedRevisionItem = revisions.find(
      (revision) => revision.revision === selectedRevisionValue,
    );
    return {
      key: group.key,
      group,
      latest,
      selectedReference,
      expanded,
      page,
      revisions,
      visibleRevisions,
      selectedRevisionValue,
      selectedRevisionItem,
      referenceVersion: conanReferenceParts(selectedReference).version,
      versionHref: selectedRevisionValue
        ? artifactHref(selectedReference, selectedRevisionValue)
        : artifactHref(selectedReference),
      snippets: usageFor(
        t,
        repository.format,
        repository.name,
        selectedReference,
      ),
    };
  });

  const toggleRow = (row: ConanTableRow) => {
    if (row.expanded) {
      onCollapse();
      onClearArtifactParams();
      return;
    }
    onExpand(row.key, row.selectedReference);
    onFilterChange("");
    if (!revisionPages[row.selectedReference])
      onLoadRevisions(row.selectedReference);
    onOpenArtifact(
      row.selectedReference,
      row.selectedRevisionValue || undefined,
    );
  };

  const columns: ColumnsType<ConanTableRow> = [
    {
      title: t("publicBrowse.conanPackage"),
      dataIndex: "key",
      key: "key",
      width: 260,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-strong">{value}</span>
      ),
    },
    {
      title: t("publicBrowse.latestVersion"),
      key: "latest",
      width: 160,
      render: (_, row) => (
        <span className="font-mono text-xs text-fg-secondary">
          {conanReferenceParts(row.latest.coordinate).version}
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
      title: t("publicBrowse.currentRevision"),
      key: "revision",
      width: 240,
      render: (_, row) => (
        <span
          className="block max-w-[220px] truncate font-mono text-xs text-fg-tertiary"
          title={row.selectedRevisionItem?.revision}
        >
          {row.selectedRevisionItem?.revision ??
            (row.expanded
              ? t("publicBrowse.loading")
              : t("publicBrowse.loadWhenExpanded"))}
        </span>
      ),
    },
    {
      title: t("publicBrowse.actions"),
      key: "actions",
      fixed: "right",
      width: 270,
      render: (_, row) => (
        <div className="whitespace-nowrap text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => toggleRow(row)}
          >
            {row.expanded
              ? t("publicBrowse.collapse")
              : t("publicBrowse.selectVersion")}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={row.versionHref}
          >
            {row.selectedRevisionValue
              ? t("publicBrowse.openVersion")
              : t("publicBrowse.open")}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.key
                ? t("publicBrowse.copied")
                : t("publicBrowse.copyConanPackageIdentifier")
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={`${t("publicBrowse.copy2")} ${row.key}`}
              icon={
                copiedCoordinate === row.key ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )
              }
              onClick={() => onCopyCoordinate(row.key)}
            />
          </Tooltip>
        </div>
      ),
    },
  ];

  const expandedRowRender = (row: ConanTableRow) => (
    <ConanRevisionPanel row={row} table={props} />
  );

  return (
    <ConsoleTable<ConanTableRow>
      rowKey="key"
      dataSource={tableRows}
      columns={columns}
      pagination={false}
      scroll={{ x: 1030 }}
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
