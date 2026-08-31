import { expect, test, type Locator, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

async function measureArtwork(artwork: Locator) {
  return artwork.evaluate(async (node) => {
    const image = node as HTMLImageElement;
    await image.decode();
    const box = image.getBoundingClientRect();
    const canvas = document.createElement("canvas");
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) throw new Error("Artwork alpha canvas is unavailable");
    context.drawImage(image, 0, 0);
    const cornerAlpha = Math.max(
      context.getImageData(0, 0, 1, 1).data[3],
      context.getImageData(image.naturalWidth - 1, 0, 1, 1).data[3],
      context.getImageData(0, image.naturalHeight - 1, 1, 1).data[3],
      context.getImageData(
        image.naturalWidth - 1,
        image.naturalHeight - 1,
        1,
        1,
      ).data[3],
    );
    return {
      width: box.width,
      height: box.height,
      naturalWidth: image.naturalWidth,
      naturalHeight: image.naturalHeight,
      density: image.naturalWidth / box.width,
      cornerAlpha,
      overflowsViewport:
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    };
  });
}

async function measureEmptyStateFlow(emptyState: Locator) {
  return emptyState.evaluate((root) => {
    const rootBox = root.getBoundingClientRect();
    const artwork = root
      .querySelector<HTMLElement>("[data-empty-artwork]")!
      .getBoundingClientRect();
    const description = root
      .querySelector<HTMLElement>(".ant-empty-description")!
      .getBoundingClientRect();
    const footer = root
      .querySelector<HTMLElement>(".ant-empty-footer")!
      .getBoundingClientRect();
    return {
      artworkDescriptionGap: description.top - artwork.bottom,
      descriptionActionGap: footer.top - description.bottom,
      artworkDescriptionOverlap: artwork.bottom > description.top,
      descriptionActionOverlap: description.bottom > footer.top,
      contentInside:
        artwork.left >= rootBox.left &&
        artwork.right <= rootBox.right &&
        footer.left >= rootBox.left &&
        footer.right <= rootBox.right,
    };
  });
}

async function measureSplitEmptyState(emptyState: Locator) {
  return emptyState.evaluate((root) => {
    const rootBox = root.getBoundingClientRect();
    const artwork = root
      .querySelector<HTMLElement>("[data-empty-artwork]")!
      .getBoundingClientRect();
    const description = root
      .querySelector<HTMLElement>(".ant-empty-description")!
      .getBoundingClientRect();
    const footer = root
      .querySelector<HTMLElement>(".ant-empty-footer")!
      .getBoundingClientRect();
    return {
      artworkDescriptionGap: description.left - artwork.right,
      descriptionActionGap: footer.top - description.bottom,
      artworkDescriptionOverlap: artwork.right > description.left,
      descriptionActionOverlap: description.bottom > footer.top,
      contentInside:
        artwork.left >= rootBox.left &&
        artwork.right <= rootBox.right &&
        description.left >= rootBox.left &&
        description.right <= rootBox.right &&
        footer.left >= rootBox.left &&
        footer.right <= rootBox.right,
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
  expect(flow.artworkDescriptionGap).toBeGreaterThanOrEqual(12);
  expect(flow.artworkDescriptionGap).toBeLessThanOrEqual(22);
  expect(flow.descriptionActionGap).toBeGreaterThanOrEqual(12);
  expect(flow.descriptionActionGap).toBeLessThanOrEqual(26);
  expect(flow.artworkDescriptionOverlap).toBe(false);
  expect(flow.descriptionActionOverlap).toBe(false);
  expect(flow.contentInside).toBe(true);
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

test("public catalog empty state uses responsive theme artwork", async ({
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

  const emptyState = page.locator(".ag-empty-state-with-artwork").first();
  const artwork = emptyState.locator('[data-empty-artwork="public-catalog"]');
  await expect(artwork).toBeVisible();
  await expect(artwork).toHaveAttribute(
    "src",
    /empty-public-catalog\.webp(?:\?.*)?$/,
  );
  expect(await measureArtwork(artwork)).toMatchObject({
    naturalWidth: 600,
    naturalHeight: 400,
    cornerAlpha: 0,
    overflowsViewport: false,
  });
  const desktop = await measureArtwork(artwork);
  expect(desktop.width).toBeLessThanOrEqual(330);
  expect(desktop.density).toBeGreaterThanOrEqual(1.8);
  const desktopFlow = await measureSplitEmptyState(emptyState);
  expect(desktopFlow.artworkDescriptionGap).toBeGreaterThanOrEqual(28);
  expect(desktopFlow.descriptionActionGap).toBeGreaterThanOrEqual(12);
  expect(desktopFlow.descriptionActionGap).toBeLessThanOrEqual(26);
  expect(desktopFlow.artworkDescriptionOverlap).toBe(false);
  expect(desktopFlow.descriptionActionOverlap).toBe(false);
  expect(desktopFlow.contentInside).toBe(true);

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
  await expect(artwork).toHaveAttribute(
    "src",
    /empty-public-catalog-light\.webp(?:\?.*)?$/,
  );
  await page.setViewportSize({ width: 390, height: 844 });
  const mobile = await measureArtwork(artwork);
  expect(mobile.width).toBeLessThanOrEqual(190);
  expect(mobile.density).toBeGreaterThanOrEqual(3);
  expect(mobile.cornerAlpha).toBe(0);
  expect(mobile.overflowsViewport).toBe(false);
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

test("repository first-use artwork leads directly to creation", async ({
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

  const emptyState = page.locator(".ag-empty-state-with-artwork");
  const artwork = emptyState.locator('[data-empty-artwork="repositories"]');
  await expect(artwork).toHaveAttribute(
    "src",
    /empty-repositories\.webp(?:\?.*)?$/,
  );
  const desktop = await measureArtwork(artwork);
  expect(desktop).toMatchObject({
    naturalWidth: 600,
    naturalHeight: 400,
    cornerAlpha: 0,
    overflowsViewport: false,
  });
  expect(desktop.width).toBeLessThanOrEqual(210);
  expect(desktop.density).toBeGreaterThanOrEqual(2.8);
  expectBoundedEmptyStateFlow(await measureEmptyStateFlow(emptyState));

  await emptyState.getByRole("button", { name: "新建仓库" }).click();
  await expect(page.getByRole("dialog", { name: "新建仓库" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "新建仓库" })).toBeHidden();

  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(artwork).toHaveAttribute(
    "src",
    /empty-repositories-light\.webp(?:\?.*)?$/,
  );

  await page.setViewportSize({ width: 390, height: 844 });
  const mobile = await measureArtwork(artwork);
  expect(mobile.width).toBeLessThanOrEqual(174);
  expect(mobile.density).toBeGreaterThanOrEqual(3);
  expect(mobile.cornerAlpha).toBe(0);
  expect(mobile.overflowsViewport).toBe(false);
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
  await expect(page.locator("[data-empty-artwork]")).toHaveCount(0);
  expect(runtimeErrors.consoleErrors).toEqual([]);
  expect(runtimeErrors.pageErrors).toEqual([]);
});
