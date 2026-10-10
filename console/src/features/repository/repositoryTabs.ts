import type { Repository } from "../../client";

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
  label: string;
  labelEn: string;
}[] = [
  { key: "artifacts", label: "制品", labelEn: "Artifacts" },
  { key: "governance", label: "治理与安全", labelEn: "Governance" },
  { key: "distribution", label: "分发", labelEn: "Distribution" },
  { key: "settings", label: "设置", labelEn: "Settings" },
];

export type RepositoryTabDefinition = {
  key: Tab;
  /** Tabs are listed in group order; the first available one opens the group. */
  group: RepositoryTabGroup;
  label: string;
  labelEn: string;
  authority: RepositoryTabAuthority;
  formats?: string[];
  hostedOnly?: boolean;
};

const LIFECYCLE_FORMATS = [
  "maven",
  "oci",
  "conan",
  "raw",
  "npm",
  "pypi",
  "go",
  "cargo",
];
const LIFECYCLE_AND_APT_FORMATS = [...LIFECYCLE_FORMATS, "apt"];

export const TABS: RepositoryTabDefinition[] = [
  {
    key: "artifacts",
    group: "artifacts",
    label: "浏览",
    labelEn: "Browse",
    authority: "read",
  },
  {
    key: "usage",
    group: "artifacts",
    label: "使用统计",
    labelEn: "Usage",
    authority: "read",
  },
  {
    key: "publish",
    group: "artifacts",
    label: "发布",
    labelEn: "Publish",
    authority: "write",
    formats: ["maven", "npm", "pypi", "oci", "cargo"],
  },
  {
    key: "grants",
    group: "governance",
    label: "访问授权",
    labelEn: "Access grants",
    authority: "administer",
  },
  {
    key: "retention",
    group: "governance",
    label: "保留策略",
    labelEn: "Retention",
    authority: "administer",
    formats: LIFECYCLE_FORMATS,
    hostedOnly: true,
  },
  {
    key: "scanning",
    group: "governance",
    label: "制品扫描",
    labelEn: "Scanning",
    authority: "intelligence",
  },
  {
    key: "security",
    group: "governance",
    label: "安全准入",
    labelEn: "Security admission",
    authority: "administer",
    formats: LIFECYCLE_AND_APT_FORMATS,
    hostedOnly: true,
  },
  {
    key: "tombstones",
    group: "governance",
    label: "墓碑",
    labelEn: "Tombstones",
    authority: "administer",
    formats: LIFECYCLE_FORMATS,
  },
  {
    key: "distribute",
    group: "distribution",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    authority: "administer",
    formats: LIFECYCLE_FORMATS,
  },
  {
    key: "apt-snapshots",
    group: "distribution",
    label: "签名快照",
    labelEn: "Signed snapshots",
    authority: "administer",
    formats: ["apt"],
    hostedOnly: true,
  },
  {
    key: "settings",
    group: "settings",
    label: "基本设置",
    labelEn: "General",
    authority: "administer",
  },
  {
    key: "capacity",
    group: "settings",
    label: "容量",
    labelEn: "Capacity",
    authority: "administer",
  },
  {
    key: "jobs",
    group: "settings",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    authority: "administer",
    formats: LIFECYCLE_AND_APT_FORMATS,
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
    (!item.formats || item.formats.includes(repo.format)) &&
    (!item.hostedOnly || repo.type === "hosted") &&
    !(item.key === "publish" && repo.type === "proxy") &&
    !(repo.format === "apt" && item.key === "jobs" && repo.type !== "hosted")
  );
}
