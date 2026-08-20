import { expect, test, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

interface RGB {
  b: number;
  g: number;
  r: number;
}

function parseRGB(color: string): RGB {
  const [r, g, b] = color.match(/[\d.]+/g)?.map(Number) ?? [];
  if ([r, g, b].some((channel) => channel === undefined)) {
    throw new Error(`Unsupported color: ${color}`);
  }
  return { r, g, b };
}

function luminance({ r, g, b }: RGB) {
  const linear = [r, g, b].map((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
}

function contrast(foreground: string, background: string) {
  const values = [
    luminance(parseRGB(foreground)),
    luminance(parseRGB(background)),
  ].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

function captureRuntimeErrors(page: Page) {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  return { consoleErrors, pageErrors };
}

function expectOnlyInjectedRouteErrors(consoleErrors: string[]) {
  expect(consoleErrors).toHaveLength(2);
  for (const message of consoleErrors) {
    expect(message).toContain("Injected route failure");
  }
}

async function openInjectedRuntimeError(page: Page, theme: "dark" | "light") {
  await page.addInitScript((colorMode) => {
    localStorage.setItem("ag.console.theme", colorMode);
  }, theme);
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/site-settings", (route) =>
    route.fulfill({
      json: {
        version: "1",
        siteName: "Artifact Gateway",
        logoUrl: "",
        brandMark: "AG",
        enabledThemeIds: [
          "gateway-dark",
          "gateway-light",
          "aerok-dark",
          "aerok-light",
        ],
        defaultThemeId: "gateway-dark",
        availableThemes: defaultConsoleThemes,
        updatedAt: "2026-08-20T00:00:00Z",
      },
    }),
  );
  await page.route("**/src/pages/Dashboard.tsx*", (route) =>
    route.fulfill({
      body: `export function DashboardPage() { throw new TypeError("Injected route failure"); }`,
      contentType: "application/javascript",
    }),
  );

  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "页面加载失败" }),
  ).toBeVisible();
}

for (const scenario of [
  { height: 1000, name: "desktop light", theme: "light", width: 1440 },
  { height: 844, name: "mobile dark", theme: "dark", width: 390 },
] as const) {
  test(`route error page stays readable on ${scenario.name}`, async ({
    page,
  }, testInfo) => {
    const runtimeErrors = captureRuntimeErrors(page);
    await page.setViewportSize({
      width: scenario.width,
      height: scenario.height,
    });
    await openInjectedRuntimeError(page, scenario.theme);

    const card = page.locator(".ag-route-error-card");
    const title = page.getByRole("heading", { name: "页面加载失败" });
    const description = card.locator(".ag-route-error-description");
    const styles = await card.evaluate((element) => {
      const cardStyle = getComputedStyle(element);
      const titleStyle = getComputedStyle(
        element.querySelector("h1") as HTMLElement,
      );
      const descriptionStyle = getComputedStyle(
        element.querySelector(".ag-route-error-description") as HTMLElement,
      );
      return {
        background: cardStyle.backgroundColor,
        description: descriptionStyle.color,
        title: titleStyle.color,
      };
    });
    const geometry = await card.evaluate((element) => {
      const box = (selector: string) =>
        element.querySelector<HTMLElement>(selector)!.getBoundingClientRect();
      const card = element.getBoundingClientRect();
      const symbol = box(".ag-route-error-symbol");
      const title = box(".ag-route-error-title");
      const description = box(".ag-route-error-description");
      const detail = box(".ag-route-error-detail");
      const actions = box(".ag-route-error-actions");
      const primary = box(".ag-route-error-primary");
      const secondary = box(".ag-route-error-secondary");
      return {
        cardInsideViewport:
          card.left >= 0 &&
          card.right <= window.innerWidth &&
          card.top >= 0 &&
          card.bottom <= document.documentElement.scrollHeight,
        symbolTitleGap: title.top - symbol.bottom,
        titleDescriptionGap: description.top - title.bottom,
        descriptionDetailGap: detail.top - description.bottom,
        detailActionsGap: actions.top - detail.bottom,
        actionsOverlap:
          primary.right > secondary.left && primary.bottom > secondary.top,
      };
    });

    await expect(card).toBeVisible();
    await expect(title).toBeVisible();
    await expect(description).toBeVisible();
    await expect(page.getByText("技术详情")).toBeVisible();
    await expect(page.getByRole("button", { name: "重新加载" })).toBeVisible();
    await expect(
      page.getByRole("link", { name: "浏览公开制品" }),
    ).toBeVisible();
    expect(contrast(styles.title, styles.background)).toBeGreaterThanOrEqual(
      4.5,
    );
    expect(
      contrast(styles.description, styles.background),
    ).toBeGreaterThanOrEqual(4.5);
    expect(geometry.cardInsideViewport).toBe(true);
    expect(geometry.symbolTitleGap).toBeGreaterThanOrEqual(18);
    expect(geometry.symbolTitleGap).toBeLessThanOrEqual(22);
    expect(geometry.titleDescriptionGap).toBeGreaterThanOrEqual(10);
    expect(geometry.titleDescriptionGap).toBeLessThanOrEqual(14);
    expect(geometry.descriptionDetailGap).toBeGreaterThanOrEqual(18);
    expect(geometry.descriptionDetailGap).toBeLessThanOrEqual(22);
    expect(geometry.detailActionsGap).toBeGreaterThanOrEqual(26);
    expect(geometry.detailActionsGap).toBeLessThanOrEqual(30);
    expect(geometry.actionsOverlap).toBe(false);
    expect(
      await page
        .locator("html")
        .evaluate((element) =>
          Math.max(0, element.scrollWidth - element.clientWidth),
        ),
    ).toBe(0);
    expectOnlyInjectedRouteErrors(runtimeErrors.consoleErrors);
    expect(runtimeErrors.pageErrors).toEqual([]);

    if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`route-error-${scenario.theme}.png`),
        fullPage: true,
      });
    }
  });
}

test("route error recovery targets meet coarse-pointer sizing", async ({
  browser,
}) => {
  const context = await browser.newContext({
    hasTouch: true,
    viewport: { width: 390, height: 844 },
  });
  const page = await context.newPage();
  const runtimeErrors = captureRuntimeErrors(page);
  await openInjectedRuntimeError(page, "dark");

  for (const target of [
    page.locator(".ag-route-error-detail summary"),
    page.getByRole("button", { name: "重新加载" }),
    page.getByRole("link", { name: "浏览公开制品" }),
  ]) {
    const box = await target.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  }
  expectOnlyInjectedRouteErrors(runtimeErrors.consoleErrors);
  expect(runtimeErrors.pageErrors).toEqual([]);
  await context.close();
});
