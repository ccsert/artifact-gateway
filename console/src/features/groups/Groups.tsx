import { useState } from "react";
import { SearchOutlined } from "@ant-design/icons";
import { Button, Input, Select } from "antd";
import { deleteGroup } from "../../client";
import type { Group, Format } from "../../client";
import { PageHeader, Card, Pagination } from "../../components/ui/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../../components/ui/Feedback";
import { ConfirmDialog } from "../../components/ui/Modal";
import {
  ConsoleTable,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { groupFormats } from "../../lib/formatProfiles";

import { CreateGroupDialog } from "./CreateGroupDialog";
import { useAccessRepositories } from "../access-control/accessQueries";
import { useFormatProfiles } from "../repository/repositoryQueries";
import { useGroupPages, useInvalidateGroups } from "./groupQueries";
import { groupColumns } from "./groupColumns";

export function GroupsPage() {
  const { text } = usePreferences();
  const groupPages = useGroupPages();
  // Member names come from the repository catalog; a failed read only
  // leaves ids in their place, as before.
  const reposQuery = useAccessRepositories();
  const profilesQuery = useFormatProfiles(true);
  const invalidateGroups = useInvalidateGroups();
  const groups = groupPages.data?.pages.flatMap((page) => page.items) ?? [];
  const repos = reposQuery.data?.items ?? [];
  const formatProfiles = profilesQuery.data ?? [];
  const nextToken = groupPages.hasNextPage ? "more" : undefined;
  const loading = groupPages.isPending || profilesQuery.isPending;
  const loadingMore = groupPages.isFetchingNextPage;
  const [mutationError, setError] = useState<unknown>(null);
  const error = mutationError ?? groupPages.error ?? profilesQuery.error;
  const [toDelete, setToDelete] = useState<Group | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [filter, setFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState<Format | "all">("all");

  const repoName = (id: string) =>
    repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + "…";
  const repoById = (id: string) => repos.find((r) => r.id === id);

  const load = () => {
    setError(null);
    void invalidateGroups();
  };

  const loadMore = () => {
    if (nextToken && !loadingMore) void groupPages.fetchNextPage();
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    try {
      const { error: err } = await deleteGroup({
        path: { groupId: toDelete.id },
      });
      if (!err) {
        setToDelete(null);
        void load();
      } else {
        setError(err);
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setDeleting(false);
    }
  };

  const visibleGroups = groups.filter(
    (group) =>
      (!filter || group.name.toLowerCase().includes(filter.toLowerCase())) &&
      (formatFilter === "all" || group.format === formatFilter),
  );
  const memberCount = groups.reduce(
    (total, group) => total + (group.members?.length ?? 0),
    0,
  );
  const publicGroups = groups.filter((group) => group.anonymousRead).length;
  const columns = groupColumns({
    repos,
    repoName,
    repoById,
    onSaved: load,
    onDelete: setToDelete,
    text,
  });

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("分组", "Groups")}
        description={text(
          "将多个同格式仓库聚合为统一入口",
          "Aggregate repositories of the same format behind one entry point",
        )}
        actions={
          <CreateGroupDialog
            repos={repos}
            profiles={formatProfiles}
            onCreated={load}
          />
        }
      />
      {error !== null && groups.length === 0 ? (
        isNotFound(error) ? (
          <Card>
            <EmptyState
              title={text("分组功能未启用", "Groups are unavailable")}
              hint={text(
                "当前后端构建尚未挂载分组管理端点（返回 404）",
                "The current backend does not expose the groups endpoint (404)",
              )}
            />
          </Card>
        ) : (
          <ErrorBanner error={error} onRetry={load} />
        )
      ) : loading && groups.length === 0 ? (
        <Loading />
      ) : groups.length === 0 ? (
        <Card>
          <EmptyState
            title={text("暂无分组", "No groups")}
            hint={text(
              "创建分组以聚合多个仓库",
              "Create a group to aggregate repositories",
            )}
          />
        </Card>
      ) : (
        <>
          {error !== null ? <ErrorBanner error={error} onRetry={load} /> : null}
          <MetricStrip
            items={[
              {
                label: text("分组总数", "Groups"),
                value: groups.length,
                hint: text(
                  `${memberCount} 个成员引用`,
                  `${memberCount} member references`,
                ),
              },
              {
                label: text("匿名可读", "Anonymous readable"),
                value: publicGroups,
                hint: publicGroups
                  ? text(
                      "仍需成员仓库允许匿名读取",
                      "Member repositories must allow anonymous reads",
                    )
                  : text("全部为私有入口", "All entry points are private"),
                tone: publicGroups ? "success" : "default",
              },
              {
                label: text("覆盖格式", "Formats"),
                value: new Set(groups.map((group) => group.format)).size,
                hint: text("按同格式仓库解析", "Resolved by matching format"),
              },
            ]}
          />
          <Card className="ag-page-primary">
            <FilterBar
              className="border-x-0 border-t-0 rounded-none"
              actions={
                filter || formatFilter !== "all" ? (
                  <Button
                    type="text"
                    onClick={() => {
                      setFilter("");
                      setFormatFilter("all");
                    }}
                  >
                    {text("清除筛选", "Clear filters")}
                  </Button>
                ) : undefined
              }
            >
              <FilterField
                label={text("搜索", "Search")}
                className="min-w-[260px]"
              >
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={text("搜索分组名称…", "Search group names…")}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField
                label={text("格式", "Format")}
                className="min-w-[150px]"
              >
                <Select<Format | "all">
                  className="w-full"
                  value={formatFilter}
                  onChange={setFormatFilter}
                  options={[
                    { value: "all", label: text("全部格式", "All formats") },
                    ...groupFormats(formatProfiles).map((format) => ({
                      value: format,
                      label: format,
                    })),
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleGroups.length === 0 ? (
              <EmptyState
                title={text("没有匹配的分组", "No matching groups")}
                hint={text(
                  "调整筛选条件后重试",
                  "Adjust the filters and try again",
                )}
              />
            ) : (
              <ConsoleTable<Group>
                rowKey="id"
                dataSource={visibleGroups}
                columns={columns}
                pagination={false}
                scroll={{ x: 1200 }}
              />
            )}
            <Pagination
              hasMore={!!nextToken}
              loading={loadingMore}
              onMore={loadMore}
            />
          </Card>
        </>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title={text("删除分组", "Delete group")}
        message={
          <>
            {text("确定删除分组", "Delete group")}{" "}
            <span className="font-mono text-fg-strong">{toDelete?.name}</span>{" "}
            {text(
              "吗？成员仓库本身不会被删除。",
              "? Member repositories are not deleted.",
            )}
          </>
        }
        confirmLabel={text("删除", "Delete")}
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
