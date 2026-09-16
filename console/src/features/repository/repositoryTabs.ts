import type { Repository } from "../../client";

export type Tab =
  | "apt-snapshots"
  | "artifacts"
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
};

export const TABS: RepositoryTabDefinition[] = [
  { key: "artifacts", label: "制品", labelEn: "Artifacts" },
  {
    key: "apt-snapshots",
    label: "签名快照",
    labelEn: "Signed snapshots",
    formats: ["apt"],
    hostedOnly: true,
  },
  {
    key: "publish",
    label: "发布",
    labelEn: "Publish",
    formats: ["maven", "npm", "pypi"],
  },
  { key: "grants", label: "访问授权", labelEn: "Access grants" },
  {
    key: "retention",
    label: "保留策略",
    labelEn: "Retention",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
    hostedOnly: true,
  },
  {
    key: "scanning",
    label: "制品扫描",
    labelEn: "Scanning",
  },
  {
    key: "security",
    label: "安全准入",
    labelEn: "Security admission",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
    hostedOnly: true,
  },
  { key: "capacity", label: "容量", labelEn: "Capacity" },
  {
    key: "distribute",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
  },
  {
    key: "jobs",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go", "apt"],
  },
  {
    key: "tombstones",
    label: "墓碑",
    labelEn: "Tombstones",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi", "go"],
  },
  { key: "settings", label: "设置", labelEn: "Settings" },
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
