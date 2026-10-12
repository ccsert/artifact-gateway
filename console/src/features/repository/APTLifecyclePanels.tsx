import type { AptLifecycleState } from "../../client";
import { Button, Popconfirm } from "antd";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { packageName } from "./aptModel";

/** The suite's snapshots; retired ones can have their references pruned. */
export function APTSnapshotHistory({
  snapshots,
  disabled,
  onPrune,
}: {
  snapshots: AptLifecycleState["snapshots"];
  disabled: boolean;
  onPrune: (snapshotId: string) => void;
}) {
  const { text } = usePreferences();
  return (
    <ConsoleTable
      rowKey={(item) => item.snapshot.id}
      dataSource={snapshots}
      scroll={{ x: 720 }}
      pagination={{}}
      columns={[
        {
          title: text("快照", "Snapshot"),
          render: (_, item) => (
            <div>
              #{item.snapshot.sequence}
              <div className="ag-apt-identity">
                <code>{item.snapshot.id}</code>
              </div>
            </div>
          ),
        },
        {
          title: text("状态", "State"),
          render: (_, item) => <StateBadge state={item.snapshot.state} />,
        },
        {
          title: text("签名指纹", "Signing fingerprint"),
          render: (_, item) => (
            <code className="ag-apt-wrap">{item.snapshot.keyFingerprint}</code>
          ),
        },
        {
          title: text("最早清理时间", "Prunable after"),
          render: (_, item) => formatDate(item.prunableAfter),
        },
        {
          title: text("操作", "Action"),
          render: (_, item) => (
            <Popconfirm
              title={text(
                "清理此退役快照的引用？",
                "Prune this retired snapshot's references?",
              )}
              description={text(
                "使用此旧索引的客户端将无法继续读取。共享对象仍受其他引用保护。",
                "Clients using this old index can no longer read it. Other references still protect shared objects.",
              )}
              onConfirm={() => onPrune(item.snapshot.id)}
              okText={text("清理引用", "Prune references")}
              cancelText={text("取消", "Cancel")}
            >
              <Button
                danger
                disabled={
                  disabled ||
                  item.snapshot.state !== "retired" ||
                  !item.prunableAfter ||
                  Date.parse(item.prunableAfter) > Date.now()
                }
              >
                {text("清理引用", "Prune references")}
              </Button>
            </Popconfirm>
          ),
        },
      ]}
    />
  );
}

/** Deleted packages that can still be restored into a new snapshot. */
export function APTRecoveryList({
  deletions,
  disabled,
  onRestore,
}: {
  deletions: AptLifecycleState["deletions"];
  disabled: boolean;
  onRestore: (deletionId: string) => void;
}) {
  const { text } = usePreferences();
  return (
    <ConsoleTable
      rowKey="id"
      dataSource={deletions}
      scroll={{ x: 640 }}
      pagination={{}}
      columns={[
        {
          title: text("软件包", "Package"),
          render: (_, item) => packageName(item.package),
        },
        {
          title: text("状态", "State"),
          render: (_, item) => <StateBadge state={item.state} />,
        },
        {
          title: text("恢复截止", "Recover until"),
          render: (_, item) => formatDate(item.restoreUntil),
        },
        {
          title: text("操作", "Action"),
          render: (_, item) => (
            <Button
              disabled={
                disabled ||
                item.state !== "recoverable" ||
                Date.parse(item.restoreUntil) <= Date.now()
              }
              onClick={() => onRestore(item.id)}
            >
              {text("预览恢复", "Preview recovery")}
            </Button>
          ),
        },
      ]}
    />
  );
}
