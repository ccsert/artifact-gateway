import { expect, test, type Locator, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

function captureRuntimeErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  return errors;
}

async function verticalGap(upper: Locator, lower: Locator) {
  const upperBox = await upper.boundingBox();
  const lowerBox = await lower.boundingBox();
  if (!upperBox || !lowerBox) return -1;
  return Math.round(lowerBox.y - (upperBox.y + upperBox.height));
}

async function horizontalOverflow(page: Page) {
  return page
    .locator("html")
    .evaluate((element) =>
      Math.max(0, element.scrollWidth - element.clientWidth),
    );
}

function captureChartModuleRequests(page: Page) {
  const requests: string[] = [];
  page.on("request", (request) => {
    if (
      request.url().includes("dashboard-charts/DashboardPiePlot") ||
      request.url().includes("dashboard-charts/DashboardLinePlot")
    ) {
      requests.push(request.url());
    }
  });
  return requests;
}

async function loadDeferredCharts(page: Page, runtimeErrors?: string[]) {
  const storageChart = page.getByTestId("storage-by-format-chart");
  const trendCharts = page.getByTestId("dashboard-trend-chart");

  await storageChart.scrollIntoViewIfNeeded();
  if (runtimeErrors) {
    await expect
      .poll(() => runtimeErrors, {
        message: "dashboard chart modules must not trigger runtime errors",
      })
      .toEqual([]);
  }
  await expect(page.getByTestId("ant-design-pie-ready")).toBeVisible();
  await trendCharts.last().scrollIntoViewIfNeeded();
  await expect(page.getByTestId("ant-design-line-ready")).toHaveCount(2);
}

async function openDashboard(
  page: Page,
  loadCharts = true,
  runtimeErrors?: string[],
) {
  await authenticateAsAdmin(page);
  await page.addInitScript(
    (history) => {
      localStorage.setItem(
        "ag.console.dashboardHistory",
        JSON.stringify(history),
      );
    },
    [
      {
        t: Date.UTC(2026, 7, 19, 2),
        repos: 1,
        bytes: 1024 * 1024,
        objects: 4,
      },
      {
        t: Date.UTC(2026, 7, 19, 3),
        repos: 2,
        bytes: 2 * 1024 * 1024,
        objects: 8,
      },
      {
        t: Date.UTC(2026, 7, 19, 4),
        repos: 2,
        bytes: 3 * 1024 * 1024,
        objects: 12,
      },
    ],
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "repo-oci",
            name: "runtime-images",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
          {
            id: "repo-apt",
            name: "linux-packages",
            format: "apt",
            type: "proxy",
            state: "active",
            version: "1",
          },
        ],
      },
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/audits**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/repository-capacities**", (route) =>
    route.fulfill({
      json: [
        {
          repositoryId: "repo-oci",
          format: "oci",
          usedBytes: 3 * 1024 * 1024,
          objectCount: 12,
          quotaBytes: 0,
        },
        {
          repositoryId: "repo-apt",
          format: "apt",
          usedBytes: 1024 * 1024,
          objectCount: 4,
          quotaBytes: 0,
        },
      ],
    }),
  );

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  const storageChart = page.getByTestId("storage-by-format-chart");
  await expect(storageChart).toBeVisible();
  await expect(page.getByText("APT", { exact: true })).toBeVisible();
  const trendCharts = page.getByTestId("dashboard-trend-chart");
  await expect(trendCharts).toHaveCount(2);
  if (loadCharts) await loadDeferredCharts(page, runtimeErrors);
}

test("dashboard charts keep desktop cards separated and bounded", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const runtimeErrors = captureRuntimeErrors(page);
  await openDashboard(page, true, runtimeErrors);

  const pageStack = page.locator(".ag-page-stack").filter({
    has: page.getByRole("heading", { name: "总览" }),
  });
  const metrics = pageStack.getByRole("group", { name: "页面摘要" });
  const chartGrid = pageStack.locator(":scope > .ag-page-primary");
  const chartCards = chartGrid.locator(":scope > .ag-card");

  await expect(chartCards).toHaveCount(2);
  await expect.soft
    .poll(() => verticalGap(metrics, chartGrid))
    .toBeGreaterThanOrEqual(24);
  await expect.soft
    .poll(() => verticalGap(metrics, chartGrid))
    .toBeLessThanOrEqual(26);

  const [storageCard, trendCard] = await Promise.all([
    chartCards.nth(0).boundingBox(),
    chartCards.nth(1).boundingBox(),
  ]);
  expect(storageCard).not.toBeNull();
  expect(trendCard).not.toBeNull();
  expect(Math.abs(storageCard!.y - trendCard!.y)).toBeLessThanOrEqual(1);
  expect(Math.abs(storageCard!.height - trendCard!.height)).toBeLessThanOrEqual(
    1,
  );
  const columnGap = trendCard!.x - (storageCard!.x + storageCard!.width);
  expect(columnGap).toBeGreaterThanOrEqual(16);
  expect(columnGap).toBeLessThanOrEqual(18);
  expect(await horizontalOverflow(page)).toBe(0);
  expect.soft(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("dashboard-charts-desktop.png"),
      fullPage: true,
    });
  }
});

test("dashboard charts form a single-column mobile flow without overflow", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const runtimeErrors = captureRuntimeErrors(page);
  const chartModuleRequests = captureChartModuleRequests(page);
  await openDashboard(page, false);

  expect(chartModuleRequests).toEqual([]);

  const pageStack = page.locator(".ag-page-stack").filter({
    has: page.getByRole("heading", { name: "总览" }),
  });
  const chartGrid = pageStack.locator(":scope > .ag-page-primary");
  const chartCards = chartGrid.locator(":scope > .ag-card");

  await expect(chartCards).toHaveCount(2);
  await expect.soft
    .poll(() => verticalGap(chartCards.nth(0), chartCards.nth(1)))
    .toBeGreaterThanOrEqual(16);
  await expect.soft
    .poll(() => verticalGap(chartCards.nth(0), chartCards.nth(1)))
    .toBeLessThanOrEqual(18);
  expect(await horizontalOverflow(page)).toBe(0);

  await loadDeferredCharts(page, runtimeErrors);
  expect(chartModuleRequests).toHaveLength(2);
  expect.soft(runtimeErrors).toEqual([]);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("dashboard-charts-mobile.png"),
      fullPage: true,
    });
  }
});
