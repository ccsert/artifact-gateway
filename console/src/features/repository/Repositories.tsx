import { useState } from "react";
import {
  ClearOutlined,
  DatabaseOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Input, Select } from "antd";
import { deleteRepository } from "../../client";
import type { Repository, Format, FormatProfile } from "../../client";
import { PageHeader, Card, Pagination } from "../../components/ui/Layout";
import { Loading, ErrorBanner, EmptyState } from "../../components/ui/Feedback";
import { ConfirmDialog } from "../../components/ui/Modal";
import { formatBytes, formatNumber } from "../../lib/format";
import {
  ConsoleTable,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import {
  useFormatProfiles,
  useInvalidateRepositories,
  useRepositoryCapacities,
  useRepositoryPages,
} from "./repositoryQueries";
import { CreateRepositoryDialog } from "./CreateRepositoryDialog";
import { repositoryListColumns } from "./repositoryListColumns";
import { repositoryFormats } from "../../lib/formatProfiles";

export function RepositoriesPage() {
  const { locale, text } = usePreferences();
  const { identity } = useAuth();
  const isAdmin = platformCapabilities(identity).platformAdmin;
  const repositoryPages = useRepositoryPages();
  const capacityQuery = useRepositoryCapacities(isAdmin);
  const profilesQuery = useFormatProfiles(isAdmin);
  const invalidateRepositories = useInvalidateRepositories();
  const [mutationError, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState<Format | "all">("all");
  type RepositoryStateFilter = Repository["state"] | "all" | "operational";
  const [stateFilter, setStateFilter] =
    useState<RepositoryStateFilter>("operational");
  const [toDelete, setToDelete] = useState<Repository | null>(null);
  const [deleting, setDeleting] = useState(false);

  const items: Repository[] =
    repositoryPages.data?.pages.flatMap((page) => page.items) ?? [];
  const formatProfiles: FormatProfile[] = profilesQuery.data ?? [];
  // Capacity is decoration on the catalog; its failure never blocks the list.
  const capacities = Object.fromEntries(
    (capacityQuery.data ?? []).map((capacity) => [
      capacity.repositoryId,
      capacity,
    ]),
  );
  const loading =
    repositoryPages.isPending ||
    (isAdmin && (capacityQuery.isPending || profilesQuery.isPending));
  const loadingMore = repositoryPages.isFetchingNextPage;
  const nextToken = repositoryPages.hasNextPage ? "more" : undefined;
  const error =
    mutationError ??
    repositoryPages.error ??
    (isAdmin ? profilesQuery.error : null) ??
    null;
  const load = () => {
    setError(null);
    void invalidateRepositories();
    if (profilesQuery.error) void profilesQuery.refetch();
  };
  const loadMore = () => void repositoryPages.fetchNextPage();

  const confirmDelete = async () => {
    if (!toDelete) return;
    if (toDelete.state !== "active") {
      setToDelete(null);
      return;
    }
    setDeleting(true);
    setError(null);
    try {
      const { error: err } = await deleteRepository({
        path: { repositoryId: toDelete.id },
      });
      if (!err) {
        setToDelete(null);
        void invalidateRepositories();
      } else {
        setError(err);
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setDeleting(false);
    }
  };

  const q = filter.toLowerCase();
  const visible = items
    .filter(
      (r) =>
        !q ||
        r.name.toLowerCase().includes(q) ||
        r.format.includes(q) ||
        (r.type ?? "hosted").includes(q),
    )
    .filter((r) => formatFilter === "all" || r.format === formatFilter)
    .filter((r) =>
      stateFilter === "operational"
        ? r.state !== "deleted"
        : stateFilter === "all" || r.state === stateFilter,
    );

  const activeCount = items.filter((r) => r.state === "active").length;
  const operationalCount = items.filter((r) => r.state !== "deleted").length;
  const proxyCount = items.filter(
    (r) => r.type === "proxy" && r.state !== "deleted",
  ).length;
  const deletedCount = items.filter((r) => r.state === "deleted").length;
  const totalUsedBytes = Object.values(capacities).reduce(
    (sum, value) => sum + value.usedBytes,
    0,
  );
  const visibleColumns = repositoryListColumns({
    capacities,
    isAdmin,
    setToDelete,
    text,
  });
  const formatOptions = isAdmin
    ? repositoryFormats(formatProfiles)
    : [...new Set(items.map((item) => item.format))];

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("仓库", "Repositories")}
        description={text(
          "Hosted 与 Proxy Repository 的统一视图",
          "A unified view of hosted and proxy repositories",
        )}
        actions={
          isAdmin ? (
            <CreateRepositoryDialog
              profiles={formatProfiles}
              onCreated={load}
            />
          ) : undefined
        }
      />
      <MetricStrip
        items={[
          {
            label: text("仓库总数", "Repositories"),
            value: operationalCount,
            hint:
              deletedCount > 0
                ? text(`${deletedCount} 个已归档`, `${deletedCount} archived`)
                : text(`${activeCount} 个活跃`, `${activeCount} active`),
          },
          {
            label: text("代理仓库", "Proxy repositories"),
            value: proxyCount,
            hint: text("上游缓存与镜像", "Upstream caches and mirrors"),
          },
          ...(isAdmin
            ? [
                {
                  label: text("当前占用", "Storage used"),
                  value: totalUsedBytes ? formatBytes(totalUsedBytes) : "—",
                  hint: Object.keys(capacities).length
                    ? text(
                        `${formatNumber(
                          Object.values(capacities).reduce(
                            (sum, value) => sum + value.objectCount,
                            0,
                          ),
                          locale,
                        )} 个对象`,
                        `${formatNumber(
                          Object.values(capacities).reduce(
                            (sum, value) => sum + value.objectCount,
                            0,
                          ),
                          locale,
                        )} objects`,
                      )
                    : text("容量未启用", "Capacity unavailable"),
                },
              ]
            : []),
        ]}
      />
      <FilterBar
        actions={
          filter || formatFilter !== "all" || stateFilter !== "operational" ? (
            <Button
              type="text"
              icon={<ClearOutlined />}
              onClick={() => {
                setFilter("");
                setFormatFilter("all");
                setStateFilter("operational");
              }}
            >
              {text("清除筛选", "Clear filters")}
            </Button>
          ) : undefined
        }
      >
        <FilterField label={text("搜索", "Search")} className="min-w-[280px]">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder={text(
              "名称、格式或仓库类型…",
              "Name, format, or repository type…",
            )}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </FilterField>
        <FilterField label={text("格式", "Format")} className="min-w-[150px]">
          <Select<Format | "all">
            className="w-full"
            value={formatFilter}
            onChange={setFormatFilter}
            options={[
              { value: "all", label: text("全部格式", "All formats") },
              ...formatOptions.map((format) => ({
                value: format,
                label: format,
              })),
            ]}
          />
        </FilterField>
        <FilterField label={text("状态", "Status")} className="min-w-[150px]">
          <Select<RepositoryStateFilter>
            className="w-full"
            value={stateFilter}
            onChange={setStateFilter}
            options={[
              {
                value: "operational",
                label: text("运行中与删除中", "Active and deleting"),
              },
              { value: "all", label: text("全部状态", "All statuses") },
              { value: "active", label: text("运行中", "Active") },
              { value: "deleting", label: text("删除中", "Deleting") },
              { value: "deleted", label: text("已删除", "Deleted") },
            ]}
          />
        </FilterField>
      </FilterBar>
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : loading ? (
        <Loading />
      ) : visible.length === 0 ? (
        <Card>
          <EmptyState
            icon={
              items.length === 0 ? <DatabaseOutlined /> : <SearchOutlined />
            }
            title={
              items.length === 0
                ? text("暂无仓库", "No repositories")
                : text("暂无符合条件的仓库", "No matching repositories")
            }
            hint={
              items.length === 0
                ? isAdmin
                  ? text(
                      "仓库是制品格式与策略的边界；创建后即可发布、代理和治理制品。",
                      "A repository defines a format and policy boundary for publishing, proxying, and governing artifacts.",
                    )
                  : text(
                      "你还没有任何仓库授权。仓库权限由平台管理员按仓库分配；请联系管理员为你的账号授权。",
                      "You have no repository grants yet. A platform administrator assigns access repository by repository; ask one to grant yours.",
                    )
                : deletedCount > 0 && stateFilter === "operational"
                  ? text(
                      "已删除仓库已归档，可在状态筛选中查看",
                      "Deleted repositories are archived and available through the status filter",
                    )
                  : text(
                      "调整筛选条件后重试",
                      "Adjust the filters and try again",
                    )
            }
            action={
              isAdmin && items.length === 0 ? (
                <CreateRepositoryDialog
                  profiles={formatProfiles}
                  onCreated={load}
                />
              ) : undefined
            }
          />
        </Card>
      ) : (
        <Card>
          {visible.length === 0 ? (
            <EmptyState
              icon={<SearchOutlined />}
              title={text("没有匹配的仓库", "No matching repositories")}
              hint={text(
                "调整筛选条件，或继续加载更多仓库",
                "Adjust the filters or load more repositories",
              )}
            />
          ) : (
            <ConsoleTable<Repository>
              rowKey="id"
              rowClassName={() => "group"}
              dataSource={visible}
              columns={visibleColumns}
              pagination={false}
              scroll={{ x: 1100 }}
            />
          )}
          <Pagination
            hasMore={!!nextToken}
            loading={loadingMore}
            onMore={loadMore}
          />
        </Card>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title={text("删除仓库", "Delete repository")}
        message={
          <>
            {text("确定要删除仓库", "Delete repository")}{" "}
            <span className="font-mono text-fg-strong">{toDelete?.name}</span>{" "}
            {text(
              "吗？仓库会立即停止读写并进入 deleting 状态，后台通常在一分钟内完成处理并标记为已删除。此操作不可撤销，审计元数据会保留。",
              "? Reads and writes stop immediately while the repository enters the deleting state. Background cleanup normally completes within one minute. This cannot be undone; audit metadata is retained.",
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
