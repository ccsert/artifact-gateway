import { expect, test, type Locator, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

async function measureEmptyStateFlow(emptyState: Locator) {
  return emptyState.evaluate((root) => {
    const rootBox = root.getBoundingClientRect();
    const icon = root
      .querySelector<HTMLElement>(".ag-empty-state-icon")!
      .getBoundingClientRect();
    const description = root
      .querySelector<HTMLElement>(".ant-empty-description")!
      .getBoundingClientRect();
    const footer = root
      .querySelector<HTMLElement>(".ant-empty-footer")!
      .getBoundingClientRect();
    return {
      iconWidth: icon.width,
      iconHeight: icon.height,
      iconDescriptionGap: description.top - icon.bottom,
      descriptionActionGap: footer.top - description.bottom,
      iconDescriptionOverlap: icon.bottom > description.top,
      descriptionActionOverlap: description.bottom > footer.top,
      contentInside:
        icon.left >= rootBox.left &&
        icon.right <= rootBox.right &&
        footer.left >= rootBox.left &&
        footer.right <= rootBox.right,
      overflowsViewport:
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    };
  });
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

function expectBoundedEmptyStateFlow(
  flow: Awaited<ReturnType<typeof measureEmptyStateFlow>>,
) {
  expect(flow.iconWidth).toBeGreaterThanOrEqual(36);
  expect(flow.iconWidth).toBeLessThanOrEqual(42);
  expect(flow.iconHeight).toBeCloseTo(flow.iconWidth, 2);
  expect(flow.iconDescriptionGap).toBeGreaterThanOrEqual(10);
  expect(flow.iconDescriptionGap).toBeLessThanOrEqual(20);
  expect(flow.descriptionActionGap).toBeGreaterThanOrEqual(12);
  expect(flow.descriptionActionGap).toBeLessThanOrEqual(26);
  expect(flow.iconDescriptionOverlap).toBe(false);
  expect(flow.descriptionActionOverlap).toBe(false);
  expect(flow.contentInside).toBe(true);
  expect(flow.overflowsViewport).toBe(false);
}

async function useDarkChinesePreferences(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme", "dark");
    localStorage.setItem("ag.console.locale", "zh-CN");
  });
}

async function mockFormatProfiles(page: Page) {
  await page.route("**/api/v2/formats", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            format: "oci",
            repositoryTypes: ["hosted", "proxy"],
            groupSupported: true,
            anonymousRead: true,
            hostedOperations: ["read", "publish", "browse"],
            proxyOperations: ["read", "browse"],
          },
        ],
      },
    }),
  );
}

async function mockDefaultSiteSettings(page: Page) {
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
        updatedAt: "2026-08-27T00:00:00Z",
      },
    }),
  );
}

async function mockAnonymousShell(page: Page) {
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await mockDefaultSiteSettings(page);
}

test("public catalog empty state stays quiet and responsive", async ({
  page,
}, testInfo) => {
  const runtimeErrors = captureRuntimeErrors(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await useDarkChinesePreferences(page);
  await mockAnonymousShell(page);
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({ json: { enabled: true, items: [] } }),
  );

  await page.goto("/browse");
  await expect(page.getByText("暂无公开仓库", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("link", { name: "管理登录" }).last(),
  ).toBeVisible();

  const emptyState = page.locator(".ag-public-catalog-empty");
  const icon = emptyState.locator(".ag-empty-state-icon");
  await expect(icon).toBeVisible();
  await expect(icon).toHaveAttribute("aria-hidden", "true");
  await expect(emptyState.locator("img")).toHaveCount(0);
  expectBoundedEmptyStateFlow(await measureEmptyStateFlow(emptyState));
  const darkIconColors = await icon.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, color: style.color };
  });

  const alignedSurfaces = await page
    .locator(".ag-public-browse-page")
    .evaluate((root) => {
      const hero = root.querySelector<HTMLElement>(".ag-page-primary")!;
      const empty = root.querySelector<HTMLElement>(
        ".ag-public-catalog-empty-surface",
      )!;
      const heroBox = hero.getBoundingClientRect();
      const emptyBox = empty.getBoundingClientRect();
      return {
        leftDelta: Math.abs(heroBox.left - emptyBox.left),
        rightDelta: Math.abs(heroBox.right - emptyBox.right),
        widthDelta: Math.abs(heroBox.width - emptyBox.width),
      };
    });
  expect(alignedSurfaces.leftDelta).toBeLessThanOrEqual(1);
  expect(alignedSurfaces.rightDelta).toBeLessThanOrEqual(1);
  expect(alignedSurfaces.widthDelta).toBeLessThanOrEqual(1);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("public-catalog-empty-desktop-dark.png"),
      fullPage: true,
    });
  }

  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  const lightIconColors = await icon.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, color: style.color };
  });
  expect(lightIconColors).not.toEqual(darkIconColors);
  await page.setViewportSize({ width: 390, height: 844 });
  expectBoundedEmptyStateFlow(await measureEmptyStateFlow(emptyState));
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("public-catalog-empty-mobile-light.png"),
      fullPage: true,
    });
  }
  expect(runtimeErrors.consoleErrors).toEqual([]);
  expect(runtimeErrors.pageErrors).toEqual([]);
});

test("repository first-use state leads directly to creation", async ({
  page,
}) => {
  const runtimeErrors = captureRuntimeErrors(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await useDarkChinesePreferences(page);
  await authenticateAsAdmin(page);
  await mockDefaultSiteSettings(page);
  await mockFormatProfiles(page);
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.goto("/repositories");
  await expect(page.getByText("暂无仓库", { exact: true })).toBeVisible();

  const emptyState = page.locator(".ag-empty-state");
  const icon = emptyState.locator(".ag-empty-state-icon");
  await expect(icon).toBeVisible();
  await expect(emptyState.locator("img")).toHaveCount(0);
  expectBoundedEmptyStateFlow(await measureEmptyStateFlow(emptyState));

  await emptyState.getByRole("button", { name: "新建仓库" }).click();
  await expect(page.getByRole("dialog", { name: "新建仓库" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "新建仓库" })).toBeHidden();

  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(icon).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  expectBoundedEmptyStateFlow(await measureEmptyStateFlow(emptyState));
  expect(runtimeErrors.consoleErrors).toEqual([]);
  expect(runtimeErrors.pageErrors).toEqual([]);
});

test("repository filter-only empty state keeps the standard feedback", async ({
  page,
}) => {
  const runtimeErrors = captureRuntimeErrors(page);
  await useDarkChinesePreferences(page);
  await authenticateAsAdmin(page);
  await mockDefaultSiteSettings(page);
  await mockFormatProfiles(page);
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "repo-archived",
            name: "archived-images",
            format: "oci",
            type: "hosted",
            state: "deleted",
            version: "1",
          },
        ],
      },
    }),
  );

  await page.goto("/repositories");
  await expect(
    page.getByText("暂无符合条件的仓库", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".ag-empty-state-icon")).toBeVisible();
  await expect(page.locator(".ag-empty-state img")).toHaveCount(0);
  expect(runtimeErrors.consoleErrors).toEqual([]);
  expect(runtimeErrors.pageErrors).toEqual([]);
});
