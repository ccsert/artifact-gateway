import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

test("audit rows switch the single expanded detail row", async ({ page }) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/audits/page**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            occurredAt: "2026-08-05T00:00:00Z",
            operation: "get",
            outcome: "resolved",
            status: 200,
            resource: "resource-a",
            requestId: "request-a",
          },
          {
            occurredAt: "2026-08-05T00:01:00Z",
            operation: "get",
            outcome: "failed",
            status: 500,
            resource: "resource-b",
            requestId: "request-b",
          },
        ],
      }),
    }),
  );

  await page.goto("/audits");
  const firstRow = page.locator('tr[data-row-key="0"]');
  const secondRow = page.locator('tr[data-row-key="1"]');
  await expect(firstRow).toBeVisible();
  await expect(secondRow).toBeVisible();

  await firstRow.click();
  await expect(page.getByText("resource-a", { exact: true })).toBeVisible();

  await secondRow.click();
  await expect(page.getByText("resource-b", { exact: true })).toBeVisible();
  await expect(page.getByText("resource-a", { exact: true })).not.toBeVisible();
});

test("API key list defaults to active keys", async ({ page }) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories?pageSize=1", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/api-keys**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "key-active",
            name: "active-key",
            roles: ["reader"],
            createdAt: "2026-08-05T00:00:00Z",
            expiresAt: "2099-01-01T00:00:00Z",
          },
          {
            id: "key-expired",
            name: "expired-key",
            roles: ["reader"],
            createdAt: "2026-08-01T00:00:00Z",
            expiresAt: "2020-01-01T00:00:00Z",
          },
          {
            id: "key-revoked",
            name: "revoked-key",
            roles: ["writer"],
            createdAt: "2026-07-01T00:00:00Z",
            expiresAt: "2099-01-01T00:00:00Z",
            revokedAt: "2026-08-02T00:00:00Z",
          },
        ],
      }),
    }),
  );

  await page.goto("/keys");
  await expect(page.getByText("active-key", { exact: true })).toBeVisible();
  await expect(
    page.getByText("expired-key", { exact: true }),
  ).not.toBeVisible();
  await expect(
    page.getByText("revoked-key", { exact: true }),
  ).not.toBeVisible();
});

test("repository list exposes one cursor pagination control", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/formats", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            format: "oci",
            repositoryTypes: ["hosted", "proxy"],
            groupSupported: true,
            anonymousRead: true,
            hostedOperations: ["read", "publish", "browse"],
            proxyOperations: ["read", "browse"],
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/repositories**", (route) => {
    const pageToken = new URL(route.request().url()).searchParams.get(
      "pageToken",
    );
    const start = pageToken ? 101 : 1;
    return route.fulfill({
      json: {
        items: Array.from({ length: pageToken ? 1 : 25 }, (_, index) => ({
          id: `repo-${start + index}`,
          name: `repository-${String(start + index).padStart(3, "0")}`,
          format: "oci",
          type: "hosted",
          state: "active",
          version: "1",
        })),
        nextPageToken: pageToken ? undefined : "repositories-page-2",
      },
    });
  });

  await page.goto("/repositories");
  await expect(page.getByText("repository-025", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "加载更多" })).toBeVisible();
  await expect(page.locator(".ant-pagination")).toHaveCount(0);

  await page.getByRole("button", { name: "加载更多" }).click();
  await expect(page.getByText("repository-101", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "加载更多" })).toHaveCount(0);
});
