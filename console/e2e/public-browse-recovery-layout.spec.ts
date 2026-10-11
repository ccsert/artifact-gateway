import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { defaultSiteSettings } from "../src/lib/siteSettings";

const repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "synthetic-public-source",
  format: "raw",
  type: "hosted",
};
const artifact = {
  coordinate: "release.tgz",
  digest: `sha256:${"a".repeat(64)}`,
  size: 1024,
  createdAt: "2026-10-01T00:00:00Z",
};

async function setup(page: Page, width: number) {
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript((width) => {
    localStorage.setItem(
      "ag.console.locale",
      width === 390 ? "en-US" : "zh-CN",
    );
    localStorage.setItem("ag.console.theme", width === 390 ? "light" : "dark");
  }, width);
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
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
    .locator(".ag-public-browse-page > *")
    .evaluateAll((els) =>
      els.map((el) => {
        const rect = el.getBoundingClientRect();
        return {
          top: rect.top,
          bottom: rect.bottom,
          header: el.classList.contains("ag-page-header"),
          primary: el.classList.contains("ag-page-primary"),
        };
      }),
    );
  for (let i = 1; i < boxes.length; i++) {
    const gap = boxes[i].top - boxes[i - 1].bottom;
    // The established catalog baseline combines the header and hero's lead
    // offsets (32px). Repository workspaces have only one offset (24px).
    const expected =
      16 + (boxes[i - 1].header ? 8 : 0) + (boxes[i].primary ? 8 : 0);
    expect(gap).toBeGreaterThanOrEqual(expected - 1);
    expect(gap).toBeLessThanOrEqual(expected + 2);
  }
  const regions = await page
    .locator(
      "[data-public-browse-workspace] > section, [data-public-browse-workspace] > aside",
    )
    .evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom };
      }),
    );
  if (regions.length === 2) {
    const [left, right] = regions;
    const gap =
      right.left >= left.right
        ? right.left - left.right
        : right.top - left.bottom;
    expect(gap).toBeGreaterThanOrEqual(19);
    expect(gap).toBeLessThanOrEqual(21);
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

for (const width of [1440, 390]) {
  const en = width === 390;

  test(`public catalog render Retry refreshes its source at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let reads = 0;
    let broken = true;
    await page.route("**/api/v2/public/repositories", (route) => {
      ++reads;
      return route.fulfill({
        json: {
          enabled: true,
          items: [
            {
              ...repository,
              format: broken ? "future-format" : "raw",
            },
          ],
        },
      });
    });
    await page.goto("/browse");
    await expect(
      page.getByText(
        en ? "This section is unavailable" : "此区域暂时无法显示",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: en ? "Management sign in" : "管理登录" }),
    ).toBeVisible();
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors.length).toBeGreaterThan(0);
    expect(
      errors.consoleErrors.every((message) =>
        /reading 'icon'|above error occurred|Console section failed/.test(
          message,
        ),
      ),
    ).toBe(true);
    await geometry(page);
    await capture(page, info, "catalog-contained");
    errors.consoleErrors.length = 0;
    const beforeRetry = reads;
    broken = false;
    await page.getByRole("button", { name: en ? "Retry" : "重试" }).click();
    await expect(
      page.getByText(repository.name, { exact: true }),
    ).toBeVisible();
    expect(reads).toBe(beforeRetry + 1);
    await expect(page.getByRole("alert")).toHaveCount(0);
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
    await capture(page, info, "catalog-recovered");
  });

  test(`public artifact render failure keeps usage and recovers at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    let reads = 0;
    await page.route("**/api/v2/public/repositories", (route) =>
      route.fulfill({ json: { enabled: true, items: [repository] } }),
    );
    await page.route(
      `**/api/v2/repositories/${repository.id}/artifact-search**`,
      (route) =>
        route.fulfill({ json: { items: ++reads === 1 ? [null] : [artifact] } }),
    );
    await page.goto(`/browse?repository=${repository.id}`);
    await expect(
      page.getByText(
        en ? "This section is unavailable" : "此区域暂时无法显示",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByText(en ? "Register a Raw source" : "注册 Raw 源地址", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: en ? "Management sign in" : "管理登录" }),
    ).toBeVisible();
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors.length).toBeGreaterThan(0);
    expect(
      errors.consoleErrors.every((message) =>
        /reading 'coordinate'|above error occurred|Console section failed/.test(
          message,
        ),
      ),
    ).toBe(true);
    await geometry(page);
    await capture(page, info, "artifacts-contained");
    errors.consoleErrors.length = 0;
    await page.getByRole("button", { name: en ? "Retry" : "重试" }).click();
    await expect(
      page.getByText(artifact.coordinate, { exact: true }),
    ).toBeVisible();
    expect(reads).toBe(2);
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
    await capture(page, info, "artifacts-recovered");
  });

  test(`public cached artifacts survive refresh failure and retry at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(page, width);
    await page.clock.install();
    let reads = 0;
    let fail = false;
    await page.route("**/api/v2/public/repositories", (route) =>
      route.fulfill({ json: { enabled: true, items: [repository] } }),
    );
    await page.route(
      `**/api/v2/repositories/${repository.id}/artifact-search**`,
      (route) => {
        ++reads;
        return fail
          ? route.fulfill({
              status: 503,
              json: { status: 503, message: "synthetic refresh failed" },
            })
          : route.fulfill({ json: { items: [artifact] } });
      },
    );
    await page.goto(`/browse?repository=${repository.id}`);
    await expect(
      page.getByText(artifact.coordinate, { exact: true }),
    ).toBeVisible();
    await page.clock.fastForward(31_000);
    fail = true;
    await page
      .getByRole("button", { name: en ? "Public repositories" : "公开仓库" })
      .click();
    await page
      .getByRole("link", { name: en ? "Open repository" : "进入仓库" })
      .click();
    await page.clock.fastForward(1500);
    await expect(
      page.getByText("synthetic refresh failed", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(artifact.coordinate, { exact: true }),
    ).toBeVisible();
    const gaps = await page
      .locator("[data-public-browse-workspace] > section > div > div > *")
      .evaluateAll((els) =>
        els.map((el) => {
          const r = el.getBoundingClientRect();
          return { top: r.top, bottom: r.bottom };
        }),
      );
    expect(gaps).toHaveLength(2);
    expect(gaps[1].top - gaps[0].bottom).toBeGreaterThanOrEqual(16);
    expect(gaps[1].top - gaps[0].bottom).toBeLessThanOrEqual(18);
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expect(
      errors.consoleErrors.every(
        (message) =>
          message ===
          "Failed to load resource: the server responded with a status of 503 (Service Unavailable)",
      ),
    ).toBe(true);
    await capture(page, info, "artifacts-refresh-warning");
    errors.consoleErrors.length = 0;
    fail = false;
    const beforeRetry = reads;
    await page.getByRole("button", { name: en ? "Retry" : "重试" }).click();
    await expect(page.getByRole("alert")).toHaveCount(0);
    expect(reads).toBe(beforeRetry + 1);
    await expect(
      page.getByText(artifact.coordinate, { exact: true }),
    ).toBeVisible();
    await geometry(page);
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
    await capture(page, info, "artifacts-refresh-recovered");
  });
}
