import type { Page } from "@playwright/test";

interface MockIdentity {
  actor: string;
  kind:
    "static_admin" | "static_resolver" | "local_session" | "api_key" | "oidc";
  role?: "admin" | "writer" | "reader";
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
