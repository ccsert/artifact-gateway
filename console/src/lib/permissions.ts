import type { CurrentIdentity } from "../client/types.gen";

export type RepositoryPermissionRequirement =
  "read" | "write" | "admin" | "intelligence";

export type RepositoryPermissions = {
  read: { allowed: boolean };
  write: { allowed: boolean };
  admin: { allowed: boolean };
  intelligence: { allowed: boolean };
};

export type ConsolePermissions = {
  isAdministrator: boolean;
  isPending: boolean;
  canBrowseRepositories: boolean;
};

export function consolePermissions(
  identity: CurrentIdentity | null,
): ConsolePermissions {
  const isAdministrator = identity?.administrator === true;
  const role = identity?.role ?? "";
  return {
    isAdministrator,
    isPending: role === "none",
    // A member holds no repository capability of its own, but its per-repository
    // grants are exactly what the catalog lists, so it must reach the catalog.
    canBrowseRepositories:
      isAdministrator ||
      role === "member" ||
      role === "reader" ||
      role === "writer",
  };
}

export function repositoryPermissionAllowed(
  permissions: RepositoryPermissions | undefined,
  requirement: RepositoryPermissionRequirement,
): boolean {
  if (requirement === "read") return true;
  return permissions?.[requirement]?.allowed === true;
}
