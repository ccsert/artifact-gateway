import { useState } from "react";
import { SearchOutlined } from "@ant-design/icons";
import { Input, Select } from "antd";
import { deleteGroup } from "../../client";
import type { Group, Format } from "../../client";
import { Pagination } from "../../components/ui/Layout";
import { EmptyState } from "../../components/ui/Feedback";
import { FilterCard, ListPage, ListStates } from "../../components/ui/ListPage";
import { ConfirmDanger } from "../../components/ui/Modal";
import { useUrlFilters } from "../../components/ui/useUrlFilters";
import { unwrap } from "../../lib/query/client";
import {
  ConsoleTable,
  FilterField,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { groupFormats } from "../../lib/formatProfiles";

import { CreateGroupDialog } from "./CreateGroupDialog";
import { useAccessRepositories } from "../access-control/accessQueries";
import { useFormatProfiles } from "../repository/repositoryQueries";
import { useGroupPages, useInvalidateGroups } from "./groupQueries";
import { groupColumns, type GroupAction } from "./groupColumns";
import { CapacityDialog } from "./GroupCapacityDialog";
import { MembersDialog } from "./GroupMembersDialog";
import { GroupResolutionDialog } from "./pages/GroupResolutionDialog";
import { RenameGroupDialog } from "./RenameGroupDialog";

const GROUP_FILTERS = { q: "", format: "all" as Format | "all" };

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
  const filters = useUrlFilters(GROUP_FILTERS);
  const { q: filter, format: formatFilter } = filters.values;

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
  // Closing a dialog returns focus to what opened it: the row's button, or
  // its More actions button when the dialog came from the row menu.
  const [action, setAction] = useState<{
    key: number;
    open: boolean;
    kind: GroupAction;
    group: Group;
    returnTo: HTMLElement | null;
  } | null>(null);
  const [deleteReturnTo, setDeleteReturnTo] = useState<HTMLElement | null>(
    null,
  );
  const closeAction = () => {
    setAction((current) => current && { ...current, open: false });
  };
  const afterActionClose = () => {
    if (!action) return;
    action.returnTo?.focus();
    setAction((current) => (current?.key === action.key ? null : current));
  };
  const columns = groupColumns({
    repoName,
    repoById,
    onAction: (kind, group) => {
      const active = document.activeElement as HTMLElement | null;
      const returnTo =
        !active || active.closest(".ant-dropdown")
          ? document.querySelector<HTMLElement>(
              `[data-group-actions="${CSS.escape(group.id)}"]`,
            )
          : active;
      setAction((current) => ({
        kind,
        group,
        returnTo,
        open: true,
        key: (current?.key ?? 0) + 1,
      }));
    },
    onDelete: (group) => {
      setDeleteReturnTo(
        document.querySelector<HTMLElement>(
          `[data-group-actions="${CSS.escape(group.id)}"]`,
        ),
      );
      setToDelete(group);
    },
    t,
  });

  return (
    <ListPage
      title={t("groups.groups")}
      description={t("groups.aggregateRepositoriesOfTheSameFormatBehindOne")}
      actions={
        <CreateGroupDialog
          repos={repos}
          profiles={formatProfiles}
          onCreated={load}
        />
      }
    >
      <ListStates
        rows={groups}
        loading={loading}
        error={error}
        onRetry={retry}
        empty={{
          title: t("groups.noGroups"),
          hint: t("groups.createAGroupToAggregateRepositories"),
        }}
        unavailable={{
          title: t("groups.groupsAreUnavailable"),
          hint: t("groups.theCurrentBackendDoesNotExposeTheGroups"),
        }}
      >
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
        <FilterCard
          className="ag-page-primary"
          active={filters.active}
          onClear={filters.reset}
          filters={
            <>
              <FilterField label={t("groups.search")} className="min-w-[260px]">
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={t("groups.searchGroupNames")}
                  value={filter}
                  onChange={(e) => filters.set({ q: e.target.value })}
                />
              </FilterField>
              <FilterField label={t("groups.format")} className="min-w-[150px]">
                <Select<Format | "all">
                  className="w-full"
                  value={formatFilter}
                  onChange={(format) => filters.set({ format })}
                  options={[
                    { value: "all", label: t("groups.allFormats") },
                    ...groupFormats(formatProfiles).map((format) => ({
                      value: format,
                      label: format,
                    })),
                  ]}
                />
              </FilterField>
            </>
          }
        >
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
              scroll={{ x: 1000 }}
            />
          )}
          <Pagination
            hasMore={!!nextToken}
            loading={loadingMore}
            onMore={loadMore}
          />
        </FilterCard>
      </ListStates>
      {action?.kind === "members" && (
        <MembersDialog
          key={action.key}
          open={action.open}
          group={action.group}
          repos={repos}
          onClose={closeAction}
          afterClose={afterActionClose}
          onSaved={() => void load()}
        />
      )}
      {action?.kind === "settings" && (
        <RenameGroupDialog
          key={action.key}
          open={action.open}
          group={action.group}
          onClose={closeAction}
          afterClose={afterActionClose}
          onSaved={() => void load()}
        />
      )}
      {action?.kind === "resolution" && (
        <GroupResolutionDialog
          key={action.key}
          open={action.open}
          group={action.group}
          onClose={closeAction}
          afterClose={afterActionClose}
        />
      )}
      {action?.kind === "capacity" && (
        <CapacityDialog
          key={action.key}
          open={action.open}
          group={action.group}
          onClose={closeAction}
          afterClose={afterActionClose}
        />
      )}
      <ConfirmDanger
        target={toDelete}
        title={t("groups.deleteGroup2")}
        message={(group) => (
          <>
            {t("groups.deleteGroup3")}{" "}
            <span className="font-mono text-fg-strong">{group.name}</span>{" "}
            {t("groups.memberRepositoriesAreNotDeleted")}
          </>
        )}
        confirmLabel={t("groups.delete")}
        onConfirm={(group) =>
          unwrap(deleteGroup({ path: { groupId: group.id } })).then(() => {})
        }
        onClose={() => setToDelete(null)}
        afterClose={() => deleteReturnTo?.focus()}
        onDone={() => void load()}
        onError={setError}
      />
    </ListPage>
  );
}
