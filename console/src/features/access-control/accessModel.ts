import type { AccessMessageKey } from "../../messages/access.zh-CN";
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
  desc: AccessMessageKey;
}[] = [
  {
    role: "admin",
    tone: "visualization-3",
    desc: "access.allOperationsBrowsePublishDeleteGrantsAndKeyManagement",
  },
  {
    role: "member",
    tone: "visualization-1",
    desc: "access.noImplicitCapabilityRepositoryAccessComesOnlyFromPer",
  },
  {
    role: "none",
    tone: "neutral",
    desc: "access.awaitingApprovalNoRepositoryPathIsReachable",
  },
];
export const AUTHORIZATION_STEPS = [
  {
    title: "access.identifyThePrincipal",
    text: "access.administratorIdentitiesPassDirectlyUsersApiKeysAndOidc",
  },
  {
    title: "access.applyTheGlobalRole",
    text: "access.adminAllowsAllOperationsIncludingArtifactIntelligenceWritesA",
  },
  {
    title: "access.applyRepositoryRules",
    text: "access.thePrincipalPermissionAndResourcePrefixMustAllMatch",
  },
  {
    title: "access.legacyCompatibility",
    text: "access.unmanagedRepositoriesCanFallBackToLegacyStaticPolicy",
  },
] as const;

export function scopeLabel(
  scopes: string[],
  t: (key: AccessMessageKey) => string,
): {
  key: "read" | "write" | "admin" | "intelligence" | "unknown";
  label: string;
  tone: BadgeTone;
} {
  if (scopes.includes("repositories:intelligence"))
    return {
      key: "intelligence",
      label: t("access.scopeIntelligence"),
      tone: "visualization-1",
    };
  if (scopes.includes("repositories:admin"))
    return {
      key: "admin",
      label: t("access.scopeAdmin"),
      tone: "visualization-3",
    };
  if (scopes.includes("repositories:write"))
    return {
      key: "write",
      label: t("access.scopeWrite"),
      tone: "visualization-5",
    };
  if (scopes.includes("repositories:read"))
    return {
      key: "read",
      label: t("access.scopeRead"),
      tone: "visualization-4",
    };
  return {
    key: "unknown",
    label: scopes.join(", ") || t("access.scopeUnknown"),
    tone: "neutral",
  };
}
