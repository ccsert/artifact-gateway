import type { CurrentIdentity, RepositoryEffectiveAccess } from "../client";

/**
 * Platform capabilities derived from the session identity. Navigation, route
 * guards, and platform-wide controls read them from here rather than comparing
 * roles inline, so one model change repoints every surface at once.
 */
export interface PlatformCapabilities {
  /** Registered but not approved: the account exists and reaches nothing. */
  pending: boolean;
  /** Platform administrator: the whole management surface plus every repository. */
  platformAdmin: boolean;
  /** Whether the repository catalog and its navigation are reachable at all. */
  browseRepositories: boolean;
}

export function platformCapabilities(
  identity: CurrentIdentity | null | undefined,
): PlatformCapabilities {
  const platformAdmin = identity?.administrator === true;
  return {
    pending: identity?.role === "none",
    platformAdmin,
    browseRepositories: platformAdmin || identity?.role === "member",
  };
}

/**
 * Repository capabilities derived from the effective-access answer for one
 * repository. A member level reaches nothing on its own, so these decisions are
 * the only input to repository-level gating.
 */
export interface RepositoryPermissions {
  read: boolean;
  write: boolean;
  administer: boolean;
  intelligence: boolean;
}

export function repositoryPermissions(
  access: RepositoryEffectiveAccess | null | undefined,
): RepositoryPermissions {
  const allowed = (decision: { allowed?: boolean } | undefined) =>
    decision?.allowed === true;
  return {
    read: allowed(access?.permissions?.read),
    write: allowed(access?.permissions?.write),
    administer: allowed(access?.permissions?.admin),
    intelligence: allowed(access?.permissions?.intelligence),
  };
}
