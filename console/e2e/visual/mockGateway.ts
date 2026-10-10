import type { Page } from "@playwright/test";
import { defaultSiteSettings } from "../../src/lib/siteSettings";
import { authenticateAsAdmin } from "../support/auth";
import * as fixtures from "./fixtures";

type Json = unknown;

/** GET routes keyed by exact pathname. Anything else fails the test. */
const routes: Record<string, Json> = {
  "/api/v2/site-settings": defaultSiteSettings,
  "/api/v2/overview-statistics": fixtures.overviewStatistics,
  "/api/v2/repositories": fixtures.repositoryPage,
  "/api/v2/groups": fixtures.groupPage,
  "/api/v2/formats": fixtures.formatProfiles,
  "/api/v2/audits": fixtures.recentAudits,
  "/api/v2/audits/page": fixtures.auditPage,
  "/api/v2/artifact-search": fixtures.artifactSearch,
  "/api/v2/scheduled-tasks": fixtures.scheduledTasks,
  "/api/v2/runtime/nodes": fixtures.runtimeNodes,
  "/api/v2/diagnostics": fixtures.diagnostics,
  "/api/v2/anonymous-access-policy": fixtures.anonymousAccessPolicy,
  "/api/v2/api-keys": fixtures.apiKeys,
  "/api/v2/repository-grants": fixtures.repositoryGrants,
  "/api/v2/service-accounts": fixtures.serviceAccounts,
  "/api/v2/users": fixtures.users,
  "/api/v2/authentication/oidc": fixtures.oidcSettings,
  "/api/v2/public/repositories": fixtures.publicRepositories,
  "/api/v2/repository-capacities": fixtures.repositoryCapacities,
  "/api/v2/service-accounts/sa-ci/credentials":
    fixtures.serviceAccountCredentials,
  "/auth/oidc/config": fixtures.oidcLoginConfig,
};

for (const item of fixtures.repositories) {
  routes[`/api/v2/repositories/${item.id}`] = item;
}

export interface MockGateway {
  /** Requests that reached the Gateway without a fixture. */
  unmatched: string[];
}

/**
 * Serve every Gateway request from fixtures. Unknown requests answer 501 and
 * are recorded, so a page that starts calling a new endpoint fails loudly
 * instead of rendering a nondeterministic error state into the baseline.
 */
export async function mockGateway(
  page: Page,
  { authenticated }: { authenticated: boolean },
): Promise<MockGateway> {
  const state: MockGateway = { unmatched: [] };
  if (authenticated) await authenticateAsAdmin(page);
  const identityRoutes = new Set(["/api/v2/identity", "/auth/session"]);
  await page.route(
    (url) => /^\/(api\/v2|auth)\//.test(url.pathname),
    async (route) => {
      const request = route.request();
      const { pathname } = new URL(request.url());
      if (authenticated && identityRoutes.has(pathname)) {
        await route.fallback();
        return;
      }
      if (request.method() === "GET" && pathname in routes) {
        await route.fulfill({ json: routes[pathname] });
        return;
      }
      if (!authenticated && pathname === "/auth/session") {
        await route.fulfill({ json: { authenticated: false } });
        return;
      }
      state.unmatched.push(`${request.method()} ${pathname}`);
      await route.fulfill({
        status: 501,
        json: { code: "fixture_missing", message: pathname },
      });
    },
  );
  return state;
}
