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
  const { t } = usePreferences();
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
    setRestoreNotice(t("repository.restored", { coordinate }));
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={t("repository.tombstoneManagement")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={() => load()} />
    );
  if (loading) return <Loading />;

  const tombstoneColumns: ColumnsType<ArtifactTombstone> = [
    {
      title: t("repository.coordinate"),
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
      title: t("repository.digest"),
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
      title: t("repository.deleted2"),
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
      title: t("repository.actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, item) => (
        <Popconfirm
          title={t("repository.restoreThisArtifact")}
          description={t(
            "repository.afterRestorationTheArtifactIsAvailableAgainIn",
          )}
          okText={t("repository.restore")}
          cancelText={t("repository.cancel")}
          onConfirm={() => restore(item.coordinate)}
        >
          <Button size="small" loading={restoring === item.coordinate}>
            {t("repository.restore")}
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
          title={t("repository.noTombstones")}
          hint={t("repository.deletedArtifactsRetainTombstoneRecordsAndCanBe")}
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
