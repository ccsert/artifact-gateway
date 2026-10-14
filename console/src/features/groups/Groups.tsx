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
  const { t } = usePreferences();
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
    return invalidateGroups();
  };
  const retry = () => Promise.allSettled([load(), profilesQuery.refetch()]);

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
    t,
  });

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("groups.groups")}
        description={t("groups.aggregateRepositoriesOfTheSameFormatBehindOne")}
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
              title={t("groups.groupsAreUnavailable")}
              hint={t("groups.theCurrentBackendDoesNotExposeTheGroups")}
            />
          </Card>
        ) : (
          <ErrorBanner error={error} onRetry={retry} />
        )
      ) : loading && groups.length === 0 ? (
        <Loading />
      ) : groups.length === 0 ? (
        <Card>
          <EmptyState
            title={t("groups.noGroups")}
            hint={t("groups.createAGroupToAggregateRepositories")}
          />
        </Card>
      ) : (
        <>
          {error !== null ? (
            <ErrorBanner error={error} onRetry={retry} />
          ) : null}
          <MetricStrip
            items={[
              {
                label: t("groups.groups2"),
                value: groups.length,
                hint: t("groups.memberReferences", { memberCount }),
              },
              {
                label: t("groups.anonymousReadable"),
                value: publicGroups,
                hint: publicGroups
                  ? t("groups.memberRepositoriesMustAllowAnonymousReads")
                  : t("groups.allEntryPointsArePrivate"),
                tone: publicGroups ? "success" : "default",
              },
              {
                label: t("groups.formats"),
                value: new Set(groups.map((group) => group.format)).size,
                hint: t("groups.resolvedByMatchingFormat"),
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
                    {t("groups.clearFilters")}
                  </Button>
                ) : undefined
              }
            >
              <FilterField label={t("groups.search")} className="min-w-[260px]">
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={t("groups.searchGroupNames")}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField label={t("groups.format")} className="min-w-[150px]">
                <Select<Format | "all">
                  className="w-full"
                  value={formatFilter}
                  onChange={setFormatFilter}
                  options={[
                    { value: "all", label: t("groups.allFormats") },
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
                title={t("groups.noMatchingGroups")}
                hint={t("groups.adjustTheFiltersAndTryAgain")}
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
        title={t("groups.deleteGroup2")}
        message={
          <>
            {t("groups.deleteGroup3")}{" "}
            <span className="font-mono text-fg-strong">{toDelete?.name}</span>{" "}
            {t("groups.memberRepositoriesAreNotDeleted")}
          </>
        }
        confirmLabel={t("groups.delete")}
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
