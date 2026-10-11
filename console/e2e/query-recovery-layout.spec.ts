import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { waitForModalOpen } from "./support/motion";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "synthetic-query-recovery",
  format: "maven",
  type: "hosted",
  state: "active",
  anonymousRead: false,
  mavenStrictPublication: false,
  version: "1",
};
const statistics = {
  generatedAt: "2026-10-07T00:00:00Z",
  totals: {
    requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
    denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
    usedBytes: 1024,
    objectCount: 1,
  },
  repositories: [
    {
      repositoryId: repository.id,
      name: repository.name,
      format: "maven",
      requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
      denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
      usedBytes: 1024,
      objectCount: 1,
    },
  ],
};

async function setup(page: Page, width: number) {
  await page.setViewportSize({ width, height: 1000 });
  await authenticateAsAdmin(page);
  await page.addInitScript((width) => {
    localStorage.setItem(
      "ag.console.locale",
      width === 390 ? "en-US" : "zh-CN",
    );
    localStorage.setItem("ag.console.theme", width === 390 ? "light" : "dark");
  }, width);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  return { pageErrors, consoleErrors };
}

async function geometry(page: Page) {
  expect(
    await page
      .locator("html")
      .evaluate((el) => el.scrollWidth - el.clientWidth),
  ).toBeLessThanOrEqual(0);
  const boxes = await page
    .locator(".ag-page-stack > *")
    .evaluateAll((elements) =>
      elements
        .map((el) => {
          const rect = el.getBoundingClientRect();
          const style = getComputedStyle(el);
          return {
            top: rect.top,
            bottom: rect.bottom,
            header: el.classList.contains("ag-page-header"),
            primary: el.classList.contains("ag-page-primary"),
            margin: Number.parseFloat(style.marginTop),
          };
        })
        .filter((box) => box.bottom > box.top),
    );
  for (let i = 1; i < boxes.length; i++) {
    const gap = boxes[i].top - boxes[i - 1].bottom;
    const expected = boxes[i - 1].header || boxes[i].primary ? 24 : 16;
    expect(gap).toBeGreaterThanOrEqual(expected - 1);
    expect(gap).toBeLessThanOrEqual(expected + 2);
  }
}

async function capture(page: Page, info: TestInfo, state: string) {
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: info.outputPath(`${state}.png`),
      fullPage: true,
      animations: "disabled",
    });
  }
}

function expectOnlyMockHttpErrors(errors: string[], statuses: number[]) {
  expect(
    errors.filter(
      (message) =>
        !statuses.some(
          (status) =>
            message ===
            `Failed to load resource: the server responded with a status of ${status} (${status === 503 ? "Service Unavailable" : status === 403 ? "Forbidden" : "Unauthorized"})`,
        ),
    ),
  ).toEqual([]);
}

for (const width of [1440, 390]) {
  const en = width === 390;
  test(`malformed overview stays local and one Retry restores shared sections at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let reads = 0;
    await page.route("**/api/v2/groups**", (route) =>
      route.fulfill({ json: { items: [] } }),
    );
    await page.route("**/api/v2/audits**", (route) =>
      route.fulfill({ json: [] }),
    );
    await page.route("**/api/v2/overview-statistics", (route) =>
      route.fulfill({
        json:
          ++reads === 1 ? { ...statistics, repositories: null } : statistics,
      }),
    );
    await page.goto("/");
    const activity = page.getByRole("alert").filter({
      hasText: en
        ? "Repository activity is unavailable"
        : "仓库活动暂时无法显示",
    });
    await expect(activity).toBeVisible();
    await expect(
      page.getByText(en ? "No audit records" : "暂无审计记录", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", {
        name: en ? "Overview" : "总览",
        exact: true,
      }),
    ).toBeVisible();
    await geometry(page);
    await capture(page, info, "overview-malformed");
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors.length).toBeGreaterThan(0);
    for (const message of errors.consoleErrors) {
      expect(message).toMatch(
        /Cannot read properties of null \(reading '(?:length|reduce)'\)|repositories is not iterable|above error occurred in the|Console section failed/,
      );
    }
    errors.consoleErrors.length = 0;
    await activity.getByRole("button", { name: en ? "Retry" : "重试" }).click();
    await expect(
      page.getByRole("link", { name: repository.name, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("group", { name: en ? "Page summary" : "页面摘要" }),
    ).toBeVisible();
    await page.getByTestId("storage-by-format-chart").scrollIntoViewIfNeeded();
    await expect(page.getByTestId("ant-design-pie-ready")).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
    expect(reads).toBe(2);
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
    await capture(page, info, "overview-recovered");
  });

  test(`cached detail shows a failed refresh and recovers in place at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let state: "initial" | "failed" | "recovered" = "initial";
    let failedReads = 0;
    await page.route(`**/api/v2/repositories/${repository.id}**`, (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith("/capabilities"))
        return route.fulfill({
          json: {
            format: "maven",
            type: "hosted",
            operations: ["read", "publish"],
          },
        });
      if (path.endsWith("/effective-access"))
        return route.fulfill({
          json: {
            permissions: {
              read: { allowed: true },
              write: { allowed: true },
              admin: { allowed: true },
            },
          },
        });
      if (path.endsWith("/capacity"))
        return route.fulfill({
          json: { usedBytes: 0, quotaBytes: 0, objectCount: 0 },
        });
      if (route.request().method() === "PATCH") {
        state = "failed";
        return route.fulfill({ json: { ...repository, version: "2" } });
      }
      if (state === "failed") {
        failedReads++;
        return route.fulfill({
          status: 503,
          json: { status: 503, message: "Synthetic refresh unavailable" },
        });
      }
      return route.fulfill({
        json: { ...repository, version: state === "recovered" ? "2" : "1" },
      });
    });
    await page.goto(`/repositories/${repository.id}?tab=settings`);
    await page
      .getByRole("button", { name: en ? "Save changes" : "保存更改" })
      .click();
    const warning = page
      .getByRole("alert")
      .filter({ hasText: "Synthetic refresh unavailable" });
    await expect(warning).toBeVisible();
    expect(failedReads).toBe(2);
    await expect(
      page.getByRole("heading", { name: repository.name, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("navigation", {
        name: en ? "Repository tasks" : "仓库任务",
      }),
    ).toBeVisible();
    await geometry(page);
    await capture(page, info, "detail-refresh-failed");
    state = "recovered";
    await warning.getByRole("button", { name: en ? "Retry" : "重试" }).click();
    await expect(warning).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: repository.name, exact: true }),
    ).toBeVisible();
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expectOnlyMockHttpErrors(errors.consoleErrors, [503]);
    await capture(page, info, "detail-refresh-recovered");
  });

  test(`deletion retries in the same confirmation and clears the failure at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let deletions = 0;
    await page.route("**/api/v2/formats", (route) =>
      route.fulfill({
        json: {
          items: [
            {
              format: "maven",
              repositoryTypes: ["hosted"],
              groupSupported: true,
              anonymousRead: true,
              hostedOperations: ["read", "publish"],
              proxyOperations: [],
            },
          ],
        },
      }),
    );
    await page.route("**/api/v2/repository-capacities", (route) =>
      route.fulfill({ json: [] }),
    );
    await page.route("**/api/v2/repositories?**", (route) =>
      route.fulfill({ json: { items: deletions < 2 ? [repository] : [] } }),
    );
    await page.route(`**/api/v2/repositories/${repository.id}`, (route) => {
      expect(route.request().method()).toBe("DELETE");
      return ++deletions === 1
        ? route.fulfill({
            status: 503,
            json: { status: 503, message: "Synthetic deletion unavailable" },
          })
        : route.fulfill({ status: 204 });
    });
    await page.goto("/repositories");
    await page
      .getByRole("button", {
        name: `${en ? "Delete" : "删除"} ${repository.name}`,
      })
      .click();
    const dialog = page.getByRole("dialog");
    await waitForModalOpen(dialog);
    const confirm = dialog.getByRole("button", {
      name: en ? /Delete$/ : /删\s*除$/,
    });
    await confirm.click();
    await expect(
      page.getByText("Synthetic deletion unavailable"),
    ).toBeVisible();
    await expect(confirm).toBeEnabled();
    await expect(confirm).not.toHaveClass(/ant-btn-loading/);
    await confirm.click();
    await expect(dialog).toHaveCount(0);
    await expect(
      page.getByText(en ? "No repositories" : "暂无仓库", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("Synthetic deletion unavailable")).toHaveCount(
      0,
    );
    expect(deletions).toBe(2);
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expectOnlyMockHttpErrors(errors.consoleErrors, [503]);
    await capture(page, info, "delete-recovered");
  });

  for (const status of [401, 403]) {
    test(`query ${status} uses the session path at ${width}px`, async ({
      page,
    }, info) => {
      const errors = await setup(page, width);
      let loggedOut = false;
      let reads = 0;
      await page.route("**/auth/session", (route) =>
        route.fulfill({
          json: loggedOut
            ? { authenticated: false }
            : {
                authenticated: true,
                identity: {
                  actor: "mock-admin",
                  kind: "local_session",
                  role: "admin",
                  administrator: true,
                },
              },
        }),
      );
      await page.route("**/auth/logout", (route) => {
        loggedOut = true;
        return route.fulfill({ status: 204 });
      });
      await page.route("**/auth/oidc/config", (route) =>
        route.fulfill({ json: { enabled: false } }),
      );
      await page.route("**/api/v2/formats", (route) =>
        route.fulfill({ json: { items: [] } }),
      );
      await page.route("**/api/v2/repository-capacities", (route) =>
        route.fulfill({ json: [] }),
      );
      await page.route("**/api/v2/repositories?**", (route) => {
        reads++;
        return route.fulfill({
          status,
          json: { status, message: "Synthetic authorization failure" },
        });
      });
      await page.goto("/repositories");
      if (status === 401) {
        await expect(page).toHaveURL(/\/login\?redirect=%2Frepositories$/);
        expect(
          await page.evaluate(() => localStorage.getItem("ag.console.token")),
        ).toBeNull();
        await expect(
          page.getByRole("heading", { name: repository.name, exact: true }),
        ).toHaveCount(0);
        expect(loggedOut).toBe(true);
      } else {
        await expect(page.getByRole("alert")).toContainText(
          "Synthetic authorization failure",
        );
        await expect(
          page.getByRole("button", { name: /mock-admin/ }),
        ).toBeVisible();
        expect(
          await page.evaluate(() => localStorage.getItem("ag.console.token")),
        ).toBe("mock-admin-token");
        expect(loggedOut).toBe(false);
        await geometry(page);
      }
      expect(reads).toBe(1);
      expect(errors.pageErrors).toEqual([]);
      expectOnlyMockHttpErrors(errors.consoleErrors, [status]);
      await capture(page, info, `session-${status}`);
    });
  }
}
