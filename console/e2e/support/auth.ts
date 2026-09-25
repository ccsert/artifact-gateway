import type { Page } from "@playwright/test";

/**
 * The identity payload the server really sends. It carries no capability set:
 * `platformCapabilities` derives the platform tier from `administrator` and
 * `role`, and the repository tier comes from the repository's own
 * effective-access answer. A mock that invented capability fields would prove
 * the console reads fields production never sends.
 */
interface MockIdentity {
  actor: string;
  kind:
    "static_admin" | "static_resolver" | "local_session" | "api_key" | "oidc";
  role?: "admin" | "member" | "none";
  administrator: boolean;
}

export async function authenticateWithIdentity(
  page: Page,
  identity: MockIdentity,
) {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
  await page.route("**/api/v2/identity", (route) =>
    route.fulfill({
      json: identity,
    }),
  );
  await page.route("**/auth/session", (route) =>
    route.fulfill({
      json: { authenticated: true, identity },
    }),
  );
}

export function authenticateAsAdmin(page: Page) {
  return authenticateWithIdentity(page, {
    actor: "mock-admin",
    kind: "local_session",
    role: "admin",
    administrator: true,
  });
}

/** A member account: the level that reaches repositories through grants. */
export function authenticateAsMember(page: Page, actor = "user:member") {
  return authenticateWithIdentity(page, {
    actor,
    kind: "local_session",
    role: "member",
    administrator: false,
  });
}

/**
 * A repository administrator is a member account holding grants on a
 * repository, so the identity alone cannot express it: the caller still has to
 * mock that repository's effective-access answer.
 */
export function authenticateAsRepositoryAdministrator(
  page: Page,
  actor = "user:repo-admin",
) {
  return authenticateAsMember(page, actor);
}
