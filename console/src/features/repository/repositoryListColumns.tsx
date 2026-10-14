import type { Translate } from "../../lib/preferences";
import { MoreOutlined } from "@ant-design/icons";
import { Button, Dropdown } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { Repository } from "../../client";
import {
  Badge,
  FormatBadge,
  RepositoryTypeBadge,
  StateBadge,
} from "../../components/ui/Badge";
import { UsageMeter } from "../../components/ui/UsageMeter";
import type { RepositoryCapacity } from "../../client";

/** The repository catalog's columns; administrators also see capacity and actions. */
export function repositoryListColumns({
  capacities,
  isAdmin,
  setToDelete,
  onCopyId,
  t,
}: {
  capacities: Record<string, RepositoryCapacity>;
  isAdmin: boolean;
  setToDelete: (repository: Repository) => void;
  onCopyId: (id: string) => void;
  t: Translate;
}): ColumnsType<Repository> {
  const columns: ColumnsType<Repository> = [
    {
      title: t("repository.name"),
      dataIndex: "name",
      key: "name",
      fixed: "left",
      width: 190,
      sorter: (left, right) => left.name.localeCompare(right.name),
      render: (name: string, repository) => (
        <Link
          to={`/repositories/${repository.id}`}
          className="font-medium text-[var(--ag-content-strong)] hover:text-[var(--ag-link-hover)]"
        >
          {name}
        </Link>
      ),
    },
    {
      title: t("repository.type"),
      dataIndex: "type",
      key: "type",
      width: 105,
      render: (type: Repository["type"]) => (
        <RepositoryTypeBadge type={type === "proxy" ? "proxy" : "hosted"} />
      ),
    },
    {
      title: t("repository.format"),
      dataIndex: "format",
      key: "format",
      width: 105,
      render: (format: Repository["format"]) => <FormatBadge format={format} />,
    },
    {
      title: t("repository.status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: Repository["state"]) => <StateBadge state={state} />,
    },
    {
      title: t("repository.capacity"),
      key: "capacity",
      width: 170,
      defaultSortOrder: "descend",
      sorter: (left, right) =>
        (capacities[left.id]?.usedBytes ?? 0) -
        (capacities[right.id]?.usedBytes ?? 0),
      render: (_value, repository) => {
        const capacity = capacities[repository.id];
        return capacity ? (
          <UsageMeter
            usedBytes={capacity.usedBytes}
            quotaBytes={capacity.quotaBytes}
          />
        ) : (
          <span className="text-xs text-fg-disabled">—</span>
        );
      },
    },
    {
      // Only a proxy has something to show here; a hosted repository's
      // resource version means nothing to the reader.
      title: t("repository.upstream"),
      key: "upstream",
      ellipsis: true,
      render: (_value, repository) =>
        repository.type === "proxy" && repository.endpoint ? (
          <span
            className="font-mono text-xs text-fg-tertiary"
            title={repository.endpoint}
          >
            {repository.endpoint}
          </span>
        ) : (
          <span className="text-xs text-fg-disabled">—</span>
        ),
    },
    {
      title: t("repository.actions"),
      key: "actions",
      fixed: "right",
      width: 82,
      align: "right",
      render: (_value, repository) =>
        repository.state === "deleting" ? (
          <Badge tone="warning">{t("repository.deleting")}</Badge>
        ) : (
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                { key: "copy-id", label: t("repository.copyRepositoryId") },
                ...(repository.state === "active"
                  ? [
                      { type: "divider" as const },
                      {
                        key: "delete",
                        danger: true,
                        label: t("repository.deleteRepositoryAction"),
                      },
                    ]
                  : []),
              ],
              onClick: ({ key }) => {
                if (key === "copy-id") onCopyId(repository.id);
                if (key === "delete") setToDelete(repository);
              },
            }}
          >
            <Button
              type="text"
              size="small"
              icon={<MoreOutlined />}
              aria-label={t("repository.moreActionsFor", {
                name: repository.name,
              })}
            />
          </Dropdown>
        ),
    },
  ];
  // Readers see the catalog; capacity, upstreams and actions are
  // administrative.
  return isAdmin
    ? columns
    : columns.filter(
        (column) =>
          column.key !== "capacity" &&
          column.key !== "upstream" &&
          column.key !== "actions",
      );
}
