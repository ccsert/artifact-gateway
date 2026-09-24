import type { Repository } from "../../client";
import {
  repositoryPermissionAllowed,
  type RepositoryPermissionRequirement,
  type RepositoryPermissions,
} from "../../lib/permissions";

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
  formats?: string[];
  hostedOnly?: boolean;
  requires?: RepositoryPermissionRequirement;
};

export const TABS: RepositoryTabDefinition[] = [
  { key: "artifacts", label: "制品", labelEn: "Artifacts" },
  { key: "usage", label: "使用统计", labelEn: "Usage" },
  {
    key: "apt-snapshots",
    label: "签名快照",
    labelEn: "Signed snapshots",
    formats: ["apt"],
    hostedOnly: true,
    requires: "admin",
  },
  {
    key: "publish",
    label: "发布",
    labelEn: "Publish",
    formats: ["maven", "npm", "pypi"],
    requires: "write",
  },
  {
    key: "grants",
    label: "访问授权",
    labelEn: "Access grants",
    requires: "admin",
  },
  {
    key: "retention",
    label: "保留策略",
    labelEn: "Retention",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
    hostedOnly: true,
    requires: "admin",
  },
  {
    key: "scanning",
    label: "制品扫描",
    labelEn: "Scanning",
    requires: "intelligence",
  },
  {
    key: "security",
    label: "安全准入",
    labelEn: "Security admission",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
    hostedOnly: true,
    requires: "admin",
  },
  {
    key: "capacity",
    label: "容量",
    labelEn: "Capacity",
    requires: "admin",
  },
  {
    key: "distribute",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
    requires: "admin",
  },
  {
    key: "jobs",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
    requires: "admin",
  },
  {
    key: "tombstones",
    label: "墓碑",
    labelEn: "Tombstones",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
    requires: "admin",
  },
  {
    key: "settings",
    label: "设置",
    labelEn: "Settings",
    requires: "admin",
  },
];

export function repositoryTabFromQuery(value: string | null): Tab {
  return TABS.find((tab) => tab.key === value)?.key ?? "artifacts";
}

export function repositoryTabAvailable(
  item: RepositoryTabDefinition,
  repo: Repository,
  permissions?: RepositoryPermissions,
) {
  return (
    (!item.formats || item.formats.includes(repo.format)) &&
    (!item.hostedOnly || repo.type === "hosted") &&
    !(item.key === "publish" && repo.type === "proxy") &&
    !(repo.format === "apt" && item.key === "jobs" && repo.type !== "hosted") &&
    repositoryPermissionAllowed(permissions, item.requires ?? "read")
  );
}
