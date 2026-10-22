import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { defaultSiteSettings } from "../src/lib/siteSettings";
import { nexusBatch, nexusCapabilities } from "../src/test/nexusBatchFixtures";
import { nexusSchedule } from "../src/test/nexusScheduleFixtures";

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
  await page.route("**/api/v2/migrations/nexus/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    expect(
      route.request().method(),
      "layout reads never dispatch a batch",
    ).toBe("GET");
    return route.fulfill({
      json: path.endsWith("/capabilities")
        ? nexusCapabilities
        : path.endsWith("/schedules")
          ? { items: [nexusSchedule], nextCursor: "" }
          : { items: [nexusBatch], nextCursor: "" },
    });
  });
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  return errors;
}

async function geometry(page: Page) {
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
    for (let i = 1; i < boxes.length; i++) {
      const expected = boxes[i - 1].header || boxes[i].primary ? 24 : 16;
      expect(boxes[i].top - boxes[i - 1].bottom).toBeGreaterThanOrEqual(
        expected - 1,
      );
      expect(boxes[i].top - boxes[i - 1].bottom).toBeLessThanOrEqual(
        expected + 2,
      );
    }
  const pairs = await page
    .locator(".ag-nexus-schedule-times, .ag-nexus-fields")
    .evaluateAll((grids) =>
      grids.map((grid) =>
        Array.from(grid.children)
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
  for (const pair of pairs) {
    if (pair.length !== 2) continue;
    if (Math.abs(pair[0].top - pair[1].top) <= 1) {
      expect(pair[1].left - pair[0].right).toBeGreaterThanOrEqual(15);
      expect(pair[1].left - pair[0].right).toBeLessThanOrEqual(18);
    } else {
      expect(pair[1].top - pair[0].bottom).toBeGreaterThanOrEqual(15);
      expect(pair[1].top - pair[0].bottom).toBeLessThanOrEqual(18);
    }
  }
}

for (const theme of ["gateway-dark", "gateway-light"])
  for (const width of [1440, 390]) {
    test(`recurring plan and explicit capacity keep geometry at ${width}px ${theme}`, async ({
      page,
    }, info) => {
      const errors = await setup(page, width, theme);
      await page.goto("/operations?tab=migrations&view=schedules");
      await expect(
        page.getByText("等待新容量观测", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("最近成功追平", { exact: true }),
      ).toBeVisible();
      await geometry(page);
      await page
        .getByRole("button", { name: "补充容量观测", exact: true })
        .click();
      await expect(
        page.getByRole("textbox", { name: "存储池可用字节", exact: true }),
      ).toHaveValue("");
      await expect(
        page.getByRole("button", { name: "提交新观测", exact: true }),
      ).toBeDisabled();
      await geometry(page);
      if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1")
        await page.screenshot({
          path: info.outputPath(`schedule-capacity-${width}.png`),
          fullPage: true,
          animations: "disabled",
        });
      await page
        .getByTestId("nexus-schedule-capacity")
        .getByRole("button", { name: "关闭", exact: true })
        .click();
      await page
        .getByRole("button", { name: "创建周期计划", exact: true })
        .click();
      await expect(
        page.getByRole("combobox", { name: "成功批次", exact: true }),
      ).toBeVisible();
      await geometry(page);
      if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1")
        await page.screenshot({
          path: info.outputPath(`schedule-create-${width}.png`),
          fullPage: true,
          animations: "disabled",
        });
      expect(errors).toEqual([]);
    });
  }

test("stopped and blocked plans show durable boundaries without dispatching", async ({
  page,
}) => {
  const errors = await setup(page, 390, "gateway-dark");
  await page.route("**/api/v2/migrations/nexus/schedules**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            ...nexusSchedule,
            state: "stopped",
            nextRunAt: null,
            activeBatchId: nexusBatch.id,
            lastError: "",
          },
          {
            ...nexusSchedule,
            id: "00000000-0000-4000-8000-000000000031",
            state: "blocked",
            lastError: "batch_requires_attention",
          },
        ],
        nextCursor: "",
      },
    }),
  );
  await page.goto("/operations?tab=migrations&view=schedules");
  const stopped = page.locator(".ag-nexus-schedule").first();
  await expect(
    stopped.getByText("已停止，不再触发", { exact: true }),
  ).toBeVisible();
  await expect(
    stopped.getByRole("link", { name: "查看当前批次" }),
  ).toBeVisible();
  await expect(
    stopped.getByRole("button", { name: "更新容量并恢复" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "更新容量并恢复" }),
  ).toBeVisible();
  await geometry(page);
  expect(errors).toEqual([]);
});
