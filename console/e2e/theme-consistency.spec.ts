import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

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

test("theme and language preferences persist on the sign-in surface", async ({
  page,
}) => {
  await page.route("**/auth/session", (route) =>
    route.fulfill({ json: { authenticated: false } }),
  );
  await page.route("**/auth/oidc/config", (route) =>
    route.fulfill({ json: { enabled: false } }),
  );

  await page.goto("/login");
  await page.getByRole("button", { name: "切换到亮色模式" }).click();
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
      };
    });
  expect(loginSurfaces).toEqual({
    brandBackground: "rgb(17, 24, 29)",
    brandDescription: "rgb(161, 161, 170)",
    formBackground: "rgb(255, 255, 255)",
    heading: "rgb(24, 24, 27)",
  });

  await page.getByRole("button", { name: "语言" }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en-US");
  await expect(
    page.locator(".ag-login-panel").getByText("Console Sign In", {
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
    page.getByRole("button", { name: "Switch to dark mode" }),
  ).toBeVisible();
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
  await expect(page.getByText("仅风险命中时", { exact: true })).toBeVisible();
  const lifecycleArrowAlignments = await page
    .locator(".ag-lifecycle-stage:not(:last-child)")
    .evaluateAll((stages) =>
      stages.map((stage) => {
        const stageBox = stage.getBoundingClientRect();
        const iconBox = stage
          .querySelector(".ag-lifecycle-stage-icon")!
          .getBoundingClientRect();
        const arrowStyle = getComputedStyle(stage, "::after");
        const arrowCenter =
          stageBox.top +
          Number.parseFloat(arrowStyle.top) +
          Number.parseFloat(arrowStyle.height) / 2;
        const iconCenter = iconBox.top + iconBox.height / 2;
        return {
          display: arrowStyle.display,
          verticalDelta: Math.abs(arrowCenter - iconCenter),
        };
      }),
    );
  expect(lifecycleArrowAlignments).toHaveLength(4);
  for (const alignment of lifecycleArrowAlignments) {
    expect(alignment.display).not.toBe("none");
    expect(alignment.verticalDelta).toBeLessThanOrEqual(1);
  }
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
});
