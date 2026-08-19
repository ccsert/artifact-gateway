import { expect, test, type Locator, type Page } from "@playwright/test";
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

test("public catalog empty state uses responsive theme artwork", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await useDarkChinesePreferences(page);
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({ json: { enabled: true, items: [] } }),
  );

  await page.goto("/browse");
  await expect(page.getByText("暂无公开仓库", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("link", { name: "管理登录" }).last(),
  ).toBeVisible();

  const artwork = page.locator('[data-empty-artwork="public-catalog"]');
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
  expect(desktop.width).toBeLessThanOrEqual(210);
  expect(desktop.density).toBeGreaterThanOrEqual(2.8);

  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(artwork).toHaveAttribute(
    "src",
    /empty-public-catalog-light\.webp(?:\?.*)?$/,
  );
  await page.setViewportSize({ width: 390, height: 844 });
  const mobile = await measureArtwork(artwork);
  expect(mobile.width).toBeLessThanOrEqual(174);
  expect(mobile.density).toBeGreaterThanOrEqual(3);
  expect(mobile.cornerAlpha).toBe(0);
  expect(mobile.overflowsViewport).toBe(false);
});

test("repository first-use artwork leads directly to creation", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await useDarkChinesePreferences(page);
  await authenticateAsAdmin(page);
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
});

test("repository filter-only empty state keeps the standard feedback", async ({
  page,
}) => {
  await useDarkChinesePreferences(page);
  await authenticateAsAdmin(page);
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
});
