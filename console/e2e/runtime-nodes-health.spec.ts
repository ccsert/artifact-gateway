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
  await page.route("**/api/v2/scheduled-tasks", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    }),
  );
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        generatedAt: "2026-08-08T08:01:00Z",
        build: {
          version: "test",
          revision: "test",
          goVersion: "go1.test",
          modified: false,
        },
        runtime: {
          instanceId: "gateway",
          roles: ["api"],
          workerFormats: [],
          workerKinds: [],
        },
        dependencies: [],
        queues: [],
        nodes: {
          status: "healthy",
          online: 0,
          stale: 0,
          offline: 1,
          issues: [],
        },
      }),
    }),
  );

  await page.goto("/operations");
  await expect(page.getByRole("heading", { name: "任务中心" })).toBeVisible();
  await page.getByRole("tab", { name: "系统诊断" }).click();
  await expect(page.getByRole("heading", { name: "运行节点" })).toBeVisible();
  await expect(page.getByText("legacy-worker", { exact: true })).toBeVisible();
  await expect(page.getByText("无格式 Worker", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Unexpected Application Error!", { exact: true }),
  ).not.toBeVisible();
});

test("job history keeps the record path compact on mobile", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/lifecycle-jobs**", (route) =>
    route.fulfill({
      json: [
        {
          repositoryId: "repo-oci",
          repositoryName: "images-production-with-a-long-name",
          job: {
            id: "job-failed-001",
            kind: "promotion",
            state: "failed",
            createdAt: "2026-08-08T08:00:00Z",
            startedAt: "2026-08-08T08:01:00Z",
            completedAt: "2026-08-08T08:02:00Z",
            attempts: 3,
            maxAttempts: 3,
            lastError: "目标仓库拒绝了晋级请求",
          },
        },
      ],
    }),
  );
  await page.route("**/api/v2/audit-retention/jobs**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/scheduled-tasks", (route) =>
    route.fulfill({ json: [] }),
  );

  await page.goto("/operations");
  await page.getByRole("tab", { name: "执行记录" }).click();

  await expect(page.getByRole("heading", { name: "执行记录" })).toBeVisible();
  const mobileList = page.locator(".ag-operation-mobile-list");
  await expect(mobileList).toBeVisible();
  await expect(
    mobileList.getByText("目标仓库拒绝了晋级请求", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "运行节点" })).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      ),
    )
    .toBe(0);
});
