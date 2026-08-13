import type { ApiKey, User } from "../client";

export function isActiveUserPrincipal(user: Pick<User, "state">): boolean {
  return user.state === "active";
}

export function isActiveApiKeyPrincipal(
  key: Pick<ApiKey, "revokedAt" | "expiresAt">,
  now = Date.now(),
): boolean {
  return !key.revokedAt && (!key.expiresAt || Date.parse(key.expiresAt) > now);
}
