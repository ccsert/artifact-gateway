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
      surface: root
        .getPropertyValue("--ag-surface-container-translucent")
        .trim(),
      actionSoft: root.getPropertyValue("--ag-action-primary-soft").trim(),
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
    actionSoft: "rgba(6, 182, 212, 0.12)",
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
      surface: root
        .getPropertyValue("--ag-surface-container-translucent")
        .trim(),
      actionSoft: root.getPropertyValue("--ag-action-primary-soft").trim(),
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
    actionSoft: "rgba(8, 145, 178, 0.1)",
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

const semanticThemeCases = [
  ["gateway-dark", "#06b6d4"],
  ["gateway-light", "#0891b2"],
  ["aerok-dark", "#3258d0"],
  ["aerok-light", "#26499d"],
] as const;

for (const [themeID, expectedAction] of semanticThemeCases) {
  test(`${themeID} commits one complete semantic browser contract`, async ({
    page,
  }, testInfo) => {
    await page.addInitScript((id) => {
      localStorage.setItem("ag.console.theme.id", id);
      localStorage.setItem(
        "ag.console.theme",
        id.endsWith("-light") ? "light" : "dark",
      );
    }, themeID);
    await authenticateAsAdmin(page);

    await page.goto("/site-settings");
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme-contract",
      "semantic-v1",
    );
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme-id",
      themeID,
    );
    await expect(page.getByRole("heading", { name: "站点设置" })).toBeVisible();

    const contract = await page.evaluate(() => {
      const root = document.documentElement;
      const style = getComputedStyle(root);
      const resolveColor = (value: string) => {
        const probe = document.createElement("span");
        probe.style.color = value;
        document.body.append(probe);
        const color = getComputedStyle(probe).color;
        probe.remove();
        return color;
      };
      return {
        action: style.getPropertyValue("--ag-action-primary").trim(),
        actionComputed: resolveColor("var(--ag-action-primary)"),
        onIdentityComputed: resolveColor("var(--ag-content-on-identity)"),
        brandMarkColor: getComputedStyle(
          document.querySelector<HTMLElement>(".ag-brand-mark")!,
        ).color,
        contentComputed: resolveColor("var(--ag-content-primary)"),
        bodyColor: getComputedStyle(document.body).color,
        selectionSource: style
          .getPropertyValue("--ag-selection-background")
          .trim(),
        selectionComputed: getComputedStyle(document.body, "::selection")
          .backgroundColor,
        visualization: style.getPropertyValue("--ag-visualization-1").trim(),
        statusInfo: style.getPropertyValue("--ag-status-info").trim(),
        semanticVariableCount: Array.from(root.style).filter((name) =>
          name.startsWith("--ag-"),
        ).length,
        legacyBrand: root.style.getPropertyValue("--ag-brand"),
        legacyText: root.style.getPropertyValue("--ag-text"),
      };
    });

    expect(contract.action.toLowerCase()).toBe(expectedAction);
    expect(contract.brandMarkColor).toBe(contract.onIdentityComputed);
    expect(contract.bodyColor).toBe(contract.contentComputed);
    expect(contract.selectionSource.toLowerCase()).not.toContain(
      expectedAction,
    );
    expect(contract.selectionComputed).not.toBe(contract.actionComputed);
    expect(contract.visualization.toLowerCase()).not.toBe(expectedAction);
    expect(contract.visualization.toLowerCase()).not.toBe(
      contract.statusInfo.toLowerCase(),
    );
    expect(contract.semanticVariableCount).toBeGreaterThan(70);
    expect(contract.legacyBrand).toBe("");
    expect(contract.legacyText).toBe("");

    if (process.env.CAPTURE_THEME_EVIDENCE === "1") {
      // Let entry motion settle so visual evidence represents the steady state.
      await page.waitForTimeout(250);
      await page.screenshot({
        path: testInfo.outputPath(`${themeID}-semantic-contract.png`),
        fullPage: true,
      });
    }
  });
}
