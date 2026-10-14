import type { ColumnsType } from "antd/es/table";
import type { Group, GroupCapacityMember } from "../../client";
import { Loading, ErrorBanner } from "../../components/ui/Feedback";
import { RepositoryTypeBadge } from "../../components/ui/Badge";
import { Modal } from "../../components/ui/Modal";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useGroupCapacity } from "./groupQueries";

/** A group's capacity across its member repositories. */
export function CapacityDialog({
  group,
  open,
  onClose,
  afterClose,
}: {
  group: Group;
  open: boolean;
  onClose: () => void;
  afterClose: () => void;
}) {
  const { t } = usePreferences();
  const capacityQuery = useGroupCapacity(group.id, open);
  const capacity = capacityQuery.data ?? null;
  const error = capacityQuery.error;
  const formatBytes = (value: number) =>
    value < 1024 ? `${value} B` : `${(value / 1024 / 1024).toFixed(1)} MB`;
  const columns: ColumnsType<GroupCapacityMember> = [
    {
      title: t("groups.position"),
      dataIndex: "position",
      key: "position",
      width: 70,
      render: (position: number) => (
        <span className="text-fg-tertiary">{position + 1}</span>
      ),
    },
    {
      title: t("groups.member"),
      dataIndex: "repositoryId",
      key: "repositoryId",
      width: 180,
      render: (id: string) => (
        <span className="font-mono text-xs text-fg-secondary">
          {id.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: t("groups.type"),
      dataIndex: "type",
      key: "type",
      width: 110,
      render: (type: GroupCapacityMember["type"]) => (
        <RepositoryTypeBadge type={type} />
      ),
    },
    {
      title: t("groups.used"),
      dataIndex: "usedBytes",
      key: "usedBytes",
      width: 120,
      render: (value: number) => (
        <span className="text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: t("groups.objects"),
      dataIndex: "objectCount",
      key: "objectCount",
      width: 90,
      render: (value: number) => (
        <span className="text-fg-secondary">{value}</span>
      ),
    },
    {
      title: t("groups.quota"),
      dataIndex: "quotaBytes",
      key: "quotaBytes",
      width: 120,
      render: (value: number | undefined) => (
        <span className="text-fg-tertiary">
          {value ? formatBytes(value) : t("groups.unlimited")}
        </span>
      ),
    },
  ];
  return (
    <>
      <Modal
        open={open}
        onClose={onClose}
        afterClose={afterClose}
        wide
        title={t("groups.capacity2", { name: group.name })}
      >
        {error !== null && <ErrorBanner error={error} />}
        {!capacity ? (
          <Loading />
        ) : (
          <ConsoleTable<GroupCapacityMember>
            rowKey="repositoryId"
            size="small"
            dataSource={capacity.members}
            columns={columns}
            pagination={false}
          />
        )}
      </Modal>
    </>
  );
}
