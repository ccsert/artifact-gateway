import { DeleteOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { Repository } from "../../client";
import {
  Badge,
  FormatBadge,
  RepositoryTypeBadge,
  StateBadge,
} from "../../components/ui/Badge";
import { formatBytes } from "../../lib/format";
import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import type { RepositoryCapacity } from "../../client";

type Localize = (chinese: string, english: string) => string;

/** The repository catalog's columns; administrators also see capacity and actions. */
export function repositoryListColumns({
  capacities,
  isAdmin,
  setToDelete,
  text,
}: {
  capacities: Record<string, RepositoryCapacity>;
  isAdmin: boolean;
  setToDelete: (repository: Repository) => void;
  text: Localize;
}): ColumnsType<Repository> {
  const columns: ColumnsType<Repository> = [
    {
      title: text("名称", "Name"),
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
      title: text("类型", "Type"),
      dataIndex: "type",
      key: "type",
      width: 105,
      render: (type: Repository["type"]) => (
        <RepositoryTypeBadge type={type === "proxy" ? "proxy" : "hosted"} />
      ),
    },
    {
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 105,
      render: (format: Repository["format"]) => <FormatBadge format={format} />,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: Repository["state"]) => <StateBadge state={state} />,
    },
    {
      title: text("容量", "Capacity"),
      key: "capacity",
      width: 140,
      defaultSortOrder: "descend",
      sorter: (left, right) =>
        (capacities[left.id]?.usedBytes ?? 0) -
        (capacities[right.id]?.usedBytes ?? 0),
      render: (_value, repository) => {
        const capacity = capacities[repository.id];
        return capacity ? (
          <div>
            <div className="font-mono text-xs text-fg-secondary">
              {formatBytes(capacity.usedBytes)}
            </div>
            {capacity.quotaBytes > 0 && (
              <div className="mt-1 text-xs text-fg-disabled">
                / {formatBytes(capacity.quotaBytes)}
              </div>
            )}
          </div>
        ) : (
          <span className="text-xs text-fg-disabled">—</span>
        );
      },
    },
    {
      title: text("配置", "Configuration"),
      key: "configuration",
      width: 230,
      ellipsis: true,
      render: (_value, repository) => (
        <span
          className="font-mono text-xs text-fg-tertiary"
          title={
            repository.type === "proxy"
              ? repository.endpoint
              : `v${repository.version}`
          }
        >
          {repository.type === "proxy"
            ? repository.endpoint
            : `v${repository.version}`}
        </span>
      ),
    },
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 145,
      render: (id: string) => (
        <CopyableValue
          value={id}
          label={`${id.slice(0, 8)}…`}
          className="text-xs text-fg-tertiary"
        />
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 82,
      align: "right",
      render: (_value, repository) =>
        repository.state === "active" ? (
          <Button
            type="text"
            size="small"
            danger
            icon={<DeleteOutlined />}
            className="opacity-40 transition-opacity group-hover:opacity-100 focus:opacity-100"
            aria-label={text(
              `删除 ${repository.name}`,
              `Delete ${repository.name}`,
            )}
            onClick={() => setToDelete(repository)}
          />
        ) : repository.state === "deleting" ? (
          <Badge tone="warning">{text("删除中", "Deleting")}</Badge>
        ) : (
          <span className="text-xs text-fg-disabled">—</span>
        ),
    },
  ];
  // Readers see the catalog; capacity, configuration and actions are
  // administrative.
  return isAdmin
    ? columns
    : columns.filter(
        (column) =>
          column.key !== "capacity" &&
          column.key !== "configuration" &&
          column.key !== "actions",
      );
}
