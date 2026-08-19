import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

test("Gateway Dark preserves the established shell and menu palette", async ({
  page,
}, testInfo) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme.id", "gateway-dark");
    localStorage.setItem("ag.console.theme", "dark");
  });
  await authenticateAsAdmin(page);

  await page.goto("/site-settings");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-dark",
  );
  await expect(page.getByRole("heading", { name: "站点设置" })).toBeVisible();

  const palette = await page.evaluate(() => {
    const style = (selector: string) => {
      const element = document.querySelector<HTMLElement>(selector);
      if (!element) throw new Error(`missing ${selector}`);
      return getComputedStyle(element);
    };
    const root = style("html");
    const sider = style(".ag-sider-desktop");
    const menu = style(".ag-desktop-nav");
    const selected = style(".ag-desktop-nav .ant-menu-item-selected");
    const indicator = getComputedStyle(
      document.querySelector<HTMLElement>(
        ".ag-desktop-nav .ant-menu-item-selected",
      )!,
      "::before",
    );
    return {
      themeID: document.documentElement.dataset.themeId,
      sider: sider.backgroundColor,
      menu: menu.backgroundColor,
      selectedBackground: selected.backgroundColor,
      selectedBackgroundImage: selected.backgroundImage,
      selectedColor: selected.color,
      indicatorBackgroundImage: indicator.backgroundImage,
      surface: root.getPropertyValue("--ag-surface").trim(),
      brandSoft: root.getPropertyValue("--ag-brand-soft").trim(),
    };
  });

  expect(palette).toEqual({
    themeID: "gateway-dark",
    sider: "rgba(12, 13, 16, 0.96)",
    menu: "rgba(0, 0, 0, 0)",
    selectedBackground: "rgba(0, 0, 0, 0)",
    selectedBackgroundImage:
      "linear-gradient(90deg, rgba(6, 182, 212, 0.14), rgba(6, 182, 212, 0.05))",
    selectedColor: "rgb(165, 243, 252)",
    indicatorBackgroundImage:
      "linear-gradient(rgb(34, 211, 238), rgb(8, 145, 178))",
    surface: "rgba(24, 24, 27, 0.55)",
    brandSoft: "rgba(6, 182, 212, 0.12)",
  });
  if (process.env.CAPTURE_THEME_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("gateway-dark-restored.png"),
      fullPage: true,
    });
  }
});

test("Gateway Light preserves the established shell and menu palette", async ({
  page,
}, testInfo) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme.id", "gateway-light");
    localStorage.setItem("ag.console.theme", "light");
  });
  await authenticateAsAdmin(page);

  await page.goto("/site-settings");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-light",
  );
  await expect(page.getByRole("heading", { name: "站点设置" })).toBeVisible();

  const palette = await page.evaluate(() => {
    const style = (selector: string) => {
      const element = document.querySelector<HTMLElement>(selector);
      if (!element) throw new Error(`missing ${selector}`);
      return getComputedStyle(element);
    };
    const root = style("html");
    const sider = style(".ag-sider-desktop");
    const menu = style(".ag-desktop-nav");
    const selected = style(".ag-desktop-nav .ant-menu-item-selected");
    const indicator = getComputedStyle(
      document.querySelector<HTMLElement>(
        ".ag-desktop-nav .ant-menu-item-selected",
      )!,
      "::before",
    );
    return {
      themeID: document.documentElement.dataset.themeId,
      sider: sider.backgroundColor,
      menu: menu.backgroundColor,
      selectedBackground: selected.backgroundColor,
      selectedBackgroundImage: selected.backgroundImage,
      selectedColor: selected.color,
      indicatorBackgroundImage: indicator.backgroundImage,
      surface: root.getPropertyValue("--ag-surface").trim(),
      brandSoft: root.getPropertyValue("--ag-brand-soft").trim(),
    };
  });

  expect(palette).toEqual({
    themeID: "gateway-light",
    sider: "rgba(255, 255, 255, 0.96)",
    menu: "rgb(255, 255, 255)",
    selectedBackground: "rgba(0, 0, 0, 0)",
    selectedBackgroundImage:
      "linear-gradient(90deg, rgba(6, 182, 212, 0.14), rgba(6, 182, 212, 0.05))",
    selectedColor: "rgb(8, 145, 178)",
    indicatorBackgroundImage:
      "linear-gradient(rgb(34, 211, 238), rgb(8, 145, 178))",
    surface: "rgba(255, 255, 255, 0.9)",
    brandSoft: "rgba(8, 145, 178, 0.1)",
  });
  if (process.env.CAPTURE_THEME_EVIDENCE === "1") {
    await page.screenshot({
      path: testInfo.outputPath("gateway-light-restored.png"),
      fullPage: true,
    });
  }
});

test("switching back from an extension theme restores Gateway Dark exactly", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("ag.console.theme.id", "aerok-dark");
    localStorage.setItem("ag.console.theme", "dark");
  });
  await authenticateAsAdmin(page);

  await page.goto("/site-settings");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "aerok-dark",
  );
  await page.getByRole("button", { name: /选择主题.*Aerok Dark/ }).click();
  await page.getByRole("menuitem", { name: /Gateway Dark/ }).click();
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme-id",
    "gateway-dark",
  );

  await expect
    .poll(() =>
      page.evaluate(() => {
        const value = (
          selector: string,
          property: "backgroundColor" | "backgroundImage" | "color",
        ) => {
          const element = document.querySelector<HTMLElement>(selector);
          if (!element) throw new Error(`missing ${selector}`);
          return getComputedStyle(element)[property];
        };
        return {
          menu: value(".ag-desktop-nav", "backgroundColor"),
          selectedBackground: value(
            ".ag-desktop-nav .ant-menu-item-selected",
            "backgroundColor",
          ),
          selectedBackgroundImage: value(
            ".ag-desktop-nav .ant-menu-item-selected",
            "backgroundImage",
          ),
          selectedColor: value(
            ".ag-desktop-nav .ant-menu-item-selected",
            "color",
          ),
          indicatorBackgroundImage: getComputedStyle(
            document.querySelector<HTMLElement>(
              ".ag-desktop-nav .ant-menu-item-selected",
            )!,
            "::before",
          ).backgroundImage,
        };
      }),
    )
    .toEqual({
      menu: "rgba(0, 0, 0, 0)",
      selectedBackground: "rgba(0, 0, 0, 0)",
      selectedBackgroundImage:
        "linear-gradient(90deg, rgba(6, 182, 212, 0.14), rgba(6, 182, 212, 0.05))",
      selectedColor: "rgb(165, 243, 252)",
      indicatorBackgroundImage:
        "linear-gradient(rgb(34, 211, 238), rgb(8, 145, 178))",
    });
});
