import type { ApiKey, Repository } from "../../client";
import type { BadgeTone } from "../../components/ui/Badge";

export interface GrantRow {
  repositoryId: string;
  repositoryName: string;
  format: Repository["format"];
  principal: string;
  scopes: string[];
  resourcePrefix: string;
}

export type EvaluatorRole = "none" | "member" | "admin";
export type AccessControlTab = "grants" | "evaluate" | "policies";

export function accessControlTabFromQuery(
  value: string | null,
): AccessControlTab {
  return value === "evaluate" || value === "policies" ? value : "grants";
}

export interface EvaluatorPrincipal {
  value: string;
  actor: string;
  label: string;
  detail: string;
  role: EvaluatorRole;
  current?: boolean;
}

export const CURRENT_PRINCIPAL = "__current__";
export const CUSTOM_PRINCIPAL = "__custom__";

export function strongestRole(roles: ApiKey["roles"]): EvaluatorRole {
  if (roles.includes("admin")) return "admin";
  if (roles.includes("member")) return "member";
  return "none";
}

export function resourcePlaceholder(format: Repository["format"] | undefined) {
  switch (format) {
    case "maven":
      return "org/example/app/1.0/app-1.0.jar";
    case "oci":
      return "team/backend";
    case "conan":
      return "pkg/1.0/user/stable";
    case "raw":
      return "releases/2026/app.tar.gz";
    case "npm":
      return "@scope/package";
    case "pypi":
      return "gateway-widget";
    case "go":
      return "example.com/team/widget";
    case "apt":
      return "dists/bookworm/InRelease";
    default:
      return "";
  }
}

export const ROLE_REFERENCE: {
  role: string;
  tone: BadgeTone;
  desc: string;
  descEn: string;
}[] = [
  {
    role: "admin",
    tone: "visualization-3",
    desc: "全部操作：浏览、发布、删除、授权、密钥管理",
    descEn:
      "All operations: browse, publish, delete, grants, and key management",
  },
  {
    role: "member",
    tone: "visualization-1",
    desc: "不含隐式权限：仓库访问完全由按仓库授权决定",
    descEn:
      "No implicit capability: repository access comes only from per-repository grants",
  },
  {
    role: "none",
    tone: "neutral",
    desc: "待授权：不能访问任何仓库路径",
    descEn: "Awaiting approval: no repository path is reachable",
  },
];
export const AUTHORIZATION_STEPS = [
  {
    title: "先看身份",
    text: "管理员身份直接放行；用户、API Key 或 OIDC 身份会先带着自己的全局角色进入判定。",
    titleEn: "Identify the principal",
    textEn:
      "Administrator identities pass directly. Users, API keys, and OIDC identities enter with their global role.",
  },
  {
    title: "再看全局角色",
    text: "admin 允许全部操作（包括写入制品情报）；member 等级不附带任何仓库权限，仓库访问由按仓库授权决定；制品情报是独立的最小权限。",
    titleEn: "Apply the global role",
    textEn:
      "Admin allows all operations, including artifact intelligence writes. A member level carries no repository capability, so per-repository grants decide; artifact intelligence is an independent least-privilege scope.",
  },
  {
    title: "再看仓库规则",
    text: "主体、权限级别和资源前缀都匹配才放行；制品情报权限只允许写安全元数据，未匹配会拒绝。",
    titleEn: "Apply repository rules",
    textEn:
      "The principal, permission, and resource prefix must all match. Artifact intelligence only writes security metadata; otherwise access is denied.",
  },
  {
    title: "兼容旧策略",
    text: "仓库还没有被正式管理时，才回退到旧版静态仓库策略；匿名访问另按全局和仓库开关判断。",
    titleEn: "Legacy compatibility",
    textEn:
      "Unmanaged repositories can fall back to legacy static policy. Anonymous access is evaluated separately.",
  },
];

export function scopeLabel(
  scopes: string[],
  english = false,
): {
  key: "read" | "write" | "admin" | "intelligence" | "unknown";
  label: string;
  tone: BadgeTone;
} {
  if (scopes.includes("repositories:intelligence"))
    return {
      key: "intelligence",
      label: english ? "Artifact intelligence" : "制品情报",
      tone: "visualization-1",
    };
  if (scopes.includes("repositories:admin"))
    return {
      key: "admin",
      label: english ? "Admin" : "管理员",
      tone: "visualization-3",
    };
  if (scopes.includes("repositories:write"))
    return {
      key: "write",
      label: english ? "Write" : "写入",
      tone: "visualization-5",
    };
  if (scopes.includes("repositories:read"))
    return {
      key: "read",
      label: english ? "Read" : "读取",
      tone: "visualization-4",
    };
  return {
    key: "unknown",
    label: scopes.join(", ") || (english ? "Not configured" : "未配置"),
    tone: "neutral",
  };
}
