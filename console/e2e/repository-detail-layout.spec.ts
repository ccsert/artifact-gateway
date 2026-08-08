import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

const repositoryId = "repo-layout";

async function mockRepositoryDetail(page: Page) {
  await authenticateAsAdmin(page);

  await page.route("**/api/v2/repositories?**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.route(`**/api/v2/repositories/${repositoryId}**`, (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (request.method() === "PATCH") {
      return route.fulfill({
        json: {
          id: repositoryId,
          name: "release-files",
          format: "raw",
          type: "hosted",
          anonymousRead: true,
          state: "active",
          version: "2",
        },
      });
    }
    if (path.endsWith("/artifact-search")) {
      return route.fulfill({
        json: {
          items: Array.from({ length: 20 }, (_, index) => ({
            coordinate: `releases/example-${index + 1}.zip`,
            digest: `sha256:${String(index).padStart(64, "0")}`,
            size: 1024 * (index + 1),
            createdAt: "2026-08-08T08:00:00Z",
          })),
        },
      });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format: "raw",
          type: "hosted",
          operations: ["read", "publish", "browse", "delete"],
        },
      });
    }
    if (path.endsWith("/effective-access")) {
      const allowed = {
        allowed: true,
        source: "administrator",
        reason: "administrator",
      };
      return route.fulfill({
        json: {
          actor: "admin",
          identity: {
            actor: "mock-admin",
            kind: "local_session",
            role: "admin",
            administrator: true,
          },
          repository: {
            id: repositoryId,
            name: "release-files",
            format: "raw",
            type: "hosted",
            state: "active",
          },
          anonymousRead: {
            allowed: true,
            source: "anonymous_policy",
            reason: "repository_anonymous_read_enabled",
          },
          permissions: { read: allowed, write: allowed, admin: allowed },
        },
      });
    }
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json: {
          repositoryId,
          format: "raw",
          usedBytes: 1024 * 1024,
          objectCount: 20,
          quotaBytes: 0,
        },
      });
    }
    return route.fulfill({
      json: {
        id: repositoryId,
        name: "release-files",
        format: "raw",
        type: "hosted",
        anonymousRead: true,
        state: "active",
        version: "1",
      },
    });
  });
}

test("repository detail keeps operational content above the fold", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockRepositoryDetail(page);

  await page.goto(`/repositories/${repositoryId}`);

  const summary = page.getByRole("group", { name: "仓库摘要" });
  await expect(summary).toBeVisible();
  await expect(summary).toContainText("1.0 MiB · 20 个对象");
  expect(
    (await summary.boundingBox())?.height ?? Number.POSITIVE_INFINITY,
  ).toBeLessThan(110);

  await page.getByRole("button", { name: "查看概念说明" }).click();
  await expect(page.getByText("概念说明", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Hosted Repository", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");

  await expect(page.getByRole("tab", { name: "设置" })).toBeVisible();
  await expect(page.getByRole("button", { name: "设置" })).toHaveCount(0);

  const table = page.locator(".ag-console-table");
  await expect(table).toBeVisible();
  expect(
    (await table.boundingBox())?.y ?? Number.POSITIVE_INFINITY,
  ).toBeLessThan(400);

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-detail.png"),
      fullPage: true,
    });
  }
});

test("repository settings live in a tab and keep the update workflow", async ({
  page,
}, testInfo) => {
  await mockRepositoryDetail(page);
  const updateRequest = page.waitForRequest(
    (request) =>
      request.method() === "PATCH" &&
      new URL(request.url()).pathname.endsWith(`/repositories/${repositoryId}`),
  );

  await page.goto(`/repositories/${repositoryId}`);
  const settingsTab = page.getByRole("tab", { name: "设置" });
  await settingsTab.click();

  await expect(page.getByRole("heading", { name: "仓库设置" })).toBeVisible();
  await expect
    .poll(async () => {
      const [tabBox, indicatorBox] = await Promise.all([
        settingsTab.boundingBox(),
        page.locator(".ant-tabs-ink-bar").boundingBox(),
      ]);
      if (!tabBox || !indicatorBox) return Number.POSITIVE_INFINITY;
      const tabCenter = tabBox.x + tabBox.width / 2;
      const indicatorCenter = indicatorBox.x + indicatorBox.width / 2;
      return Math.abs(tabCenter - indicatorCenter);
    })
    .toBeLessThan(2);
  await expect(page.getByRole("switch")).toBeVisible();
  await expect(page.getByRole("switch")).toBeChecked();
  await page.getByRole("button", { name: "保存更改" }).click();

  await updateRequest;
  await expect(page.getByText("仓库设置已保存")).toBeVisible();

  if (process.env.CAPTURE_REPOSITORY_DETAIL) {
    await page.screenshot({
      path: testInfo.outputPath("repository-settings.png"),
      fullPage: true,
    });
  }
});
