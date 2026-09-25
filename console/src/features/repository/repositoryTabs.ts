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

export type RepositoryTabDefinition = {
  key: Tab;
  label: string;
  labelEn: string;
  authority: RepositoryTabAuthority;
  formats?: string[];
  hostedOnly?: boolean;
};

export const TABS: RepositoryTabDefinition[] = [
  { key: "artifacts", label: "制品", labelEn: "Artifacts", authority: "read" },
  { key: "usage", label: "使用统计", labelEn: "Usage", authority: "read" },
  {
    key: "apt-snapshots",
    label: "签名快照",
    labelEn: "Signed snapshots",
    authority: "administer",
    formats: ["apt"],
    hostedOnly: true,
  },
  {
    key: "publish",
    label: "发布",
    labelEn: "Publish",
    authority: "write",
    formats: ["maven", "npm", "pypi", "oci"],
  },
  {
    key: "grants",
    label: "访问授权",
    labelEn: "Access grants",
    authority: "administer",
  },
  {
    key: "retention",
    label: "保留策略",
    labelEn: "Retention",
    authority: "administer",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
    hostedOnly: true,
  },
  {
    key: "scanning",
    label: "制品扫描",
    labelEn: "Scanning",
    authority: "intelligence",
  },
  {
    key: "security",
    label: "安全准入",
    labelEn: "Security admission",
    authority: "administer",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
    hostedOnly: true,
  },
  {
    key: "capacity",
    label: "容量",
    labelEn: "Capacity",
    authority: "administer",
  },
  {
    key: "distribute",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    authority: "administer",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
  },
  {
    key: "jobs",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    authority: "administer",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
  },
  {
    key: "tombstones",
    label: "墓碑",
    labelEn: "Tombstones",
    authority: "administer",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
  },
  {
    key: "settings",
    label: "设置",
    labelEn: "Settings",
    authority: "administer",
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
