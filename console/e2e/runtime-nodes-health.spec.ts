import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { expectTabGutter } from "./support/tabs";

test("operations survives legacy runtime node null arrays", async ({
  page,
}, testInfo) => {
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
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("operations-tables.png"),
    });
  }
  await page.getByRole("link", { name: "系统运行" }).click();
  await expect(page.getByRole("heading", { name: "系统运行" })).toBeVisible();
  await expectTabGutter(page, ".ag-compact-tabs", ".ag-page-stack");
  await expect(page.getByRole("tab", { name: "系统诊断" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("heading", { name: "构建信息" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "运行身份", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "运行节点" })).toBeVisible();
  await expect(page.getByText("legacy-worker", { exact: true })).toBeVisible();
  await expect(page.getByText("版本未知", { exact: true })).toBeVisible();
  await expect(page.getByText("无格式 Worker", { exact: true })).toBeVisible();
  const backgroundQueues = page
    .getByRole("heading", { name: "后台队列" })
    .locator(
      "xpath=ancestor::*[contains(concat(' ', normalize-space(@class), ' '), ' ag-card ')][1]",
    );
  const runtimeNodes = page
    .getByRole("heading", { name: "运行节点" })
    .locator(
      "xpath=ancestor::*[contains(concat(' ', normalize-space(@class), ' '), ' ag-card ')][1]",
    );
  await expect
    .poll(async () => {
      const queueBox = await backgroundQueues.boundingBox();
      const runtimeBox = await runtimeNodes.boundingBox();
      if (!queueBox || !runtimeBox) return -1;
      return Math.round(runtimeBox.y - (queueBox.y + queueBox.height));
    })
    .toBeGreaterThanOrEqual(24);
  const identityCard = page.locator(".ag-diagnostics-identity-card");
  await expect
    .poll(() =>
      identityCard.evaluate(
        (element) => element.scrollWidth - element.clientWidth,
      ),
    )
    .toBe(0);
  await expect(
    page.getByText("Unexpected Application Error!", { exact: true }),
  ).not.toBeVisible();
});

test("connected version links to diagnostics and reports a rolling upgrade", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/diagnostics", (route) =>
    route.fulfill({
      json: {
        generatedAt: "2026-09-30T08:00:00Z",
        build: {
          version: "v0.4.3",
          revision: "def456",
          goVersion: "go1.26",
          modified: false,
        },
        runtime: {
          instanceId: "api-new",
          sessionId: "session-new",
          roles: ["api"],
          workerFormats: [],
          workerKinds: [],
        },
        dependencies: [],
        queues: [],
        nodes: {
          status: "degraded",
          online: 2,
          stale: 0,
          offline: 0,
          issues: [],
        },
      },
    }),
  );
  await page.route("**/api/v2/runtime/nodes", (route) =>
    route.fulfill({
      json: {
        currentSessionId: "session-new",
        releaseSource: "not_configured",
        items: [
          {
            instanceId: "api-new",
            sessionId: "session-new",
            version: "v0.4.3",
            revision: "def456",
            roles: ["api"],
            workerFormats: [],
            workerKinds: [],
            startedAt: "2026-09-30T08:00:00Z",
            lastSeenAt: "2026-09-30T08:01:00Z",
            status: "online",
          },
          {
            instanceId: "worker-old",
            sessionId: "session-old",
            version: "v0.4.2",
            revision: "abc123",
            roles: ["worker"],
            workerFormats: ["oci"],
            workerKinds: ["reclaim"],
            startedAt: "2026-09-29T08:00:00Z",
            lastSeenAt: "2026-09-30T08:01:00Z",
            status: "online",
          },
        ],
        health: {
          status: "degraded",
          online: 2,
          stale: 0,
          offline: 0,
          issues: [
            {
              code: "mixed_build",
              severity: "warning",
              message: "在线节点运行不同的版本或修订号，可能正在滚动升级",
              affectedNodes: ["session-new", "session-old"],
            },
          ],
        },
      },
    }),
  );
  await page.goto("/repositories");
  await page
    .locator(".ag-sider-desktop")
    .getByRole("link", { name: /当前节点 · v0.4.3 · def456/ })
    .click();
  await expect(page).toHaveURL(/\/system\?tab=diagnostics$/);
  await expect(page.getByRole("tab", { name: "系统诊断" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByText("当前连接节点", { exact: true })).toBeVisible();
  await expect(
    page.getByText("涉及会话: session-new, session-old"),
  ).toBeVisible();
  await expect(page.getByText(/无法判断当前构建是否为最新版本/)).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(1);
  await page.screenshot({
    path: testInfo.outputPath("runtime-version-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: "运行节点" })).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(1);
  await page.screenshot({
    path: testInfo.outputPath("runtime-version-mobile.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "打开导航" }).click();
  await expect(
    page
      .locator(".ag-mobile-nav-drawer")
      .getByRole("link", { name: /当前节点 · v0.4.3 · def456/ }),
  ).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test("job history uses one compact and consistent detail path", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1180, height: 900 });
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
            completedAt: "2026-08-08T08:02:00Z",
            attempts: 3,
            maxAttempts: 3,
            lastError: "目标仓库拒绝了晋级请求",
          },
        },
        {
          repositoryId: "repo-maven",
          repositoryName: "maven-releases",
          job: {
            id: "job-completed-001",
            kind: "retention",
            state: "completed",
            createdAt: "2026-08-08T07:00:00Z",
            completedAt: "2026-08-08T07:02:00Z",
            attempts: 1,
            maxAttempts: 3,
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

  const table = page.locator(".ag-operation-desktop-table");
  await expect(table).toBeVisible();
  await expect(table.locator(".ant-table-row-expand-icon")).toHaveCount(0);
  const detailButtons = table.getByRole("button", { name: "查看任务详情" });
  await expect(detailButtons).toHaveCount(2);
  await expect
    .poll(() =>
      table
        .locator(".ant-table-container")
        .evaluate((element) => element.scrollWidth - element.clientWidth),
    )
    .toBe(0);
  await detailButtons.nth(1).click();
  await expect(detailButtons.nth(1)).toHaveAttribute("aria-expanded", "true");
  await expect(
    table.getByText("此任务没有报告额外的执行详情。", { exact: true }),
  ).toBeVisible();
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
      page.evaluate(() =>
        Math.max(
          0,
          document.documentElement.scrollWidth -
            document.documentElement.clientWidth,
        ),
      ),
    )
    .toBe(0);
});
