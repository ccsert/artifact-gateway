import { DatabaseOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { Group, GroupCapacityMember } from "../../client";
import { Loading, ErrorBanner } from "../../components/ui/Feedback";
import { RepositoryTypeBadge } from "../../components/ui/Badge";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useGroupCapacity } from "./groupQueries";

/** A group's capacity across its member repositories. */
export function CapacityDialog({ group }: { group: Group }) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const capacityQuery = useGroupCapacity(group.id, dialog.open);
  const capacity = capacityQuery.data ?? null;
  const error = capacityQuery.error;
  const formatBytes = (value: number) =>
    value < 1024 ? `${value} B` : `${(value / 1024 / 1024).toFixed(1)} MB`;
  const columns: ColumnsType<GroupCapacityMember> = [
    {
      title: text("位置", "Position"),
      dataIndex: "position",
      key: "position",
      width: 70,
      render: (position: number) => (
        <span className="text-fg-tertiary">{position + 1}</span>
      ),
    },
    {
      title: text("成员", "Member"),
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
      title: text("类型", "Type"),
      dataIndex: "type",
      key: "type",
      width: 110,
      render: (type: GroupCapacityMember["type"]) => (
        <RepositoryTypeBadge type={type} />
      ),
    },
    {
      title: text("已用", "Used"),
      dataIndex: "usedBytes",
      key: "usedBytes",
      width: 120,
      render: (value: number) => (
        <span className="text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("对象", "Objects"),
      dataIndex: "objectCount",
      key: "objectCount",
      width: 90,
      render: (value: number) => (
        <span className="text-fg-secondary">{value}</span>
      ),
    },
    {
      title: text("配额", "Quota"),
      dataIndex: "quotaBytes",
      key: "quotaBytes",
      width: 120,
      render: (value: number | undefined) => (
        <span className="text-fg-tertiary">
          {value ? formatBytes(value) : text("无限制", "Unlimited")}
        </span>
      ),
    },
  ];
  return (
    <>
      <Button
        size="small"
        type="text"
        icon={<DatabaseOutlined />}
        onClick={dialog.show}
      >
        {text("容量", "Capacity")}
      </Button>
      <Modal
        open={dialog.open}
        onClose={dialog.hide}
        wide
        title={text(`容量贡献 · ${group.name}`, `Capacity · ${group.name}`)}
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
