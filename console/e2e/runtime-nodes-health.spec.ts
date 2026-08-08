import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

test("operations survives legacy runtime node null arrays", async ({
  page,
}) => {
  await authenticateAsAdmin(page);

  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/lifecycle-jobs**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    }),
  );
  await page.route("**/api/v2/audit-retention/jobs**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    }),
  );
  await page.route("**/api/v2/runtime/nodes", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            instanceId: "legacy-worker",
            sessionId: null,
            roles: null,
            workerFormats: null,
            workerKinds: null,
            startedAt: "2026-08-08T08:00:00Z",
            lastSeenAt: "2026-08-08T08:01:00Z",
            status: "offline",
          },
        ],
        health: {
          status: "healthy",
          online: 0,
          stale: 0,
          offline: 1,
          issues: null,
        },
      }),
    }),
  );

  await page.goto("/operations");
  await expect(page.getByRole("heading", { name: "任务中心" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "运行节点" })).toBeVisible();
  await expect(page.getByText("legacy-worker", { exact: true })).toBeVisible();
  await expect(page.getByText("无格式 Worker", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Unexpected Application Error!", { exact: true }),
  ).not.toBeVisible();
});
