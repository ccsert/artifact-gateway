import { expect, test, type Locator, type Page } from "@playwright/test";
import { defaultConsoleThemes } from "../src/lib/consoleTheme";
import { authenticateAsAdmin } from "./support/auth";

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

function measureLifecycleInsets(root: HTMLElement) {
  const rootBox = root.getBoundingClientRect();
  const itemBoxes = Array.from(
    root.querySelectorAll<HTMLElement>(".ag-lifecycle-step"),
    (item) => item.getBoundingClientRect(),
  );
  return {
    top: Math.min(...itemBoxes.map((box) => box.top - rootBox.top)),
    right: Math.min(...itemBoxes.map((box) => rootBox.right - box.right)),
    bottom: Math.min(...itemBoxes.map((box) => rootBox.bottom - box.bottom)),
    left: Math.min(...itemBoxes.map((box) => box.left - rootBox.left)),
  };
}

async function measurePanelArtwork(panel: Locator) {
  return panel.evaluate(async (element) => {
    const style = getComputedStyle(element, "::before");
    const source = style.backgroundImage.match(/url\(["']?(.*?)["']?\)/)?.[1];
    if (!source) throw new Error("Login artwork URL is missing");

    const image = new Image();
    image.src = source;
    await image.decode();

    const panelBox = element.getBoundingClientRect();
    const sourceRatio = image.naturalWidth / image.naturalHeight;
    const panelRatio = panelBox.width / panelBox.height;
    const widthConstrained = panelRatio >= sourceRatio;
    const effectiveDensity = widthConstrained
      ? image.naturalWidth / panelBox.width
      : image.naturalHeight / panelBox.height;
    const renderedWidth = image.naturalWidth / effectiveDensity;
    const renderedHeight = image.naturalHeight / effectiveDensity;
    const cropFraction = widthConstrained
      ? 1 - panelBox.height / renderedHeight
      : 1 - panelBox.width / renderedWidth;

    return {
      source,
      sourceWidth: image.naturalWidth,
      sourceHeight: image.naturalHeight,
      panelWidth: panelBox.width,
      panelHeight: panelBox.height,
      effectiveDensity,
      cropFraction,
      backgroundSize: style.backgroundSize,
      pointerEvents: style.pointerEvents,
    };
  });
}

test("theme and language preferences persist on the sign-in surface", async ({
  page,
}) => {
  await mockDefaultSiteSettings(page);
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );

  await page.goto("/login");
  await expect
    .poll(() =>
      page
        .locator(".ag-login-brand-panel")
        .evaluate(
          (panel) => getComputedStyle(panel, "::before").backgroundImage,
        ),
    )
    .toContain("artifact-control-plane.webp");
  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() =>
      page
        .locator("body")
        .evaluate((element) => getComputedStyle(element).backgroundColor),
    )
    .toBe("rgb(246, 247, 249)");

  const loginSurfaces = await page
    .locator(".ag-login-frame")
    .evaluate((frame) => {
      const roleColor = (
        variable: string,
        property: "backgroundColor" | "color",
      ) => {
        const probe = document.createElement("span");
        if (property === "backgroundColor") {
          probe.style.backgroundColor = `var(${variable})`;
        } else {
          probe.style.color = `var(${variable})`;
        }
        document.body.append(probe);
        const value = getComputedStyle(probe)[property];
        probe.remove();
        return value;
      };
      const brandPanel = frame.querySelector<HTMLElement>(
        ".ag-login-brand-panel",
      );
      const brandDescription = brandPanel?.querySelector<HTMLElement>(
        ".ag-login-brand-copy p",
      );
      const formPanel = frame.querySelector<HTMLElement>(".ag-login-panel");
      const heading = formPanel?.querySelector<HTMLElement>("h2");
      return {
        brandBackground: brandPanel
          ? getComputedStyle(brandPanel).backgroundColor
          : null,
        brandDescription: brandDescription
          ? getComputedStyle(brandDescription).color
          : null,
        formBackground: formPanel
          ? getComputedStyle(formPanel).backgroundColor
          : null,
        heading: heading ? getComputedStyle(heading).color : null,
        containerRole: roleColor("--ag-surface-container", "backgroundColor"),
        secondaryRole: roleColor("--ag-content-secondary", "color"),
        strongRole: roleColor("--ag-content-strong", "color"),
      };
    });
  expect(loginSurfaces.formBackground).toBe(loginSurfaces.containerRole);
  expect(loginSurfaces.brandDescription).toBe(loginSurfaces.secondaryRole);
  expect(loginSurfaces.heading).toBe(loginSurfaces.strongRole);
  expect(loginSurfaces.brandBackground).not.toBe(loginSurfaces.formBackground);
  await expect
    .poll(() =>
      page
        .locator(".ag-login-brand-panel")
        .evaluate(
          (panel) => getComputedStyle(panel, "::before").backgroundImage,
        ),
    )
    .toContain("artifact-control-plane-light.webp");

  await page.getByRole("button", { name: "语言" }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
  await expect(
    page.locator(".ag-login-panel").getByText("Sign in to the console", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("Username & Password", { exact: true }),
  ).toBeVisible();

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
  await expect(
    page.getByRole("button", { name: /Choose theme.*Gateway Light/ }),
  ).toBeVisible();
});

test("sign-in artwork and source-derived backdrop stay theme-aware and size-appropriate", async ({
  browser,
  page,
}, testInfo) => {
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") runtimeErrors.push(message.text());
  });

  await mockDefaultSiteSettings(page);
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );

  await page.setViewportSize({ width: 1440, height: 900 });
  const artworkResponse = page.waitForResponse(
    (response) =>
      response.url().includes("artifact-control-plane.webp") && response.ok(),
  );
  await page.goto("/login");
  await artworkResponse;

  const brandPanel = page.locator(".ag-login-brand-panel");
  await expect(brandPanel).toBeVisible();
  const beams = page.locator('[data-kokonutui-component="beams-background"]');
  const beamsCanvas = beams.locator("canvas");
  await expect(beams).toBeVisible();
  await expect(brandPanel.locator("canvas")).toHaveCount(0);
  await expect(beamsCanvas).toHaveAttribute("data-ready", "true");
  const initialBeamFrame = await beamsCanvas.evaluate((canvas) =>
    (canvas as HTMLCanvasElement).toDataURL(),
  );
  await expect
    .poll(() =>
      beamsCanvas.evaluate((canvas) =>
        (canvas as HTMLCanvasElement).toDataURL(),
      ),
    )
    .not.toBe(initialBeamFrame);
  const beamsLayout = await beams.evaluate((backdrop) => {
    const panel = backdrop.parentElement!;
    const backdropBox = backdrop.getBoundingClientRect();
    const panelBox = panel.getBoundingClientRect();
    return {
      widthDelta: Math.abs(backdropBox.width - panelBox.width),
      heightDelta: Math.abs(backdropBox.height - panelBox.height),
      active: backdrop.getAttribute("data-active"),
    };
  });
  expect(beamsLayout.widthDelta).toBeLessThanOrEqual(1);
  expect(beamsLayout.heightDelta).toBeLessThanOrEqual(1);
  expect(beamsLayout.active).toBe("true");
  const brandLayout = await brandPanel.evaluate((panel) => {
    const panelBox = panel.getBoundingClientRect();
    const copyBox = panel
      .querySelector<HTMLElement>(".ag-login-brand-copy")
      ?.getBoundingClientRect();
    const noteBox = panel
      .querySelector<HTMLElement>(".ag-login-security-note")
      ?.getBoundingClientRect();
    return {
      copyInside:
        Boolean(copyBox) &&
        copyBox!.left >= panelBox.left &&
        copyBox!.right <= panelBox.right &&
        copyBox!.top >= panelBox.top,
      noteInside:
        Boolean(noteBox) &&
        noteBox!.left >= panelBox.left &&
        noteBox!.right <= panelBox.right &&
        noteBox!.bottom <= panelBox.bottom,
      surfacesOverlap: Boolean(
        copyBox && noteBox && copyBox.bottom > noteBox.top,
      ),
    };
  });
  expect(brandLayout).toMatchObject({
    copyInside: true,
    noteInside: true,
    surfacesOverlap: false,
  });
  const desktopDarkArtwork = await measurePanelArtwork(brandPanel);
  expect(desktopDarkArtwork).toMatchObject({
    sourceWidth: 1024,
    sourceHeight: 1536,
    backgroundSize: "cover",
    pointerEvents: "none",
  });
  expect(desktopDarkArtwork.source).toContain("artifact-control-plane.webp");
  expect(desktopDarkArtwork.effectiveDensity).toBeGreaterThanOrEqual(2);
  expect(desktopDarkArtwork.cropFraction).toBeLessThanOrEqual(0.08);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("login-artwork-desktop.png"),
      fullPage: true,
    });
  }

  await page.setViewportSize({ width: 920, height: 900 });
  await expect(brandPanel).toBeVisible();
  const narrowDesktopArtwork = await measurePanelArtwork(brandPanel);
  expect(narrowDesktopArtwork.effectiveDensity).toBeGreaterThanOrEqual(2);
  expect(narrowDesktopArtwork.cropFraction).toBeLessThanOrEqual(0.14);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);

  await page.setViewportSize({ width: 1440, height: 900 });
  const lightArtworkResponse = page.waitForResponse(
    (response) =>
      response.url().includes("artifact-control-plane-light.webp") &&
      response.ok(),
  );
  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await lightArtworkResponse;
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(beams).toHaveAttribute("data-color-mode", "light");
  await expect(beamsCanvas).toHaveAttribute("data-ready", "true");
  const lightControlSurfaces = await page.evaluate(() => {
    const roleBackground = (variable: string) => {
      const probe = document.createElement("span");
      probe.style.backgroundColor = `var(${variable})`;
      document.body.append(probe);
      const value = getComputedStyle(probe).backgroundColor;
      probe.remove();
      return value;
    };
    return {
      input: getComputedStyle(
        document.querySelector<HTMLElement>(
          ".ag-login-form .ant-input-affix-wrapper",
        )!,
      ).backgroundColor,
      modes: getComputedStyle(
        document.querySelector<HTMLElement>(".ag-login-modes")!,
      ).backgroundColor,
      containerRole: roleBackground("--ag-surface-container"),
      disabledRole: roleBackground("--ag-surface-disabled"),
    };
  });
  expect(lightControlSurfaces.input).toBe(lightControlSurfaces.containerRole);
  expect(lightControlSurfaces.modes).toBe(lightControlSurfaces.disabledRole);
  const desktopLightArtwork = await measurePanelArtwork(brandPanel);
  expect(desktopLightArtwork.source).toContain(
    "artifact-control-plane-light.webp",
  );
  expect(desktopLightArtwork.sourceWidth).toBe(desktopDarkArtwork.sourceWidth);
  expect(desktopLightArtwork.sourceHeight).toBe(
    desktopDarkArtwork.sourceHeight,
  );
  expect(desktopLightArtwork.effectiveDensity).toBeGreaterThanOrEqual(2);
  expect(desktopLightArtwork.cropFraction).toBeLessThanOrEqual(0.08);

  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.mouse.move(12, 12);
    await expect(page.getByRole("tooltip")).toBeHidden();
    await page.screenshot({
      path: testInfo.outputPath("login-artwork-light.png"),
      fullPage: true,
    });
  }

  const mobileContext = await browser.newContext({
    viewport: { width: 900, height: 900 },
  });
  const mobilePage = await mobileContext.newPage();
  mobilePage.on("pageerror", (error) => runtimeErrors.push(error.message));
  mobilePage.on("console", (message) => {
    if (message.type() === "error") runtimeErrors.push(message.text());
  });
  const mobileArtworkRequests: string[] = [];
  mobilePage.on("request", (request) => {
    if (request.url().includes("artifact-control-plane"))
      mobileArtworkRequests.push(request.url());
  });
  await mockDefaultSiteSettings(mobilePage);
  await mobilePage.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await mobilePage.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );
  await mobilePage.goto("/login");
  await mobilePage.waitForLoadState("networkidle");
  await expect(mobilePage.locator(".ag-login-brand-panel")).toBeHidden();
  await expect(mobilePage.locator(".ag-login-panel")).toBeVisible();
  expect(mobileArtworkRequests).toEqual([]);
  await expect(
    mobilePage.locator('[data-kokonutui-component="beams-background"]'),
  ).toHaveAttribute("data-active", "false");
  await expect(mobilePage.locator(".ag-login-beams canvas")).toHaveCount(0);
  await expect
    .poll(() =>
      mobilePage.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);

  await mobilePage.setViewportSize({ width: 390, height: 844 });
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await mobilePage.screenshot({
      path: testInfo.outputPath("login-artwork-mobile.png"),
      fullPage: true,
    });
  }
  await mobileContext.close();

  const reducedMotionContext = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const reducedMotionPage = await reducedMotionContext.newPage();
  reducedMotionPage.on("pageerror", (error) =>
    runtimeErrors.push(error.message),
  );
  reducedMotionPage.on("console", (message) => {
    if (message.type() === "error") runtimeErrors.push(message.text());
  });
  await mockDefaultSiteSettings(reducedMotionPage);
  await reducedMotionPage.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await reducedMotionPage.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );
  await reducedMotionPage.goto("/login");
  await reducedMotionPage.waitForLoadState("networkidle");
  await expect(
    reducedMotionPage.locator(".ag-login-brand-panel"),
  ).toBeVisible();
  await expect(
    reducedMotionPage.locator('[data-kokonutui-component="beams-background"]'),
  ).toHaveAttribute("data-active", "false");
  await expect(reducedMotionPage.locator(".ag-login-beams canvas")).toHaveCount(
    0,
  );
  await reducedMotionContext.close();
  expect(runtimeErrors).toEqual([]);
});

test("OIDC, password, and token sign-in modes share a stable layout", async ({
  page,
}) => {
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({
      json: {
        enabled: true,
        issuer: "https://identity.example.test/realms/artifact-gateway",
      },
    }),
  );

  await page.goto("/login");
  const frame = page.locator(".ag-login-frame");
  const oidcMode = page.getByRole("radio", { name: /企业登录/ });
  await expect(oidcMode).toBeChecked();
  await expect(page.getByText("由组织身份提供方统一认证")).toBeVisible();
  const oidcHeight = await frame.evaluate((element) =>
    Math.round(element.getBoundingClientRect().height),
  );

  await page
    .locator(".ag-login-modes label")
    .filter({ hasText: "访问令牌" })
    .click();
  await expect(page.getByPlaceholder("粘贴 Bearer Token…")).toBeVisible();
  const tokenHeight = await frame.evaluate((element) =>
    Math.round(element.getBoundingClientRect().height),
  );

  await page
    .locator(".ag-login-modes label")
    .filter({ hasText: "账号密码" })
    .click();
  await expect(page.getByPlaceholder("alice")).toBeVisible();
  const passwordHeight = await frame.evaluate((element) =>
    Math.round(element.getBoundingClientRect().height),
  );

  expect([oidcHeight, tokenHeight, passwordHeight]).toEqual([
    oidcHeight,
    oidcHeight,
    oidcHeight,
  ]);
});

test("public browse surfaces stay aligned with the dark console palette", async ({
  page,
}) => {
  await page.goto("/browse");
  await expect(
    page.getByRole("heading", { name: "查找并使用可信的公开制品" }),
  ).toBeVisible();

  const card = page.locator(".ant-card");
  await expect(card).not.toHaveCount(0);
  const surface = await card.first().evaluate((element) => {
    const style = getComputedStyle(element);
    const content = element.querySelector(".ant-card-body > div");
    return {
      background: style.backgroundColor,
      border: style.borderColor,
      contentPadding: content ? getComputedStyle(content).padding : null,
    };
  });

  expect(surface).toEqual({
    background: "rgba(24, 24, 27, 0.55)",
    border: "rgba(63, 63, 70, 0.35)",
    contentPadding: "40px",
  });
});

test("management tables keep action columns opaque and cards separated", async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await authenticateAsAdmin(page);
  await mockFormatProfiles(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-1",
            name: "images",
            format: "oci",
            type: "hosted",
            anonymousRead: true,
            state: "active",
            version: "1",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/repositories/repo-1/capacity**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        repositoryId: "repo-1",
        format: "oci",
        usedBytes: 1024,
        objectCount: 1,
        quotaBytes: 0,
      }),
    }),
  );
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          repositoryId: "repo-1",
          format: "oci",
          usedBytes: 1024,
          objectCount: 1,
          quotaBytes: 0,
        },
      ]),
    }),
  );
  await page.route("**/api/v2/users**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "user-1",
            name: "test",
            role: "admin",
            state: "active",
            version: "1",
            createdAt: "2026-08-05T00:00:00Z",
          },
        ],
      }),
    }),
  );

  await page.goto("/users");
  await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();

  const pageHeader = page
    .getByRole("heading", { name: "用户" })
    .locator("xpath=../..");
  const card = pageHeader.locator(
    "xpath=following-sibling::*[contains(@class, 'ant-card')][1]",
  );
  const gap = await pageHeader.evaluate((element) => {
    const next = element.nextElementSibling;
    if (!next) return 0;
    return (
      next.getBoundingClientRect().top - element.getBoundingClientRect().bottom
    );
  });
  await expect(card).toBeVisible();
  expect(gap).toBeGreaterThanOrEqual(16);

  await page.goto("/repositories");
  await expect(page.getByRole("heading", { name: "仓库" })).toBeVisible();
  await expect(
    page.getByRole("columnheader", { name: "操作", exact: true }),
  ).toBeVisible();
  const actionCell = page.locator(".ant-table-cell-fix-end").first();
  await expect(actionCell).toBeVisible();
  const actionSurface = await actionCell.evaluate((element) => {
    const style = getComputedStyle(element);
    return style.backgroundColor;
  });
  expect(actionSurface).toBe("rgb(20, 20, 23)");
});

test("deleted repositories stay archived unless explicitly requested", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await mockFormatProfiles(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-active",
            name: "active-repository",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
          {
            id: "repo-deleted",
            name: "archived-repository",
            format: "oci",
            type: "hosted",
            state: "deleted",
            version: "3",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/repositories/repo-active/capacity**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        repositoryId: "repo-active",
        format: "oci",
        usedBytes: 0,
        objectCount: 0,
        quotaBytes: 0,
      }),
    }),
  );
  await page.route("**/api/v2/repository-capacities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          repositoryId: "repo-active",
          format: "oci",
          usedBytes: 0,
          objectCount: 0,
          quotaBytes: 0,
        },
      ]),
    }),
  );

  await page.goto("/repositories");
  await expect(page.getByText("active-repository")).toBeVisible();
  await expect(page.getByText("archived-repository")).not.toBeVisible();

  await page.getByText("运行中与删除中", { exact: true }).click();
  await page.getByText("已删除", { exact: true }).click();
  await expect(page.getByText("archived-repository")).toBeVisible();
  await expect(page.getByText("active-repository")).not.toBeVisible();
});

test("dashboard excludes archived repositories from operational status", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: "repo-deleted",
            name: "archived-repository",
            format: "raw",
            type: "hosted",
            state: "deleted",
            version: "3",
          },
          {
            id: "repo-active",
            name: "active-repository",
            format: "oci",
            type: "hosted",
            state: "active",
            version: "1",
          },
        ],
      }),
    }),
  );
  await page.route("**/api/v2/groups**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );
  await page.route("**/api/v2/audits**", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: "[]" }),
  );
  await page.route("**/api/v2/repository-capacities**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          repositoryId: "repo-active",
          format: "oci",
          usedBytes: 1024,
          objectCount: 1,
          quotaBytes: 0,
        },
      ]),
    }),
  );

  await page.goto("/");
  await expect(page.getByText("平台运行正常")).toBeVisible();
  await expect(page.getByText("active-repository")).toBeVisible();
  await expect(page.getByText("archived-repository")).not.toBeVisible();
  await expect(
    page.getByRole("heading", { name: "可信制品路径" }),
  ).toBeVisible();
  await expect(page.locator(".ag-lifecycle-stage-title")).toHaveText([
    "来源",
    "扫描",
    "隔离闸门",
    "晋级与复制",
    "分发",
  ]);
  await expect(
    page.getByRole("button", { name: /仅风险命中时/ }),
  ).toBeVisible();
  const lifecycleSteps = page.locator(".ag-lifecycle-steps");
  await expect(lifecycleSteps).toHaveClass(/ant-steps-navigation/);
  await expect(lifecycleSteps.getByRole("button")).toHaveCount(5);
  const desktopLifecycleInsets = await lifecycleSteps.evaluate(
    measureLifecycleInsets,
  );
  for (const inset of Object.values(desktopLifecycleInsets)) {
    expect(inset).toBeGreaterThanOrEqual(16);
  }
  const lifecycleArrowAlignments = await page
    .locator(".ag-lifecycle-step:not(:last-child)")
    .evaluateAll((stages) =>
      stages.map((stage) => {
        const stageBox = stage.getBoundingClientRect();
        const arrowStyle = getComputedStyle(stage, "::after");
        const arrowCenter = stageBox.top + Number.parseFloat(arrowStyle.top);
        const stageCenter = stageBox.top + stageBox.height / 2;
        return {
          display: arrowStyle.display,
          verticalDelta: Math.abs(arrowCenter - stageCenter),
        };
      }),
    );
  expect(lifecycleArrowAlignments).toHaveLength(4);
  for (const alignment of lifecycleArrowAlignments) {
    expect(alignment.display).not.toBe("none");
    expect(alignment.verticalDelta).toBeLessThanOrEqual(1);
  }
  await expect
    .poll(() =>
      page
        .locator(".ag-lifecycle-step-conditional")
        .evaluate((stage) => getComputedStyle(stage, "::after").borderTopStyle),
    )
    .toBe("dashed");
  const firstLifecycleStep = lifecycleSteps.getByRole("button").first();
  const idleStepBackground = await firstLifecycleStep.evaluate(
    (step) => getComputedStyle(step).backgroundColor,
  );
  await firstLifecycleStep.hover();
  await expect
    .poll(() =>
      firstLifecycleStep.evaluate(
        (step) => getComputedStyle(step).backgroundColor,
      ),
    )
    .not.toBe(idleStepBackground);
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("dashboard-lifecycle-desktop.png"),
      fullPage: true,
    });
  }
  await expect(
    page
      .getByRole("group", { name: "页面摘要" })
      .locator(":scope > div")
      .first(),
  ).toContainText("1");

  await page.setViewportSize({ width: 720, height: 900 });
  await expect(lifecycleSteps).toHaveClass(/ant-steps-vertical/);
  await expect(lifecycleSteps).not.toHaveClass(/ant-steps-navigation/);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);

  await page.setViewportSize({ width: 390, height: 844 });
  const openNavigation = page.getByRole("button", { name: "打开导航" });
  await expect(openNavigation).toBeVisible();
  const triggerBox = await openNavigation.boundingBox();
  expect(triggerBox?.width).toBeGreaterThanOrEqual(44);
  expect(triggerBox?.height).toBeGreaterThanOrEqual(44);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(0);
  const firstMetricBox = await page
    .getByRole("group", { name: "页面摘要" })
    .locator(":scope > div")
    .first()
    .boundingBox();
  expect(firstMetricBox?.width).toBeGreaterThan(300);
  const mobileLifecycleInsets = await lifecycleSteps.evaluate(
    measureLifecycleInsets,
  );
  for (const inset of Object.values(mobileLifecycleInsets)) {
    expect(inset).toBeGreaterThanOrEqual(16);
  }
  if (process.env.CAPTURE_LAYOUT_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("dashboard-lifecycle-mobile.png"),
      fullPage: true,
    });
  }

  await openNavigation.click();
  const drawer = page.getByRole("dialog");
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("link", { name: /仓库/ })).toBeVisible();
  const closeNavigationBox = await drawer
    .getByRole("button", { name: "关闭导航" })
    .boundingBox();
  expect(closeNavigationBox?.width).toBeGreaterThanOrEqual(44);
  expect(closeNavigationBox?.height).toBeGreaterThanOrEqual(44);
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();

  const scanStep = lifecycleSteps.getByRole("button").nth(1);
  await scanStep.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/search$/);
});
