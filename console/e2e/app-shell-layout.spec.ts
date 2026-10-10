import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";
import { mockDefaultSiteSettings } from "./support/siteSettings";
import { mockGateway } from "./visual/mockGateway";
import { repositories } from "./visual/fixtures";

async function shellGeometry(page: Page) {
  return page.evaluate(() => {
    const element = (selector: string) => {
      const element = document.querySelector<HTMLElement>(selector);
      if (!element) throw new Error(`Missing ${selector}`);
      return element;
    };
    const box = (selector: string) => element(selector).getBoundingClientRect();
    const center = (rect: DOMRect) => rect.left + rect.width / 2;
    const sider = box(".ag-sider-desktop");
    const main = box(".ag-shell-main");
    const nav = box(".ag-desktop-nav");
    const selected = box(".ag-desktop-nav .ant-menu-item-selected");
    const selectedIcon = box(
      ".ag-desktop-nav .ant-menu-item-selected .anticon",
    );
    const brand = box(".ag-sider-desktop .ag-brand-mark");
    const toggle = box(".ag-sider-desktop .ag-sider-toggle");
    const navElement = element(".ag-desktop-nav");
    const siderElement = element(".ag-sider-desktop");
    const mainElement = element(".ag-shell-main");

    return {
      siderLeft: sider.left,
      siderRight: sider.right,
      siderWidth: sider.width,
      mainLeft: main.left,
      navRight: nav.right,
      navOverflow: navElement.scrollWidth - navElement.clientWidth,
      selectedLeft: selected.left,
      selectedRight: selected.right,
      selectedTop: selected.top,
      selectedCenter: center(selected),
      iconCenter: center(selectedIcon),
      brandCenter: center(brand),
      toggleCenter: center(toggle),
      groupCount: document.querySelectorAll(
        ".ag-desktop-nav .ant-menu-item-group-title",
      ).length,
      itemCount: document.querySelectorAll(".ag-desktop-nav .ant-menu-item")
        .length,
      siderTransition: getComputedStyle(siderElement).transitionDuration,
      mainTransition: getComputedStyle(mainElement).transitionDuration,
    };
  });
}

test("desktop navigation collapses within one aligned and stable rail", async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });

  await page.setViewportSize({ width: 1440, height: 900 });
  await authenticateAsAdmin(page);
  await mockDefaultSiteSettings(page);
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme", "dark");
    localStorage.setItem("ag.console.locale", "zh-CN");
    localStorage.removeItem("ag:sider-collapsed");
  });
  await page.goto("/search");

  const sider = page.locator(".ag-sider-desktop");
  await expect(sider).toHaveAttribute("data-collapsed", "false");
  const expanded = await shellGeometry(page);
  expect(expanded.siderWidth).toBe(224);
  expect(expanded.mainLeft).toBe(224);
  expect(expanded.groupCount).toBe(3);
  expect(expanded.itemCount).toBeGreaterThan(8);

  await page.getByRole("button", { name: "收起导航" }).click();
  await expect(sider).toHaveAttribute("data-collapsed", "true");

  const motionSamples = await sider.evaluate(async (element) => {
    const samples: number[] = [];
    for (let index = 0; index < 16; index += 1) {
      await new Promise<void>((resolve) =>
        requestAnimationFrame(() => resolve()),
      );
      samples.push(element.getBoundingClientRect().width);
    }
    return samples;
  });
  for (let index = 1; index < motionSamples.length; index += 1) {
    expect(motionSamples[index]).toBeLessThanOrEqual(
      motionSamples[index - 1] + 0.5,
    );
  }

  await expect
    .poll(async () => (await shellGeometry(page)).siderWidth)
    .toBe(80);
  const collapsed = await shellGeometry(page);
  const railCenter = collapsed.siderLeft + collapsed.siderWidth / 2;
  expect(collapsed.mainLeft).toBe(80);
  expect(collapsed.navRight).toBeLessThanOrEqual(collapsed.siderRight);
  expect(collapsed.navOverflow).toBeLessThanOrEqual(0);
  expect(collapsed.selectedLeft).toBeGreaterThanOrEqual(collapsed.siderLeft);
  expect(collapsed.selectedRight).toBeLessThanOrEqual(collapsed.siderRight);
  expect(Math.abs(collapsed.selectedCenter - railCenter)).toBeLessThanOrEqual(
    1,
  );
  expect(Math.abs(collapsed.iconCenter - railCenter)).toBeLessThanOrEqual(1);
  expect(Math.abs(collapsed.brandCenter - railCenter)).toBeLessThanOrEqual(1);
  expect(Math.abs(collapsed.toggleCenter - railCenter)).toBeLessThanOrEqual(1);
  expect(collapsed.groupCount).toBe(expanded.groupCount);
  expect(collapsed.itemCount).toBe(expanded.itemCount);
  expect(Math.abs(collapsed.selectedTop - expanded.selectedTop)).toBeLessThan(
    1,
  );
  expect(collapsed.siderTransition).toBe("0.22s");
  expect(collapsed.mainTransition).toBe("0.22s");
  expect(
    await page.evaluate(
      () => document.body.scrollWidth - document.body.clientWidth,
    ),
  ).toBe(0);

  if (process.env.CAPTURE_SIDER_LAYOUT === "1") {
    await page.screenshot({
      path: testInfo.outputPath("sider-collapsed-dark.png"),
      fullPage: true,
    });
  }

  await page.getByRole("button", { name: "展开导航" }).click();
  await expect
    .poll(async () => (await shellGeometry(page)).siderWidth)
    .toBe(224);
  const restored = await shellGeometry(page);
  expect(restored.mainLeft).toBe(224);
  expect(restored.groupCount).toBe(expanded.groupCount);
  expect(restored.itemCount).toBe(expanded.itemCount);
  expect(Math.abs(restored.selectedTop - expanded.selectedTop)).toBeLessThan(1);

  await page.getByRole("button", { name: /选择主题.*Gateway Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Light/ }).click();
  await page.getByRole("button", { name: "收起导航" }).click();
  await expect
    .poll(async () => (await shellGeometry(page)).siderWidth)
    .toBe(80);
  if (process.env.CAPTURE_SIDER_LAYOUT === "1") {
    await page.screenshot({
      path: testInfo.outputPath("sider-collapsed-light.png"),
      fullPage: true,
    });
  }

  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});

for (const width of [1440, 390]) {
  test(`command palette reveals keyboard selection and later repositories at ${width}px`, async ({
    page,
  }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.addInitScript(() =>
      localStorage.setItem("ag.console.locale", "zh-CN"),
    );
    const gateway = await mockGateway(page, { authenticated: true });
    await page.goto("/repositories");
    await expect(page.getByText("cargo-hosted", { exact: true })).toBeVisible();
    const tokens: (string | null)[] = [];
    let failFirstRequest = true;
    await page.route(
      (url) => url.pathname === "/api/v2/repositories",
      async (route) => {
        const token = new URL(route.request().url()).searchParams.get(
          "pageToken",
        );
        tokens.push(token);
        if (failFirstRequest) {
          failFirstRequest = false;
          await route.fulfill({
            status: 503,
            json: {
              code: "temporarily_unavailable",
              message: "Repository list unavailable",
            },
          });
          return;
        }
        await route.fulfill({
          json:
            token === "opaque-next"
              ? {
                  items: [
                    {
                      ...repositories[0],
                      id: "later-release",
                      name: "later-release",
                    },
                  ],
                }
              : {
                  items: Array.from({ length: 100 }, (_, index) => ({
                    ...repositories[0],
                    id: `repo-${index}`,
                    name: `repo-${index}`,
                  })),
                  nextPageToken: "opaque-next",
                },
        });
      },
    );
    await page.keyboard.press("ControlOrMeta+k");
    const input = page.getByRole("combobox", { name: "命令面板" });
    const alert = page.getByRole("alert");
    await expect(alert).toContainText("Repository list unavailable");
    const errorBounds = await alert.boundingBox();
    expect(errorBounds!.x).toBeGreaterThanOrEqual(0);
    expect(errorBounds!.x + errorBounds!.width).toBeLessThanOrEqual(width);
    if (process.env.CAPTURE_SIDER_LAYOUT === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`command-palette-error-${width}.png`),
        fullPage: true,
      });
    }
    await page.getByRole("button", { name: /重\s*试/ }).click();
    await expect(alert).toHaveCount(0);
    await expect.poll(() => tokens).toEqual([null, null, "opaque-next"]);
    await input.press("ArrowDown");
    for (let index = 0; index < 18; index += 1) await input.press("ArrowDown");
    await expect
      .poll(() =>
        page
          .locator('.ag-command-option[aria-selected="true"]')
          .evaluate((option) => {
            const list = option.closest(".ag-command-list")!;
            const bounds = list.getBoundingClientRect();
            const selected = option.getBoundingClientRect();
            return (
              selected.top >= bounds.top && selected.bottom <= bounds.bottom
            );
          }),
      )
      .toBe(true);
    await input.fill("later-release");
    await expect(
      page.getByRole("option", { name: /^later-release/ }),
    ).toHaveAttribute("aria-selected", "true");
    await input.dispatchEvent("keydown", {
      key: "Enter",
      isComposing: true,
      keyCode: 229,
    });
    await expect(input).toBeVisible();
    await expect(page).toHaveURL(/\/repositories$/);
    expect(
      await page.evaluate(
        () => document.body.scrollWidth - document.body.clientWidth,
      ),
    ).toBe(0);
    if (process.env.CAPTURE_SIDER_LAYOUT === "1") {
      await page.screenshot({
        path: testInfo.outputPath(`command-palette-${width}.png`),
        fullPage: true,
      });
    }
    expect(errors).toEqual([]);
    expect(gateway.unmatched).toEqual([]);
  });
}
