import type { MessageKey } from "../../messages";
import type { Repository } from "../../client";
import {
  supportsRepositoryFeature,
  type RepositoryFormatFeature,
} from "../../formats";

/**
 * The authority a tab's data and actions need on the repository. The server
 * gates every tab's endpoints on the same scopes, so a surface is only offered
 * when the repository's effective access grants it.
 */
export type RepositoryTabAuthority =
  "read" | "write" | "intelligence" | "administer";

export type Tab =
  | "apt-snapshots"
  | "artifacts"
  | "usage"
  | "publish"
  | "grants"
  | "retention"
  | "scanning"
  | "security"
  | "capacity"
  | "distribute"
  | "jobs"
  | "tombstones"
  | "settings";

/**
 * Design language v2 (issue 274) groups the repository tasks by intent so the
 * detail page offers four choices instead of a dozen peers. Deep links keep
 * addressing the individual task through `?tab=`.
 */
export type RepositoryTabGroup =
  "artifacts" | "governance" | "distribution" | "settings";

export const REPOSITORY_TAB_GROUPS: {
  key: RepositoryTabGroup;
  label: MessageKey;
}[] = [
  { key: "artifacts", label: "repository.tabArtifacts" },
  { key: "governance", label: "repository.tabGovernance" },
  { key: "distribution", label: "repository.tabDistribution" },
  { key: "settings", label: "repository.tabSettings" },
];

export type RepositoryTabDefinition = {
  key: Tab;
  /** Tabs are listed in group order; the first available one opens the group. */
  group: RepositoryTabGroup;
  label: MessageKey;
  authority: RepositoryTabAuthority;
  feature?: RepositoryFormatFeature;
};

export const TABS: RepositoryTabDefinition[] = [
  {
    key: "artifacts",
    group: "artifacts",
    label: "repository.tabBrowse",
    authority: "read",
  },
  {
    key: "usage",
    group: "artifacts",
    label: "repository.tabUsage",
    authority: "read",
  },
  {
    key: "publish",
    group: "artifacts",
    label: "repository.tabPublish",
    authority: "write",
    feature: "publish",
  },
  {
    key: "grants",
    group: "governance",
    label: "repository.tabAccessGrants",
    authority: "administer",
  },
  {
    key: "retention",
    group: "governance",
    label: "repository.tabRetention",
    authority: "administer",
    feature: "retention",
  },
  {
    key: "scanning",
    group: "governance",
    label: "repository.tabScanning",
    authority: "intelligence",
  },
  {
    key: "security",
    group: "governance",
    label: "repository.tabSecurityAdmission",
    authority: "administer",
    feature: "security",
  },
  {
    key: "tombstones",
    group: "governance",
    label: "repository.tabTombstones",
    authority: "administer",
    feature: "tombstones",
  },
  {
    key: "distribute",
    group: "distribution",
    label: "repository.tabPromoteReplicate",
    authority: "administer",
    feature: "distribute",
  },
  {
    key: "apt-snapshots",
    group: "distribution",
    label: "repository.tabSignedSnapshots",
    authority: "administer",
    feature: "apt-snapshots",
  },
  {
    key: "settings",
    group: "settings",
    label: "repository.tabGeneral",
    authority: "administer",
  },
  {
    key: "capacity",
    group: "settings",
    label: "repository.tabCapacity",
    authority: "administer",
  },
  {
    key: "jobs",
    group: "settings",
    label: "repository.tabLifecycleJobs",
    authority: "administer",
    feature: "jobs",
  },
];

export function repositoryTabFromQuery(value: string | null): Tab {
  return TABS.find((tab) => tab.key === value)?.key ?? "artifacts";
}

export function repositoryTabAvailable(
  item: RepositoryTabDefinition,
  repo: Repository,
) {
  return (
    !item.feature ||
    supportsRepositoryFeature(repo.format, repo.type, item.feature)
  );
}
