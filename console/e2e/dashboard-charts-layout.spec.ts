import { expect, test, type Locator, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const statistics = {
  generatedAt: "2026-09-30T12:00:00Z",
  totals: {
    requests: { oneDay: 6, sevenDays: 9, thirtyDays: 17 },
    denied: { oneDay: 1, sevenDays: 2, thirtyDays: 2 },
    objectCount: 16,
    usedBytes: 4 * 1024 * 1024,
  },
  repositories: [
    {
      repositoryId: "repo-oci",
      name: "runtime-images",
      format: "oci",
      requests: { oneDay: 1, sevenDays: 7, thirtyDays: 11 },
      denied: { oneDay: 0, sevenDays: 1, thirtyDays: 1 },
      objectCount: 12,
      usedBytes: 3 * 1024 * 1024,
    },
    {
      repositoryId: "repo-apt",
      name: "linux-packages",
      format: "apt",
      requests: { oneDay: 5, sevenDays: 2, thirtyDays: 6 },
      denied: { oneDay: 1, sevenDays: 1, thirtyDays: 1 },
      objectCount: 4,
      usedBytes: 1024 * 1024,
    },
  ],
};

async function mockDashboard(page: Page, empty = false) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      json: {
        items: empty
          ? []
          : statistics.repositories.map((item) => ({
              id: item.repositoryId,
              name: item.name,
              format: item.format,
              type: "hosted",
              state: "active",
              version: "1",
            })),
      },
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/audits**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/overview-statistics", (route) =>
    route.fulfill({
      json: empty
        ? {
            generatedAt: statistics.generatedAt,
            totals: {
              requests: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
              denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
              objectCount: 0,
              usedBytes: 0,
            },
            repositories: [],
          }
        : statistics,
    }),
  );
}

async function verticalGap(upper: Locator, lower: Locator) {
  const upperBox = await upper.boundingBox();
  const lowerBox = await lower.boundingBox();
  expect(upperBox).not.toBeNull();
  expect(lowerBox).not.toBeNull();
  return Math.round(lowerBox!.y - (upperBox!.y + upperBox!.height));
}

test("dashboard switches real request windows and repository ranking", async ({
  page,
}) => {
  await mockDashboard(page);
  await page.goto("/");
  const metric = page.getByRole("group", { name: "页面摘要" });
  const requests = metric
    .locator(":scope > div")
    .filter({ hasText: "总请求量" });
  await expect(requests).toContainText("9");
  await expect(
    page.locator(".ant-table-tbody tr.ant-table-row").first(),
  ).toContainText("runtime-images");
  await page.getByText("1 天", { exact: true }).click();
  await expect(requests).toContainText("6");
  await expect(
    page.locator(".ant-table-tbody tr.ant-table-row").first(),
  ).toContainText("linux-packages");
  await page.getByText("30 天", { exact: true }).click();
  await expect(requests).toContainText("17");
  await expect(
    page.locator(".ant-table-tbody tr.ant-table-row").first(),
  ).toContainText("runtime-images");
  await expect(page.getByText("近期趋势")).toHaveCount(0);
});

test("dashboard empty statistics show a clear empty state", async ({
  page,
}) => {
  await mockDashboard(page, true);
  await page.goto("/");
  await expect(page.getByText("暂无仓库", { exact: true })).toBeVisible();
  await expect(
    page.getByText("创建仓库后，这里会展示请求量、对象数与存储占用。"),
  ).toBeVisible();
});

for (const viewport of [
  { width: 1440, height: 1000 },
  { width: 390, height: 844 },
]) {
  test(`dashboard layout stays bounded at ${viewport.width}px`, async ({
    page,
  }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await page.setViewportSize(viewport);
    await mockDashboard(page);
    await page.goto("/");
    const stack = page.locator(".ag-page-stack").filter({
      has: page.getByRole("heading", { name: "总览" }),
    });
    const metrics = stack.getByRole("group", { name: "页面摘要" });
    const primary = stack.locator(":scope > .ag-page-primary");
    await expect(primary).toBeVisible();
    expect(await verticalGap(metrics, primary)).toBeGreaterThanOrEqual(23);
    expect(await verticalGap(metrics, primary)).toBeLessThanOrEqual(26);
    await page.getByTestId("storage-by-format-chart").scrollIntoViewIfNeeded();
    await expect(page.getByTestId("ant-design-pie-ready")).toBeVisible();
    await page.evaluate(() => window.scrollTo(0, 0));
    expect(
      await page
        .locator("html")
        .evaluate((element) => element.scrollWidth - element.clientWidth),
    ).toBeLessThanOrEqual(0);
    expect(errors).toEqual([]);
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`dashboard-${viewport.width}.png`),
        fullPage: true,
      });
    }
  });
}
