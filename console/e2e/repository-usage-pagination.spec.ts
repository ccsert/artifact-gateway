import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const repositoryId = "22222222-2222-4222-8222-222222222222";

for (const width of [1440, 1024]) {
  test(`usage pagination fetches bounded server pages at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await authenticateAsAdmin(page);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await page.route("**/api/v2/site-settings", (route) =>
      route.fulfill({ json: defaultSiteSettings }),
    );
    const queries: string[] = [];
    await page.route(`**/api/v2/repositories/${repositoryId}**`, (route) => {
      const url = new URL(route.request().url());
      if (url.pathname.endsWith("/artifact-usage")) {
        queries.push(url.search);
        const q = url.searchParams.get("q") ?? "";
        const limit = Number(url.searchParams.get("limit") ?? 100);
        const offset = Number(url.searchParams.get("offset") ?? 0);
        const paths = Array.from(
          { length: 235 },
          (_, index) => `files/widget-${String(index).padStart(3, "0")}.zip`,
        ).filter((path) => path.includes(q));
        return route.fulfill({
          json: {
            repositoryId,
            totalCount: paths.length,
            totals: { resources: 235, downloadCount: 470, totalBytes: 481280 },
            generatedAt: "2026-10-06T00:00:00Z",
            items: paths.slice(offset, offset + limit).map((resource) => ({
              format: "raw",
              resource,
              downloadCount: 2,
              totalBytes: 2048,
              firstDownloadedAt: "2026-10-01T00:00:00Z",
              lastDownloadedAt: "2026-10-06T00:00:00Z",
            })),
          },
        });
      }
      if (url.pathname.endsWith("/effective-access")) {
        const allowed = {
          allowed: true,
          source: "administrator",
          reason: "administrator",
        };
        return route.fulfill({
          json: {
            permissions: { read: allowed, write: allowed, admin: allowed },
          },
        });
      }
      if (url.pathname.endsWith("/capacity"))
        return route.fulfill({
          json: {
            repositoryId,
            format: "raw",
            usedBytes: 481280,
            objectCount: 235,
            quotaBytes: 0,
          },
        });
      return route.fulfill({
        json: {
          id: repositoryId,
          name: "usage-fixture",
          format: "raw",
          type: "hosted",
          state: "active",
          version: "1",
          anonymousRead: false,
          mavenStrictPublication: false,
        },
      });
    });
    await page.goto(`/repositories/${repositoryId}?tab=usage`);
    await expect(page.getByText("files/widget-000.zip")).toBeVisible();
    await expect(page.locator("tbody tr.ant-table-row")).toHaveCount(20);
    await expect(page.getByText("第 1-20 项，共 235 项")).toBeVisible();
    await page.locator(".ant-pagination-item-2").click();
    await expect(page.getByText("files/widget-020.zip")).toBeVisible();
    expect(queries.at(-1)).toBe("?limit=20&offset=20");
    const search = page.getByRole("searchbox", { name: "搜索制品地址" });
    await search.fill("widget-23");
    await search.press("Enter");
    await expect(page.getByText("第 1-5 项，共 5 项")).toBeVisible();
    await expect(page.getByText(/全仓库：235 个地址/)).toBeVisible();
    expect(queries.at(-1)).toBe("?limit=20&offset=0&q=widget-23");
    await search.fill("");
    await search.press("Enter");
    await expect(page.locator("tbody tr.ant-table-row")).toHaveCount(20);
    const countBefore = queries.length;
    await page.getByRole("combobox").last().click();
    await page.getByTitle("50 条/页").click();
    await expect(page.locator("tbody tr.ant-table-row")).toHaveCount(50);
    expect(queries.length).toBe(countBefore + 1);
    expect(queries.at(-1)).toBe("?limit=50&offset=0");
    const geometry = await page
      .locator(".ag-page-stack")
      .last()
      .evaluate((element) => {
        const children = [...element.children].map((child) =>
          child.getBoundingClientRect(),
        );
        return {
          gaps: children
            .slice(1)
            .map((rect, i) => rect.top - children[i].bottom),
          overflow:
            document.documentElement.scrollWidth -
            document.documentElement.clientWidth,
        };
      });
    expect(geometry.overflow).toBeLessThanOrEqual(0);
    for (const gap of geometry.gaps) {
      expect(gap).toBeGreaterThanOrEqual(16);
      expect(gap).toBeLessThanOrEqual(18);
    }
    expect(errors).toEqual([]);
    await page.screenshot({
      path: testInfo.outputPath(`usage-pagination-${width}.png`),
      fullPage: true,
    });
  });
}
