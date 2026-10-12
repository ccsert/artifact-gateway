import { useState } from "react";
import { Button, Popconfirm } from "antd";
import type { ColumnsType } from "antd/es/table";
import { restoreRepositoryArtifact } from "../../client";
import type { ArtifactTombstone, Repository } from "../../client";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
  isNotFound,
} from "../../components/ui/Feedback";
import { Pagination } from "../../components/ui/Layout";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { useTombstonePages } from "./repositoryQueries";

/** Deleted artifacts that can still be restored. */
export function RepositoryTombstonesTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const tombstones = useTombstonePages(repo.id);
  const items = tombstones.data?.pages.flatMap((page) => page.items) ?? [];
  const nextToken = tombstones.hasNextPage ? "more" : undefined;
  const loading = tombstones.isPending;
  const error = tombstones.error;
  const [restoreError, setRestoreError] = useState<unknown>(null);
  const [restoreNotice, setRestoreNotice] = useState("");
  const [restoring, setRestoring] = useState<string | null>(null);

  /** Restart from the first page, or load the next one. */
  const load = (pageToken?: string) =>
    pageToken ? tombstones.fetchNextPage() : tombstones.reload();

  const restore = async (coordinate: string) => {
    setRestoring(coordinate);
    setRestoreError(null);
    setRestoreNotice("");
    const { error: err } = await restoreRepositoryArtifact({
      path: { repositoryId: repo.id },
      body: { coordinate },
    });
    setRestoring(null);
    if (err) {
      setRestoreError(err);
      return;
    }
    setRestoreNotice(text(`已恢复 ${coordinate}`, `Restored ${coordinate}`));
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("墓碑管理", "Tombstone management")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={() => load()} />
    );
  if (loading) return <Loading />;

  const tombstoneColumns: ColumnsType<ArtifactTombstone> = [
    {
      title: text("坐标", "Coordinate"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-fg"
          title={value}
        >
          {value}
        </span>
      ),
    },
    {
      title: text("摘要", "Digest"),
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("删除时间", "Deleted"),
      dataIndex: "tombstonedAt",
      key: "tombstonedAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, item) => (
        <Popconfirm
          title={text("确认恢复此制品？", "Restore this artifact?")}
          description={text(
            "恢复后制品会重新出现在仓库浏览与协议读取中。",
            "After restoration, the artifact is available again in repository browsing and protocol reads.",
          )}
          okText={text("恢复", "Restore")}
          cancelText={text("取消", "Cancel")}
          onConfirm={() => restore(item.coordinate)}
        >
          <Button size="small" loading={restoring === item.coordinate}>
            {text("恢复", "Restore")}
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      {restoreError !== null && <ErrorBanner error={restoreError} />}
      {restoreNotice && (
        <Notice
          tone="success"
          title={restoreNotice}
          onClose={() => setRestoreNotice("")}
        />
      )}
      {items.length === 0 ? (
        <EmptyState
          title={text("暂无墓碑", "No tombstones")}
          hint={text(
            "被删除的制品会保留墓碑记录，可在此恢复",
            "Deleted artifacts retain tombstone records and can be restored here.",
          )}
        />
      ) : (
        <>
          <ConsoleTable<ArtifactTombstone>
            rowKey={(item) =>
              `${item.coordinate}:${item.digest}:${item.tombstonedAt}`
            }
            dataSource={items}
            columns={tombstoneColumns}
            pagination={false}
            scroll={{ x: 830 }}
          />
          <Pagination hasMore={!!nextToken} onMore={() => load(nextToken)} />
        </>
      )}
    </div>
  );
}
