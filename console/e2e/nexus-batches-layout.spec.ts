import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { authenticateAsAdmin, authenticateAsMember } from "./support/auth";
import { waitForModalOpen } from "./support/motion";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import {
  migrationPreflight,
  migrationSource,
} from "../src/test/migrationFixtures";
import {
  nexusBatch,
  nexusCapabilities,
  nexusEntries,
  nexusTargets,
  nexusScanFailure,
} from "../src/test/nexusBatchFixtures";

async function setup(page: Page, width: number, theme: string) {
  await page.setViewportSize({ width, height: 1000 });
  await authenticateAsAdmin(page);
  await page.addInitScript((theme) => {
    localStorage.setItem("ag.console.locale", "zh-CN");
    localStorage.setItem("ag.console.theme.id", theme);
  }, theme);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/migration-sources**", (route) => {
    const path = new URL(route.request().url()).pathname;
    return route.fulfill({
      json: path.endsWith("/preflight")
        ? migrationPreflight
        : path.endsWith(migrationSource.id)
          ? migrationSource
          : { items: [migrationSource], nextCursor: "" },
    });
  });
  await page.route("**/api/v2/repositories**", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    return route.fulfill({
      json: nexusTargets.find((target) => target.id === id) ?? {
        items: nexusTargets,
        nextPageToken: "",
      },
    });
  });
  await page.route("**/api/v2/migrations/nexus/**", (route) => {
    expect(
      route.request().method(),
      "layout fixture never executes a task",
    ).toBe("GET");
    const path = new URL(route.request().url()).pathname;
    return route.fulfill({
      json: path.endsWith("/capabilities")
        ? nexusCapabilities
        : path.endsWith("/entries")
          ? { items: nexusEntries, nextCursor: "" }
          : path.endsWith(nexusBatch.id)
            ? nexusBatch
            : { items: [nexusBatch], nextCursor: "" },
    });
  });
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  return { pageErrors, consoleErrors };
}
async function geometry(page: Page, width: number) {
  await page.evaluate(() => document.fonts.ready);
  expect(
    await page
      .locator("html")
      .evaluate((node) => node.scrollWidth - node.clientWidth),
  ).toBeLessThanOrEqual(0);
  const stacks = await page.locator(".ag-page-stack").evaluateAll((elements) =>
    elements.map((element) =>
      Array.from(element.children)
        .map((child) => {
          const box = child.getBoundingClientRect();
          return {
            top: box.top,
            bottom: box.bottom,
            header: child.classList.contains("ag-page-header"),
            primary: child.classList.contains("ag-page-primary"),
          };
        })
        .filter((box) => box.bottom > box.top),
    ),
  );
  for (const boxes of stacks)
    for (let index = 1; index < boxes.length; index++) {
      const gap = boxes[index].top - boxes[index - 1].bottom;
      const expected =
        boxes[index - 1].header || boxes[index].primary ? 24 : 16;
      expect(gap).toBeGreaterThanOrEqual(expected - 1);
      expect(gap).toBeLessThanOrEqual(expected + 2);
    }
  const grids = await page
    .locator(".ag-nexus-fields, .ag-nexus-stat-grid")
    .evaluateAll((elements) =>
      elements.map((element) =>
        Array.from(element.children)
          .slice(0, 2)
          .map((child) => {
            const box = child.getBoundingClientRect();
            return {
              top: box.top,
              bottom: box.bottom,
              left: box.left,
              right: box.right,
            };
          }),
      ),
    );
  for (const boxes of grids) {
    if (boxes.length !== 2) continue;
    if (width > 640) {
      expect(boxes[1].left - boxes[0].right).toBeGreaterThanOrEqual(15);
      expect(boxes[1].left - boxes[0].right).toBeLessThanOrEqual(18);
      expect(Math.abs(boxes[1].top - boxes[0].top)).toBeLessThanOrEqual(1);
    } else {
      expect(boxes[1].top - boxes[0].bottom).toBeGreaterThanOrEqual(15);
      expect(boxes[1].top - boxes[0].bottom).toBeLessThanOrEqual(18);
    }
  }
}
async function capture(page: Page, info: TestInfo, name: string) {
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1")
    await page.screenshot({
      path: info.outputPath(`${name}.png`),
      fullPage: true,
      animations: "disabled",
    });
}
for (const theme of ["gateway-dark", "gateway-light"])
  for (const width of [1440, 390]) {
    test(`batch report and explicit capacity preserve geometry at ${width}px ${theme}`, async ({
      page,
    }, info) => {
      const errors = await setup(page, width, theme);
      await page.goto(
        `/operations?tab=migrations&view=batches&batch=${nexusBatch.id}`,
      );
      await expect(
        page.getByRole("heading", { name: "迁移验收观察报告" }),
      ).toBeVisible();
      await expect(
        page.getByText("本批次 target-only 冷客户端验收：未验证"),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "展开行", exact: true })
        .first()
        .click();
      await expect(
        page.getByText(nexusEntries[0].assets[0].sha256, { exact: true }),
      ).toBeVisible();
      await geometry(page, width);
      await capture(page, info, "batch-report");
      await page
        .getByRole("button", { name: "重新扫描并追平", exact: true })
        .click();
      await page.getByRole("button", { name: "运行预检", exact: true }).click();
      await expect(
        page.getByRole("combobox", { name: "releases 的目标仓库" }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "应用映射并创建批次" }),
      ).toBeDisabled();
      await expect(
        page.getByRole("textbox", { name: "存储池可用字节" }),
      ).toHaveValue("");
      await geometry(page, width);
      await capture(page, info, "batch-capacity");
      expect(errors.pageErrors).toEqual([]);
      expect(errors.consoleErrors).toEqual([]);
    });
  }
test("batch initial failures, empty state and unknown scan totals preserve read truth", async ({
  page,
}) => {
  const errors = await setup(page, 1440, "gateway-dark");
  await page.route("**/api/v2/migrations/nexus/batches?**", (route) =>
    route.fulfill({
      status: 503,
      json: { status: 503, message: "secret-never-render" },
    }),
  );
  await page.goto("/operations?tab=migrations&view=batches");
  await expect(page.getByText(/批次请求未完成/)).toBeVisible();
  await expect(page.getByRole("status")).toHaveCount(0);
  await expect(page.getByText("尚无迁移批次", { exact: true })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("secret-never-render");
  await geometry(page, 1440);
  await page.route("**/api/v2/migrations/nexus/batches?**", (route) =>
    route.fulfill({ json: { items: [], nextCursor: "" } }),
  );
  await page.getByRole("button", { name: "刷新批次", exact: true }).click();
  await expect(page.getByText("尚无迁移批次", { exact: true })).toBeVisible();
  await geometry(page, 1440);
  await page.route(
    `**/api/v2/migrations/nexus/batches/${nexusBatch.id}`,
    (route) =>
      route.fulfill({
        json: {
          ...nexusBatch,
          state: "scanning",
          scanComplete: false,
          totalBytes: null,
          entryCount: 0,
        },
      }),
  );
  await page.goto(`/operations?tab=migrations&batch=${nexusBatch.id}`);
  await expect(
    page.getByText("未知；完整清单尚未封存", { exact: true }),
  ).toBeVisible();
  expect(errors.pageErrors).toEqual([]);
  expect(
    errors.consoleErrors.filter(
      (value) =>
        !/^Failed to load resource: the server responded with a status of 503/.test(
          value,
        ),
    ),
  ).toEqual([]);
});
test("member batch deep links are denied before capabilities, tasks or source APIs", async ({
  page,
}) => {
  await authenticateAsMember(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({ json: defaultSiteSettings }),
  );
  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  let calls = 0;
  await page.route("**/api/v2/migrations/nexus/**", (route) => {
    calls++;
    return route.fulfill({ status: 403, json: { status: 403 } });
  });
  await page.goto(
    `/operations?tab=migrations&view=batches&batch=${nexusBatch.id}`,
  );
  await expect(page).toHaveURL(/\/repositories$/);
  expect(calls).toBe(0);
});

for (const width of [1440, 390])
  test(`unsealed scan diagnosis and fresh retry dialog preserve geometry at ${width}px`, async ({
    page,
  }, info) => {
    const errors = await setup(
      page,
      width,
      width === 1440 ? "gateway-dark" : "gateway-light",
    );
    await page.route(
      `**/api/v2/migrations/nexus/batches/${nexusBatch.id}`,
      (route) =>
        route.fulfill({
          json: {
            ...nexusBatch,
            state: "failed",
            scanComplete: false,
            totalBytes: null,
            metadataDiscovered: 5,
            entryCount: 0,
            manifestSha256: "",
            visibleIdentities: 0,
            counts: { ...nexusBatch.counts, committed: 0 },
            lastScanFailure: nexusScanFailure,
          },
        }),
    );
    await page.route(
      `**/api/v2/migrations/nexus/batches/${nexusBatch.id}/entries?**`,
      (route) => route.fulfill({ json: { items: [], nextCursor: "" } }),
    );
    await page.goto(
      `/operations?tab=migrations&view=batches&batch=${nexusBatch.id}`,
    );
    const diagnostic = page.getByRole("region", {
      name: "最近的持久扫描失败定位",
      exact: true,
    });
    await expect(
      diagnostic.getByText("source_missing", { exact: true }),
    ).toBeVisible();
    await expect(
      diagnostic.getByText(nexusScanFailure.sourcePath, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("未知；完整清单尚未封存", { exact: true }),
    ).toBeVisible();
    await geometry(page, width);
    await capture(page, info, "scan-diagnosis");
    await page
      .getByRole("button", { name: "重试可重试失败项", exact: true })
      .click();
    const dialog = page.getByRole("dialog", {
      name: "重试可重试失败项",
      exact: true,
    });
    await waitForModalOpen(dialog);
    await expect(
      dialog.getByRole("textbox", { name: "存储池可用字节", exact: true }),
    ).toHaveValue("");
    await expect(
      dialog.getByRole("button", { name: "重试可重试失败项", exact: true }),
    ).toBeDisabled();
    await geometry(page, width);
    await capture(page, info, "retry-unknown-capacity");
    expect(errors.pageErrors).toEqual([]);
    expect(errors.consoleErrors).toEqual([]);
  });
