import type { Translate } from "../../lib/preferences";
import type {
  ApiKey,
  AuthorizationRole,
  Grant,
  Repository,
  ServiceAccount,
  User,
} from "../../client";
import type { BadgeTone } from "../../components/ui/Badge";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";

export type GrantLevel = "read" | "write" | "admin" | "intelligence";

export const CUSTOM_PRINCIPAL = "__custom__";
export const BUILTIN_PERMISSION_PREFIX = "builtin:";
export const CUSTOM_ROLE_PREFIX = "role:";
export const SNAPSHOT_PERMISSION = "snapshot";

/**
 * One editable grant row. `key` and `selectorFormat` are only used by the
 * authorization-template editor: its table addresses rows by key, and each row
 * may pick a resource-prefix format. The repository editor keys rows by index
 * and derives the format from the repository.
 */
export type DraftGrant = {
  key?: string;
  principal: string;
  scopes: Grant["scopes"];
  resourcePrefix?: string;
  roleId?: string;
  selectorFormat?: Repository["format"];
};

export interface PrincipalOption {
  value: string;
  label: string;
  detail: string;
}

type PrincipalKind = "user" | "api-key" | "service-account" | "custom";

function principalKind(principal: string): PrincipalKind {
  if (principal.startsWith("user:")) return "user";
  if (principal.startsWith("api-key:")) return "api-key";
  if (principal.startsWith("service-account:")) return "service-account";
  return "custom";
}

export function principalEditorKind(principal: string): PrincipalKind | "" {
  if (principal === CUSTOM_PRINCIPAL) return "custom";
  return principal ? principalKind(principal) : "";
}

export function grantTone(level: GrantLevel): BadgeTone {
  if (level === "intelligence") return "visualization-1";
  if (level === "admin") return "visualization-3";
  if (level === "write") return "visualization-5";
  return "visualization-4";
}

export function grantLevel(scopes: Grant["scopes"]): GrantLevel {
  if (scopes.includes("repositories:intelligence")) return "intelligence";
  if (scopes.includes("repositories:admin")) return "admin";
  if (scopes.includes("repositories:write")) return "write";
  return "read";
}

export function scopesForLevel(level: GrantLevel): Grant["scopes"] {
  return [`repositories:${level}`] as Grant["scopes"];
}

export function scopesForRole(role: AuthorizationRole): Grant["scopes"] {
  return [...role.scopes];
}

/**
 * Stable identity of one grant row, for React keys and for the per-row loading
 * and removal bookkeeping around them.
 *
 * The fields are JSON-encoded rather than joined with a separator: a principal
 * and a resource prefix may each contain the separators a hand-rolled key would
 * pick (`-`, a space), so two different grants could collapse into one key,
 * which shows up as duplicate React keys and as acting on the wrong row.
 */
export function grantRowKey(...parts: (string | undefined)[]): string {
  return JSON.stringify(parts.map((part) => part ?? ""));
}

/** A blank grant row: no principal and the least-privilege read scope. */
export function emptyGrant(): DraftGrant {
  return {
    key: `${Date.now()}-${Math.random()}`,
    principal: "",
    scopes: ["repositories:read"],
  };
}

export function permissionSelection(
  grant: DraftGrant,
  roles: AuthorizationRole[],
): string {
  if (grant.roleId && roles.some((role) => role.id === grant.roleId))
    return `${CUSTOM_ROLE_PREFIX}${grant.roleId}`;
  return grant.scopes.length === 1
    ? `${BUILTIN_PERMISSION_PREFIX}${grantLevel(grant.scopes)}`
    : SNAPSHOT_PERMISSION;
}

export function grantedCapabilitiesLabel(
  scopes: Grant["scopes"],
  t: Translate,
): string {
  if (scopes.includes("repositories:admin"))
    return t("access.readWriteAdminIntelligence");
  const capabilities: string[] = scopes.includes("repositories:write")
    ? [t("access.readWrite")]
    : scopes.includes("repositories:read")
      ? [t("access.scopeRead")]
      : [];
  if (scopes.includes("repositories:intelligence"))
    capabilities.push(t("access.scopeIntelligence"));
  return capabilities.join(" + ");
}

export function principalOptions(
  users: User[],
  apiKeys: ApiKey[],
  serviceAccounts: ServiceAccount[],
  t: Translate,
): PrincipalOption[] {
  return [
    ...users.filter(isActiveUserPrincipal).map((user) => ({
      value: `user:${user.name}`,
      label: `${t("access.user")} · ${user.name}`,
      detail: `${t("access.globalRole")} ${user.role}`,
    })),
    ...apiKeys
      .filter((key) => isActiveApiKeyPrincipal(key))
      .map((key) => ({
        value: `api-key:${key.id}`,
        label: `API Key · ${key.name}`,
        detail: `${t("access.globalRole")} ${key.roles.join(", ")}`,
      })),
    ...serviceAccounts
      .filter((account) => account.state === "active")
      .map((account) => ({
        value: `service-account:${account.id}`,
        label: `${t("access.serviceAccount")} · ${account.name}`,
        detail: t("access.noGlobalRoleRepositoryGrantsDecideAccess"),
      })),
  ];
}
