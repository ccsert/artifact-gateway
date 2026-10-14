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
  useClipboardAction,
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
  const { locale, t } = usePreferences();
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
  const { copy } = useClipboardAction();
  const visibleColumns = repositoryListColumns({
    capacities,
    isAdmin,
    setToDelete,
    onCopyId: (id) => void copy(id),
    t,
  });
  const formatOptions = isAdmin
    ? repositoryFormats(formatProfiles)
    : [...new Set(items.map((item) => item.format))];

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("repository.repositories")}
        description={t("repository.aUnifiedViewOfHostedAndProxyRepositories")}
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
            label: t("repository.repositories2"),
            value: operationalCount,
            hint:
              deletedCount > 0
                ? t("repository.archived", { deletedCount })
                : t("repository.active", { activeCount }),
          },
          {
            label: t("repository.proxyRepositories"),
            value: proxyCount,
            hint: t("repository.upstreamCachesAndMirrors"),
          },
          ...(isAdmin
            ? [
                {
                  label: t("repository.storageUsed"),
                  value: totalUsedBytes ? formatBytes(totalUsedBytes) : "—",
                  hint: Object.keys(capacities).length
                    ? t("repository.objects", {
                        number: formatNumber(
                          Object.values(capacities).reduce(
                            (sum, value) => sum + value.objectCount,
                            0,
                          ),
                          locale,
                        ),
                      })
                    : t("repository.capacityUnavailable"),
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
              {t("repository.clearFilters")}
            </Button>
          ) : undefined
        }
      >
        <FilterField label={t("repository.search")} className="min-w-[280px]">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder={t("repository.nameFormatOrRepositoryType")}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </FilterField>
        <FilterField label={t("repository.format")} className="min-w-[150px]">
          <Select<Format | "all">
            className="w-full"
            value={formatFilter}
            onChange={setFormatFilter}
            options={[
              { value: "all", label: t("repository.allFormats") },
              ...formatOptions.map((format) => ({
                value: format,
                label: format,
              })),
            ]}
          />
        </FilterField>
        <FilterField label={t("repository.status")} className="min-w-[150px]">
          <Select<RepositoryStateFilter>
            className="w-full"
            value={stateFilter}
            onChange={setStateFilter}
            options={[
              {
                value: "operational",
                label: t("repository.activeAndDeleting"),
              },
              { value: "all", label: t("repository.allStatuses") },
              { value: "active", label: t("repository.active2") },
              { value: "deleting", label: t("repository.deleting") },
              { value: "deleted", label: t("repository.deleted") },
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
                ? t("repository.noRepositories")
                : t("repository.noMatchingRepositories")
            }
            hint={
              items.length === 0
                ? isAdmin
                  ? t("repository.aRepositoryDefinesAFormatAndPolicyBoundary")
                  : t("repository.youHaveNoRepositoryGrantsYetAPlatform")
                : deletedCount > 0 && stateFilter === "operational"
                  ? t(
                      "repository.deletedRepositoriesAreArchivedAndAvailableThroughThe",
                    )
                  : t("repository.adjustTheFiltersAndTryAgain")
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
              title={t("repository.noMatchingRepositories2")}
              hint={t("repository.adjustTheFiltersOrLoadMoreRepositories")}
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
        title={t("repository.deleteRepository")}
        message={
          <>
            {t("repository.deleteRepository2")}{" "}
            <span className="font-mono text-fg-strong">{toDelete?.name}</span>{" "}
            {t("repository.readsAndWritesStopImmediatelyWhileTheRepository")}
          </>
        }
        confirmLabel={t("repository.delete2")}
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
