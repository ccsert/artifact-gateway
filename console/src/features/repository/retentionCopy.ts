import type { Repository, RetentionDryRun } from "../../client";

export type Localize = (chinese: string, english: string) => string;

/** Retention labels and hints in each format's own vocabulary. */
export function retentionFormatCopy(
  format: Repository["format"],
  text: Localize,
) {
  switch (format) {
    case "maven":
      return {
        ageLabel: text(
          "Maven 发布版本保留天数",
          "Maven release retention days",
        ),
        ageHint: text(
          "发布版本创建超过此天数后，才会进入清理候选。SNAPSHOT 使用下方独立期限。",
          "A release becomes eligible after this many days. SNAPSHOT builds use the separate period below.",
        ),
        minimumLabel: text(
          "每个 Maven 模块最少保留版本",
          "Minimum versions per Maven module",
        ),
        minimumHint: text(
          "按 groupId:artifactId 分组，始终保护最新的这些版本。",
          "Group by groupId:artifactId and always protect the newest versions.",
        ),
        maximumLabel: text(
          "每个 Maven 模块最多保留版本",
          "Maximum versions per Maven module",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧版本或快照构建会进入候选。",
          "Use 0 for no limit. Older releases or snapshot builds beyond the limit become eligible.",
        ),
        matchLabel: text(
          "只清理匹配 Maven 坐标",
          "Only clean matching Maven coordinates",
        ),
        matchHint: text(
          "可选 RE2 坐标正则；留空表示匹配全部 Maven 坐标。",
          "Optional RE2 coordinate regex. Leave empty to match all Maven coordinates.",
        ),
        protectLabel: text("保护 Maven 坐标", "Protect Maven coordinates"),
        protectHint: text(
          "匹配任一正则的坐标永不进入清理候选。",
          "Coordinates matching any regex never become eligible.",
        ),
        matchPlaceholder: text("如 ^com\\.example:", "e.g. ^com\\.example:"),
        protectPlaceholder: text(
          "如 ^com\\.example:platform:",
          "e.g. ^com\\.example:platform:",
        ),
        candidateName: text("Maven 制品版本", "Maven artifact versions"),
        candidateType: text("Maven 制品版本", "Maven artifact version"),
      };
    case "oci":
      return {
        ageLabel: text("镜像版本保留天数", "Image version retention days"),
        ageHint: text(
          "Manifest 创建超过此天数后，才会进入清理候选。",
          "A manifest becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个镜像最少保留版本",
          "Minimum versions per image",
        ),
        minimumHint: text(
          "按镜像名称分组，始终保护最新的这些 manifest。",
          "Group by image name and always protect these newest manifests.",
        ),
        maximumLabel: text(
          "每个镜像最多保留版本",
          "Maximum versions per image",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧 manifest 会进入候选。",
          "Use 0 for no limit. Older manifests beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配镜像", "Only clean matching images"),
        matchHint: text(
          "可匹配镜像名、name@digest 或 name:tag；留空表示全部。",
          "Matches image name, name@digest, or name:tag. Leave empty for all images.",
        ),
        protectLabel: text("保护镜像版本", "Protect image versions"),
        protectHint: text(
          "可用镜像名保护全部版本，或用 digest、tag 精确保护。",
          "Use an image name to protect all versions, or a digest/tag for an exact version.",
        ),
        matchPlaceholder: text(
          "如 ^team/backend(@|:)",
          "e.g. ^team/backend(@|:)",
        ),
        protectPlaceholder: text(
          "如 ^team/backend:stable$",
          "e.g. ^team/backend:stable$",
        ),
        candidateName: text("镜像版本", "image versions"),
        candidateType: text("Manifest 版本", "Manifest version"),
      };
    case "conan":
      return {
        ageLabel: text(
          "Recipe revision 保留天数",
          "Recipe revision retention days",
        ),
        ageHint: text(
          "Recipe revision 创建超过此天数后，才会进入清理候选。",
          "A recipe revision becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 reference 最少保留版本",
          "Minimum versions per reference",
        ),
        minimumHint: text(
          "按完整 Conan reference 分组，保护最新的 recipe revisions。",
          "Group by full Conan reference and protect the newest recipe revisions.",
        ),
        maximumLabel: text(
          "每个 reference 最多保留版本",
          "Maximum versions per reference",
        ),
        maximumHint: text(
          "0 表示不限制；清理 recipe revision 时会同时隐藏其二进制包。",
          "Use 0 for no limit. Cleaning a recipe revision also hides its binary packages.",
        ),
        matchLabel: text(
          "只清理匹配 reference",
          "Only clean matching references",
        ),
        matchHint: text(
          "可匹配完整 reference 或 reference#recipe-revision。",
          "Matches a full reference or reference#recipe-revision.",
        ),
        protectLabel: text("保护 Conan 版本", "Protect Conan versions"),
        protectHint: text(
          "匹配 reference 可保护全部 revisions，精确坐标只保护一个版本。",
          "A matching reference protects all revisions; an exact coordinate protects one version.",
        ),
        matchPlaceholder: text("如 ^openssl/3\\.", "e.g. ^openssl/3\\."),
        protectPlaceholder: text(
          "如 @release/stable(#|$)",
          "e.g. @release/stable(#|$)",
        ),
        candidateName: "Recipe revision",
        candidateType: "Recipe revision",
      };
    case "raw":
      return {
        ageLabel: text("资产未更新保留天数", "Asset inactivity retention days"),
        ageHint: text(
          "路径资产超过此天数未更新后，才会进入清理候选。",
          "A path asset becomes eligible after it has not been updated for this many days.",
        ),
        minimumLabel: "",
        minimumHint: "",
        maximumLabel: "",
        maximumHint: "",
        matchLabel: text("只清理匹配路径", "Only clean matching paths"),
        matchHint: text(
          "可选 RE2 路径正则；留空表示匹配仓库内全部资产。",
          "Optional RE2 path regex. Leave empty to match every repository asset.",
        ),
        protectLabel: text("保护路径", "Protect paths"),
        protectHint: text(
          "匹配任一正则的路径永不进入清理候选。",
          "Paths matching any regex never become eligible.",
        ),
        matchPlaceholder: text(
          "如 ^releases/nightly/",
          "e.g. ^releases/nightly/",
        ),
        protectPlaceholder: text(
          "如 ^releases/stable/",
          "e.g. ^releases/stable/",
        ),
        candidateName: text("路径资产", "path assets"),
        candidateType: text("路径资产", "Path asset"),
      };
    case "npm":
      return {
        ageLabel: text(
          "npm 包版本保留天数",
          "npm package version retention days",
        ),
        ageHint: text(
          "包版本发布超过此天数后，才会进入清理候选。",
          "A package version becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 npm 包最少保留版本",
          "Minimum versions per npm package",
        ),
        minimumHint: text(
          "按包名分组，始终保护按发布时间最新的这些版本。",
          "Group by package name and always protect these most recently published versions.",
        ),
        maximumLabel: text(
          "每个 npm 包最多保留版本",
          "Maximum versions per npm package",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的较早发布版本会进入候选。",
          "Use 0 for no limit. Earlier publications beyond the limit become eligible.",
        ),
        matchLabel: text(
          "只清理匹配 npm 包",
          "Only clean matching npm packages",
        ),
        matchHint: text(
          "可匹配包名或 package@version；留空表示全部 npm 包。",
          "Matches a package name or package@version. Leave empty for all npm packages.",
        ),
        protectLabel: text("保护 npm 包版本", "Protect npm package versions"),
        protectHint: text(
          "匹配包名可保护全部版本，匹配 package@version 可保护一个版本。",
          "Match a package name to protect every version, or package@version for one version.",
        ),
        matchPlaceholder: text("如 ^@team/", "e.g. ^@team/"),
        protectPlaceholder: text(
          "如 ^@team/platform(@|$)",
          "e.g. ^@team/platform(@|$)",
        ),
        candidateName: text("npm 包版本", "npm package versions"),
        candidateType: text("npm 包版本", "npm package version"),
      };
    case "pypi":
      return {
        ageLabel: text(
          "PyPI 项目版本保留天数",
          "PyPI project version retention days",
        ),
        ageHint: text(
          "项目版本发布超过此天数后，才会进入清理候选。",
          "A project version becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 PyPI 项目最少保留版本",
          "Minimum versions per PyPI project",
        ),
        minimumHint: text(
          "按规范化项目名分组，始终保护按发布时间最新的这些版本。",
          "Group by normalized project name and always protect these most recently published versions.",
        ),
        maximumLabel: text(
          "每个 PyPI 项目最多保留版本",
          "Maximum versions per PyPI project",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的较早发布版本会进入候选，并隐藏该版本的全部文件。",
          "Use 0 for no limit. Earlier publications beyond the limit become eligible together with every file in that version.",
        ),
        matchLabel: text(
          "只清理匹配 PyPI 项目",
          "Only clean matching PyPI projects",
        ),
        matchHint: text(
          "可匹配项目名或 project@version；留空表示全部 PyPI 项目。",
          "Matches a project name or project@version. Leave empty for all PyPI projects.",
        ),
        protectLabel: text(
          "保护 PyPI 项目版本",
          "Protect PyPI project versions",
        ),
        protectHint: text(
          "匹配项目名可保护全部版本，匹配 project@version 可保护一个版本。",
          "Match a project name to protect every version, or project@version for one version.",
        ),
        matchPlaceholder: text("如 ^internal-", "e.g. ^internal-"),
        protectPlaceholder: text(
          "如 ^platform-core(@|$)",
          "e.g. ^platform-core(@|$)",
        ),
        candidateName: text("PyPI 项目版本", "PyPI project versions"),
        candidateType: text("PyPI 项目版本", "PyPI project version"),
      };
    case "go":
      return {
        ageLabel: text(
          "Go 模块版本保留天数",
          "Go module version retention days",
        ),
        ageHint: text(
          "模块版本发布超过此天数后，才会进入清理候选。",
          "A module version becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 Go 模块最少保留版本",
          "Minimum versions per Go module",
        ),
        minimumHint: text(
          "按模块路径分组，始终保护按发布时间最新的这些版本。",
          "Group by module path and always protect these most recently published versions.",
        ),
        maximumLabel: text(
          "每个 Go 模块最多保留版本",
          "Maximum versions per Go module",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的较早发布版本会进入候选。",
          "Use 0 for no limit. Earlier publications beyond the limit become eligible.",
        ),
        matchLabel: text(
          "只清理匹配 Go 模块",
          "Only clean matching Go modules",
        ),
        matchHint: text(
          "可匹配模块路径或 module@version；留空表示全部 Go 模块。",
          "Matches a module path or module@version. Leave empty for all Go modules.",
        ),
        protectLabel: text("保护 Go 模块版本", "Protect Go module versions"),
        protectHint: text(
          "匹配模块路径可保护全部版本，匹配 module@version 可保护一个版本。",
          "Match a module path to protect every version, or module@version for one version.",
        ),
        matchPlaceholder: text(
          "如 ^example\\.com/team/",
          "e.g. ^example\\.com/team/",
        ),
        protectPlaceholder: text(
          "如 ^example\\.com/team/widget(@|$)",
          "e.g. ^example\\.com/team/widget(@|$)",
        ),
        candidateName: text("Go 模块版本", "Go module versions"),
        candidateType: text("Go 模块版本", "Go module version"),
      };
    default:
      return {
        ageLabel: text("制品保留天数", "Artifact retention days"),
        ageHint: text(
          "制品创建超过此天数后，才会进入清理候选。",
          "An artifact becomes eligible after this many days.",
        ),
        minimumLabel: text("每组最少保留版本", "Minimum versions per group"),
        minimumHint: text(
          "始终保护每组最新的这些版本。",
          "Always protect these newest versions in each group.",
        ),
        maximumLabel: text("每组最多保留版本", "Maximum versions per group"),
        maximumHint: text(
          "0 表示不限制；超过上限的旧版本会进入候选。",
          "Use 0 for no limit. Older versions beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配制品", "Only clean matching artifacts"),
        matchHint: text(
          "可选 RE2 正则；留空表示匹配全部制品。",
          "Optional RE2 regex. Leave empty to match all artifacts.",
        ),
        protectLabel: text("保护制品", "Protect artifacts"),
        protectHint: text(
          "匹配任一正则的制品永不进入清理候选。",
          "Artifacts matching any regex never become eligible.",
        ),
        matchPlaceholder: text("如 ^team/", "e.g. ^team/"),
        protectPlaceholder: text("如 /stable$", "e.g. /stable$"),
        candidateName: text("制品", "artifacts"),
        candidateType: text("制品版本", "Artifact version"),
      };
  }
}

export function retentionCandidateTypeLabel(
  versionType: RetentionDryRun["candidates"][number]["versionType"],
  versionLabel: string,
  text: Localize,
) {
  if (versionType === "snapshot") return text("快照构建", "Snapshot build");
  if (versionType === "release") return text("发布版本", "Release version");
  if (versionType === "asset") return text("路径资产", "Path asset");
  return versionLabel;
}
