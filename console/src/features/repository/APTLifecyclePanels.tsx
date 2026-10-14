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
  const { t } = usePreferences();
  return (
    <ConsoleTable
      rowKey={(item) => item.snapshot.id}
      dataSource={snapshots}
      scroll={{ x: 720 }}
      pagination={{}}
      columns={[
        {
          title: t("repository.snapshot"),
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
          title: t("repository.state"),
          render: (_, item) => <StateBadge state={item.snapshot.state} />,
        },
        {
          title: t("repository.signingFingerprint"),
          render: (_, item) => (
            <code className="ag-apt-wrap">{item.snapshot.keyFingerprint}</code>
          ),
        },
        {
          title: t("repository.prunableAfter"),
          render: (_, item) => formatDate(item.prunableAfter),
        },
        {
          title: t("repository.action"),
          render: (_, item) => (
            <Popconfirm
              title={t("repository.pruneThisRetiredSnapshotSReferences")}
              description={t("repository.clientsUsingThisOldIndexCanNoLonger")}
              onConfirm={() => onPrune(item.snapshot.id)}
              okText={t("repository.pruneReferences")}
              cancelText={t("repository.cancel")}
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
                {t("repository.pruneReferences")}
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
  const { t } = usePreferences();
  return (
    <ConsoleTable
      rowKey="id"
      dataSource={deletions}
      scroll={{ x: 640 }}
      pagination={{}}
      columns={[
        {
          title: t("repository.package"),
          render: (_, item) => packageName(item.package),
        },
        {
          title: t("repository.state"),
          render: (_, item) => <StateBadge state={item.state} />,
        },
        {
          title: t("repository.recoverUntil"),
          render: (_, item) => formatDate(item.restoreUntil),
        },
        {
          title: t("repository.action"),
          render: (_, item) => (
            <Button
              disabled={
                disabled ||
                item.state !== "recoverable" ||
                Date.parse(item.restoreUntil) <= Date.now()
              }
              onClick={() => onRestore(item.id)}
            >
              {t("repository.previewRecovery")}
            </Button>
          ),
        },
      ]}
    />
  );
}
