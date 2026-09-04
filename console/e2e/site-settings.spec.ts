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
        const roleBackground = (variable: string) => {
          const probe = document.createElement("span");
          probe.style.backgroundColor = `var(${variable})`;
          document.body.append(probe);
          const value = getComputedStyle(probe).backgroundColor;
          probe.remove();
          return value;
        };
        window.__themeTransitionCommit = {
          theme: document.documentElement.dataset.theme,
          bodyBackground: body.backgroundColor,
          cardBackground: card ? getComputedStyle(card).backgroundColor : null,
          primaryBackground: primaryButton
            ? getComputedStyle(primaryButton).backgroundColor
            : null,
          canvasRoleBackground: roleBackground("--ag-surface-canvas"),
          cardRoleBackground: roleBackground(
            "--ag-surface-container-translucent",
          ),
          primaryRoleBackground: roleBackground("--ag-action-primary"),
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
      const formChildren = Array.from(form.children, (child) =>
        child.getBoundingClientRect(),
      );
      const themeSection = form.querySelector<HTMLElement>(
        ".ag-site-theme-settings",
      )!;
      const themeHeading = themeSection.querySelector<HTMLElement>(
        ".ag-site-settings-section-heading",
      )!;
      const themeGrid = themeSection.querySelector<HTMLElement>(
        ".ag-theme-option-grid",
      )!;
      return {
        primaryGap: cardBox.top - headerBox.bottom,
        formGaps: formChildren
          .slice(1)
          .map((box, index) => box.top - formChildren[index].bottom),
        themeTopInset:
          themeHeading.getBoundingClientRect().top -
          themeSection.getBoundingClientRect().top,
        themeContentGap:
          themeGrid.getBoundingClientRect().top -
          themeHeading.getBoundingClientRect().bottom,
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
  for (const gap of desktopGeometry.formGaps) {
    expect(gap).toBeGreaterThanOrEqual(23);
    expect(gap).toBeLessThanOrEqual(25);
  }
  expect(desktopGeometry.themeTopInset).toBeGreaterThanOrEqual(24);
  expect(desktopGeometry.themeTopInset).toBeLessThanOrEqual(26);
  expect(desktopGeometry.themeContentGap).toBeGreaterThanOrEqual(15);
  expect(desktopGeometry.themeContentGap).toBeLessThanOrEqual(17);
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
      page.evaluate(() => window.__themeTransitionCommit?.theme ?? null),
    )
    .toBe("light");
  const transitionState = await page.evaluate(() => ({
    calls: window.__themeTransitionCalls,
    commit: window.__themeTransitionCommit,
  }));
  expect(transitionState.calls).toBe(1);
  expect(transitionState.commit).toMatchObject({
    theme: "light",
    bodyBackground: "rgb(245, 245, 245)",
    primaryBackground: "rgb(38, 73, 157)",
  });
  expect(transitionState.commit?.bodyBackground).toBe(
    transitionState.commit?.canvasRoleBackground,
  );
  expect(transitionState.commit?.cardBackground).toBe(
    transitionState.commit?.cardRoleBackground,
  );
  expect(transitionState.commit?.primaryBackground).toBe(
    transitionState.commit?.primaryRoleBackground,
  );

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

test("maximum branding values stay bounded from desktop through narrow zoom", async ({
  page,
}) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));

  const siteName =
    "Artifact Gateway Regional Distribution Control Plane Node".padEnd(80, "X");
  const themeName = "Enterprise Regional Operations Theme".padEnd(80, "Y");
  const availableThemes = defaultConsoleThemes.map((theme, index) =>
    index === 0 ? { ...theme, name: themeName } : theme,
  );
  await mockSiteSettings(page, {
    version: "11",
    siteName,
    logoUrl: "",
    brandMark: "ABCDEFGH",
    enabledThemeIds: availableThemes.map((theme) => theme.id),
    defaultThemeId: availableThemes[0].id,
    availableThemes,
    updatedAt: "2026-08-19T08:00:00Z",
  });
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({ json: { enabled: true, items: [] } }),
  );

  await page.setViewportSize({ width: 920, height: 900 });
  await page.goto("/login");
  const loginGeometry = await page
    .locator(".ag-login-brand-panel")
    .evaluate((panel) => {
      const title = panel.querySelector("h1")!.getBoundingClientRect();
      const note = panel
        .querySelector<HTMLElement>(".ag-login-security-note")!
        .getBoundingClientRect();
      const mark = panel.querySelector<HTMLElement>(".ag-brand-mark")!;
      const markText = mark.querySelector<HTMLElement>(".ag-brand-mark-text")!;
      const range = document.createRange();
      range.selectNodeContents(markText);
      return {
        titleBeforeNote: title.bottom <= note.top,
        markTextWidth: range.getBoundingClientRect().width,
        markAvailableWidth: mark.clientWidth - 6,
        horizontalOverflow:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      };
    });
  expect(loginGeometry.titleBeforeNote).toBe(true);
  expect(loginGeometry.markTextWidth).toBeLessThanOrEqual(
    loginGeometry.markAvailableWidth + 1,
  );
  expect(loginGeometry.horizontalOverflow).toBeLessThanOrEqual(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/browse");
  const publicHeader = page.locator(".ag-public-browse-header");
  const publicGeometry = await publicHeader.evaluate((header) => {
    const children = Array.from(header.children, (child) =>
      child.getBoundingClientRect(),
    );
    return {
      childrenOverlap:
        children.length >= 2 &&
        children[0].right > children[1].left &&
        children[0].bottom > children[1].top,
      horizontalOverflow:
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    };
  });
  expect(publicGeometry.childrenOverlap).toBe(false);
  expect(publicGeometry.horizontalOverflow).toBeLessThanOrEqual(0);

  await authenticateAsAdmin(page);
  await page.setViewportSize({ width: 320, height: 900 });
  await page.goto("/site-settings");
  await expect(page.getByText(themeName, { exact: true })).toBeVisible();
  const themeGeometry = await page
    .locator(".ag-theme-option")
    .first()
    .evaluate((option) => {
      const optionBox = option.getBoundingClientRect();
      const nameBox = option
        .querySelector<HTMLElement>(".ag-theme-option-name")!
        .getBoundingClientRect();
      return {
        nameInside:
          nameBox.left >= optionBox.left && nameBox.right <= optionBox.right,
        horizontalOverflow:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      };
    });
  expect(themeGeometry.nameInside).toBe(true);
  expect(themeGeometry.horizontalOverflow).toBeLessThanOrEqual(0);
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
      canvasRoleBackground: string;
      cardRoleBackground: string;
      primaryRoleBackground: string;
    } | null;
  }
}
