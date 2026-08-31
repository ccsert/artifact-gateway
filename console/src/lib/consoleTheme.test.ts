import { afterEach, describe, expect, it } from "vitest";
import {
  applyConsoleTheme,
  buildConsoleThemeConfig,
  defaultConsoleThemes,
  resolveConsoleTheme,
} from "./consoleTheme";

afterEach(() => {
  document.documentElement.removeAttribute("style");
  delete document.documentElement.dataset.themeContract;
});

describe("console themes", () => {
  it("preserves explicit Ant Design aliases after the mode algorithm", () => {
    const dark = defaultConsoleThemes.find(
      (theme) => theme.id === "aerok-dark",
    )!;
    const { token } = resolveConsoleTheme(dark);

    expect(token.colorPrimary).toBe("#3258D0");
    expect(token.colorBgLayout).toBe("#090D16");
    expect(token.colorBgContainer).toBe("#121722");
    expect(token.colorBgElevated).toBe("#1B2230");
    expect(token.colorBorder).toBe("rgba(95, 112, 156, 0.32)");
    expect(token.colorTextSecondary).toBe("#B0B6C5");
  });

  it("derives Console CSS variables from the same resolved token map", () => {
    const light = defaultConsoleThemes.find(
      (theme) => theme.id === "aerok-light",
    )!;

    applyConsoleTheme(light, document.documentElement);

    expect(
      document.documentElement.style.getPropertyValue("--ag-action-primary"),
    ).toBe("#26499D");
    expect(
      document.documentElement.style.getPropertyValue("--ag-surface-container"),
    ).toBe("#ffffff");
    expect(
      document.documentElement.style.getPropertyValue("--ag-status-danger"),
    ).toBe("#B2154E");
    expect(document.documentElement).toHaveAttribute(
      "data-theme-contract",
      "semantic-v1",
    );
  });

  it("preserves the established Gateway Dark shell palette", () => {
    const dark = defaultConsoleThemes.find(
      (theme) => theme.id === "gateway-dark",
    )!;

    applyConsoleTheme(dark, document.documentElement);

    expect(document.documentElement.style.getPropertyValue("--ag-sider")).toBe(
      "",
    );
    expect(
      document.documentElement.style.getPropertyValue("--ag-surface-sider"),
    ).toBe("rgba(12, 13, 16, 0.96)");
    expect(
      document.documentElement.style.getPropertyValue(
        "--ag-surface-container-translucent",
      ),
    ).toBe("rgba(24, 24, 27, 0.55)");
    expect(
      document.documentElement.style.getPropertyValue(
        "--ag-action-primary-soft",
      ),
    ).toBe("rgba(6, 182, 212, 0.12)");

    const config = buildConsoleThemeConfig(dark);
    expect(config.components?.Menu?.darkItemBg).toBe("transparent");
    expect(config.components?.Menu?.darkItemSelectedColor).toBe("#a5f3fc");
    expect(config.components?.Button?.defaultBg).toBe("#141417");
    expect(config.components?.Input?.activeShadow).toBe(
      "0 0 0 2px rgba(6, 182, 212, 0.35)",
    );
    expect(config.components?.Segmented?.trackBg).toBe(
      "rgba(63, 63, 70, 0.16)",
    );
    expect(config.components?.Table?.headerColor).toBe("#8f8f9a");
  });

  it("preserves the established Gateway Light menu surface", () => {
    const light = defaultConsoleThemes.find(
      (theme) => theme.id === "gateway-light",
    )!;
    const resolved = resolveConsoleTheme(light);

    expect(resolved.roles.surface.menu).toBe("#ffffff");
    expect(resolved.antDesign.components?.Menu?.itemBg).toBe("#ffffff");
    expect(resolved.antDesign.components?.Menu?.subMenuItemBg).toBe("#ffffff");
  });

  it("keeps extension menus on their package palette", () => {
    const dark = defaultConsoleThemes.find(
      (theme) => theme.id === "aerok-dark",
    )!;
    const config = buildConsoleThemeConfig(dark);

    expect(config.components?.Menu?.darkItemBg).toBe("transparent");
    expect(config.components?.Menu?.darkItemHoverBg).toBe(
      "rgba(105, 121, 158, 0.18)",
    );
    expect(config.components?.Menu?.darkItemSelectedBg).toBe("#17203A");
    expect(config.components?.Menu?.darkItemSelectedColor).toBe("#6686EA");
  });

  it("keeps component geometry in the shared Console contract", () => {
    const config = buildConsoleThemeConfig(defaultConsoleThemes[0]);

    expect(config.components?.Menu?.itemHeight).toBe(38);
    expect(config.components?.Button?.borderRadius).toBe(8);
  });

  it("keeps native text selection neutral across every built-in theme", () => {
    for (const theme of defaultConsoleThemes) {
      const { roles } = resolveConsoleTheme(theme);

      const selection = roles.selection.background.toLowerCase();
      expect(selection).toContain(roles.content.primary.toLowerCase());
      expect(selection).toContain(roles.surface.container.toLowerCase());
      expect(selection).not.toContain(roles.action.primary.toLowerCase());
      expect(roles.selection.foreground.toLowerCase()).toBe(
        roles.content.primary.toLowerCase(),
      );
    }
  });

  it("projects the same complete semantic variable contract for every theme", () => {
    const variableSets = defaultConsoleThemes.map((theme) =>
      Object.keys(resolveConsoleTheme(theme).cssVariables).sort(),
    );

    expect(variableSets[0].length).toBeGreaterThan(50);
    for (const variables of variableSets.slice(1)) {
      expect(variables).toEqual(variableSets[0]);
    }
    expect(variableSets[0]).not.toContain("--ag-brand");
    expect(variableSets[0]).toContain("--ag-action-primary");
    expect(variableSets[0]).toContain("--ag-selection-background");
    expect(variableSets[0]).toContain("--ag-visualization-trend-primary");
  });

  it("keeps visualization colors independent from action and status meaning", () => {
    for (const theme of defaultConsoleThemes) {
      const { roles } = resolveConsoleTheme(theme);
      const operationalColors = [
        roles.action.primary,
        roles.status.success.foreground,
        roles.status.warning.foreground,
        roles.status.danger.foreground,
        roles.status.info.foreground,
      ].map((color) => color.toLowerCase());

      for (const color of roles.visualization.categorical) {
        expect(operationalColors).not.toContain(color.toLowerCase());
      }
      expect(roles.visualization.trendPrimary).not.toBe(roles.action.primary);
    }
  });

  it("resolves a v1 extension package without built-in CSS knowledge", () => {
    const extension = {
      ...defaultConsoleThemes[3],
      id: "operator-plum",
      name: "Operator Plum",
      token: {
        ...defaultConsoleThemes[3].token,
        colorPrimary: "#7C3AED",
        colorPrimaryHover: "#8B5CF6",
        colorPrimaryActive: "#6D28D9",
      },
    };

    const resolved = resolveConsoleTheme(extension);

    expect(resolved.roles.action.primary).toBe("#7C3AED");
    expect(resolved.roles.navigation.indicatorStart).toBe("#8B5CF6");
    expect(resolved.roles.surface.container).toBe("#ffffff");
    expect(resolved.roles.selection.background).not.toContain("#7C3AED");
    expect(Object.keys(resolved.cssVariables).length).toBeGreaterThan(50);
  });

  it("removes obsolete generic variables when applying the semantic contract", () => {
    document.documentElement.style.setProperty("--ag-brand", "hotpink");
    document.documentElement.style.setProperty("--ag-text", "hotpink");

    applyConsoleTheme(defaultConsoleThemes[0], document.documentElement);

    expect(document.documentElement.style.getPropertyValue("--ag-brand")).toBe(
      "",
    );
    expect(document.documentElement.style.getPropertyValue("--ag-text")).toBe(
      "",
    );
  });
});
