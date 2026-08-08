import { expect, test, type Page } from "@playwright/test";

function authenticate(page: Page) {
  return page.addInitScript(() => {
    localStorage.setItem("ag.console.token", "mock-admin-token");
    localStorage.setItem("ag.console.role", "admin");
  });
}

test("audit rows switch the single expanded detail row", async ({ page }) => {
  await authenticate(page);
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
  await authenticate(page);
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
