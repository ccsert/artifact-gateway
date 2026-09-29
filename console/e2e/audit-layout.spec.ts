import { expect, test, type Locator, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

async function gap(upper: Locator, lower: Locator) {
  const [a, b] = await Promise.all([upper.boundingBox(), lower.boundingBox()]);
  expect(a).not.toBeNull();
  expect(b).not.toBeNull();
  return b!.y - (a!.y + a!.height);
}

async function noOverflow(page: Page) {
  expect(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(0);
}

test("audit log starts with filters and keeps the page controls at both widths", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/audits/page**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/audits");
  const header = page.locator(".ag-page-header");
  const card = page.locator(".ag-page-stack > .ag-card").first();
  await expect(card.getByRole("button", { name: "刷新" })).toBeVisible();
  await expect(page.getByRole("group", { name: "页面摘要" })).toHaveCount(0);
  expect(await gap(header, card)).toBeGreaterThanOrEqual(23);
  expect(await gap(header, card)).toBeLessThanOrEqual(25);
  await noOverflow(page);
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("audit-log-desktop.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  expect(await gap(header, card)).toBeGreaterThanOrEqual(23);
  expect(await gap(header, card)).toBeLessThanOrEqual(25);
  await noOverflow(page);
  expect(errors).toEqual([]);
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("audit-log-mobile.png"),
      fullPage: true,
    });
  }
});

test("audit retention has one policy form and preserves cleanup actions", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/audit-retention-policy", (route) =>
    route.fulfill({ json: { version: "3", enabled: true, keepDays: 90 } }),
  );
  await page.route("**/api/v2/audit-retention/jobs**", (route) =>
    route.fulfill({ json: [] }),
  );

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/audit-retention");
  const header = page.locator(".ag-page-header");
  const cards = page.locator(".ag-page-stack > .ag-card");
  await expect(cards).toHaveCount(2);
  await expect(page.getByText("策略设置")).toBeVisible();
  await expect(page.getByText("清理说明")).toHaveCount(0);
  await expect(page.getByRole("group", { name: "页面摘要" })).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "立即执行清理" }),
  ).toBeEnabled();
  expect(await gap(header, cards.first())).toBeGreaterThanOrEqual(23);
  expect(await gap(header, cards.first())).toBeLessThanOrEqual(25);
  expect(await gap(cards.first(), cards.last())).toBeGreaterThanOrEqual(15);
  expect(await gap(cards.first(), cards.last())).toBeLessThanOrEqual(17);
  await noOverflow(page);
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("audit-retention-desktop.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  expect(await gap(header, cards.first())).toBeGreaterThanOrEqual(23);
  expect(await gap(header, cards.first())).toBeLessThanOrEqual(25);
  expect(await gap(cards.first(), cards.last())).toBeGreaterThanOrEqual(15);
  expect(await gap(cards.first(), cards.last())).toBeLessThanOrEqual(17);
  await noOverflow(page);
  expect(errors).toEqual([]);
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("audit-retention-mobile.png"),
      fullPage: true,
    });
  }
});
