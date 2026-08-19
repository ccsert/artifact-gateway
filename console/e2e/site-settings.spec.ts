import { expect, test, type Page } from "@playwright/test";
import type { ConsoleTheme } from "../src/client";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

interface MockSiteSettings {
  version: string;
  siteName: string;
  logoUrl: string;
  brandMark: string;
  enabledThemeIds: string[];
  defaultThemeId: string;
  availableThemes: ConsoleTheme[];
  updatedAt: string;
}

async function mockSiteSettings(page: Page, initial: MockSiteSettings) {
  let settings = initial;
  await page.route("**/api/v2/site-settings", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        headers: { ETag: settings.version },
        json: settings,
      });
      return;
    }
    const request = route.request();
    if (request.headers()["if-match"] !== settings.version) {
      await route.fulfill({ status: 412, json: { code: "version_conflict" } });
      return;
    }
    const update = request.postDataJSON() as Pick<
      MockSiteSettings,
      | "siteName"
      | "logoUrl"
      | "brandMark"
      | "enabledThemeIds"
      | "defaultThemeId"
    >;
    settings = {
      ...settings,
      ...update,
      version: String(Number(settings.version) + 1),
      updatedAt: "2026-08-19T06:00:00Z",
    };
    await route.fulfill({
      headers: { ETag: settings.version },
      json: settings,
    });
  });
}

test("site identity and theme transition stay coordinated across desktop and mobile", async ({
  page,
}, testInfo) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.addInitScript(() => {
    const transitionDocument = document as Document & {
      startViewTransition?: (update: () => void | Promise<void>) => {
        finished: Promise<void>;
        skipTransition: () => void;
      };
    };
    const original = transitionDocument.startViewTransition?.bind(document);
    Object.assign(window, {
      __themeTransitionCalls: 0,
      __themeTransitionCommit: null,
    });
    if (!original) return;
    transitionDocument.startViewTransition = (update) => {
      window.__themeTransitionCalls += 1;
      return original(async () => {
        await update();
        const body = getComputedStyle(document.body);
        const card = document.querySelector<HTMLElement>(".ag-card");
        const primaryButton =
          document.querySelector<HTMLElement>(".ant-btn-primary");
        window.__themeTransitionCommit = {
          theme: document.documentElement.dataset.theme,
          bodyBackground: body.backgroundColor,
          cardBackground: card ? getComputedStyle(card).backgroundColor : null,
          primaryBackground: primaryButton
            ? getComputedStyle(primaryButton).backgroundColor
            : null,
        };
      });
    };
  });
  await authenticateAsAdmin(page);
  await mockSiteSettings(page, {
    version: "7",
    siteName: "Forge Harbor",
    logoUrl: "",
    brandMark: "FH",
    enabledThemeIds: [
      "gateway-dark",
      "gateway-light",
      "aerok-dark",
      "aerok-light",
    ],
    defaultThemeId: "gateway-dark",
    availableThemes: defaultConsoleThemes,
    updatedAt: "2026-08-19T05:00:00Z",
  });
  await page.route("**/api/v2/formats", (route) =>
    route.fulfill({ json: { items: [] } }),
  );
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({ json: { items: [] } }),
  );

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/site-settings");
  await expect(page.getByRole("heading", { name: "站点设置" })).toBeVisible();
  await expect(page.locator(".ag-sider-desktop")).toContainText("Forge Harbor");

  const desktopGeometry = await page
    .locator(".ag-page-stack")
    .evaluate((root) => {
      const header = root.querySelector<HTMLElement>(".ag-page-header")!;
      const card = root.querySelector<HTMLElement>(".ag-card")!;
      const workspace = root.querySelector<HTMLElement>(
        ".ag-site-settings-workspace",
      )!;
      const form = workspace.querySelector<HTMLElement>(
        ".ag-site-settings-form",
      )!;
      const preview = workspace.querySelector<HTMLElement>(
        ".ag-site-settings-preview",
      )!;
      const headerBox = header.getBoundingClientRect();
      const cardBox = card.getBoundingClientRect();
      const formBox = form.getBoundingClientRect();
      const previewBox = preview.getBoundingClientRect();
      return {
        primaryGap: cardBox.top - headerBox.bottom,
        columnsOverlap: formBox.right > previewBox.left + 1,
        workspaceWidth: workspace.getBoundingClientRect().width,
        columnWidth: formBox.width + previewBox.width,
        horizontalOverflow:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      };
    });
  expect(desktopGeometry.primaryGap).toBeGreaterThanOrEqual(24);
  expect(desktopGeometry.primaryGap).toBeLessThanOrEqual(26);
  expect(desktopGeometry.columnsOverlap).toBe(false);
  expect(
    Math.abs(desktopGeometry.workspaceWidth - desktopGeometry.columnWidth),
  ).toBeLessThanOrEqual(1);
  expect(desktopGeometry.horizontalOverflow).toBeLessThanOrEqual(0);

  await page.getByLabel("站点名称").fill("Forge Harbor preview");
  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Aerok Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() =>
      page.evaluate(() => ({
        calls: window.__themeTransitionCalls,
        commit: window.__themeTransitionCommit,
      })),
    )
    .toEqual({
      calls: 1,
      commit: {
        theme: "light",
        bodyBackground: "rgb(245, 245, 245)",
        cardBackground: "rgb(255, 255, 255)",
        primaryBackground: "rgb(38, 73, 157)",
      },
    });

  const siteName = page.getByLabel("站点名称");
  const brandMark = page.getByLabel("品牌标识");
  await siteName.fill("Acme Packages");
  await brandMark.fill("AC");
  await page.getByRole("button", { name: "保存并应用" }).click();
  await expect(page.locator(".ag-sider-desktop")).toContainText(
    "Acme Packages",
  );
  await expect(page).toHaveTitle("Acme Packages Console");
  await expect(page.getByText("站点设置已生效")).toBeVisible();

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("site-settings-desktop-light.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(page.getByRole("heading", { name: "站点设置" })).toBeVisible();
  const mobileGeometry = await page
    .locator(".ag-site-settings-workspace")
    .evaluate((workspace) => {
      const form = workspace.querySelector<HTMLElement>(
        ".ag-site-settings-form",
      )!;
      const preview = workspace.querySelector<HTMLElement>(
        ".ag-site-settings-preview",
      )!;
      const formBox = form.getBoundingClientRect();
      const previewBox = preview.getBoundingClientRect();
      return {
        previewBelowForm: previewBox.top >= formBox.bottom - 1,
        horizontalOverflow:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      };
    });
  expect(mobileGeometry.previewBelowForm).toBe(true);
  expect(mobileGeometry.horizontalOverflow).toBeLessThanOrEqual(0);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("site-settings-mobile-light.png"),
      fullPage: true,
    });
  }

  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});

declare global {
  interface Window {
    __themeTransitionCalls: number;
    __themeTransitionCommit: {
      theme?: string;
      bodyBackground: string;
      cardBackground: string | null;
      primaryBackground: string | null;
    } | null;
  }
}
