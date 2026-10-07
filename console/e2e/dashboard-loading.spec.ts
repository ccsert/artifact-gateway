import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const statistics = {
  generatedAt: "2026-10-07T00:00:00Z",
  totals: {
    requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
    denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
    objectCount: 1,
    usedBytes: 1024,
  },
  repositories: [
    {
      repositoryId: "synthetic-raw",
      name: "synthetic-raw",
      format: "raw",
      requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
      denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
      objectCount: 1,
      usedBytes: 1024,
    },
  ],
};

async function setup(page: Page) {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  return errors;
}

for (const width of [1440, 390]) {
  test(`storage renders before pending optional sources at ${width}px`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 });
    const errors = await setup(page);
    let release!: () => void;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const counts = { groups: 0, audits: 0, statistics: 0 };
    await page.route("**/api/v2/groups**", async (route) => {
      counts.groups++;
      await pending;
      await route.fulfill({ json: { items: [] } });
    });
    await page.route("**/api/v2/audits**", async (route) => {
      counts.audits++;
      await pending;
      await route.fulfill({ json: [] });
    });
    await page.route("**/api/v2/overview-statistics", async (route) => {
      counts.statistics++;
      await route.fulfill({ json: statistics });
    });
    await page.goto("/");
    await page.getByTestId("storage-by-format-chart").scrollIntoViewIfNeeded();
    await expect(page.getByTestId("ant-design-pie-ready")).toBeVisible();
    await expect(
      page.getByText("正在加载分组…", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("正在加载审计事件…", { exact: true }),
    ).toBeVisible();
    const stack = page.locator(".ag-page-stack").filter({
      has: page.getByRole("heading", { name: "总览", exact: true }),
    });
    const metrics = await stack
      .getByRole("group", { name: "页面摘要" })
      .boundingBox();
    const primary = await stack
      .locator(":scope > .ag-page-primary")
      .boundingBox();
    expect(primary!.y - metrics!.y - metrics!.height).toBeGreaterThanOrEqual(
      23,
    );
    expect(primary!.y - metrics!.y - metrics!.height).toBeLessThanOrEqual(26);
    const cards = stack.locator(":scope > .grid > .ag-card");
    const storage = await cards.nth(0).boundingBox();
    const audits = await cards.nth(1).boundingBox();
    if (width === 1440) {
      expect(audits!.x - storage!.x - storage!.width).toBeGreaterThanOrEqual(
        16,
      );
      expect(audits!.x - storage!.x - storage!.width).toBeLessThanOrEqual(18);
    } else {
      expect(audits!.y - storage!.y - storage!.height).toBeGreaterThanOrEqual(
        16,
      );
      expect(audits!.y - storage!.y - storage!.height).toBeLessThanOrEqual(18);
    }
    expect(
      await page
        .locator("html")
        .evaluate((el) => el.scrollWidth - el.clientWidth),
    ).toBeLessThanOrEqual(0);
    expect(counts).toEqual({ groups: 1, audits: 1, statistics: 1 });
    expect(errors).toEqual([]);
    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: info.outputPath(`dashboard-pending-${width}.png`),
        fullPage: true,
      });
    }
    release();
    await expect(page.getByText("暂无审计记录", { exact: true })).toBeVisible();
  });
}

test("a failed groups source preserves storage and retries only groups", async ({
  page,
}) => {
  await setup(page);
  let groups = 0;
  let overview = 0;
  let audits = 0;
  await page.route("**/api/v2/groups**", (route) => {
    groups++;
    return groups === 1
      ? route.fulfill({
          status: 500,
          json: { message: "synthetic groups unavailable" },
        })
      : route.fulfill({ json: { items: [] } });
  });
  await page.route("**/api/v2/audits**", (route) => {
    audits++;
    return route.fulfill({ json: [] });
  });
  await page.route("**/api/v2/overview-statistics", (route) => {
    overview++;
    return route.fulfill({ json: statistics });
  });
  await page.goto("/");
  await expect(page.getByText("synthetic groups unavailable")).toBeVisible();
  await page.getByTestId("storage-by-format-chart").scrollIntoViewIfNeeded();
  await expect(page.getByTestId("ant-design-pie-ready")).toBeVisible();
  await page.getByRole("button", { name: /重试/ }).click();
  await expect(page.getByText("共 0 个成员引用")).toBeVisible();
  expect({ groups, overview, audits }).toEqual({
    groups: 2,
    overview: 1,
    audits: 1,
  });
});
