import { afterEach, describe, expect, it } from "vitest";
import {
  applyConsoleTheme,
  buildConsoleThemeConfig,
  defaultConsoleThemes,
  resolveConsoleTheme,
} from "./consoleTheme";

afterEach(() => {
  document.documentElement.removeAttribute("style");
});

describe("console themes", () => {
  it("preserves explicit Ant Design aliases after the mode algorithm", () => {
    const dark = defaultConsoleThemes.find(
      (theme) => theme.id === "aerok-dark",
    )!;
    const token = resolveConsoleTheme(dark);

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

    expect(document.documentElement.style.getPropertyValue("--ag-brand")).toBe(
      "#26499D",
    );
    expect(
      document.documentElement.style.getPropertyValue("--ag-surface-solid"),
    ).toBe("#ffffff");
    expect(document.documentElement.style.getPropertyValue("--ag-danger")).toBe(
      "#B2154E",
    );
  });

  it("preserves the established Gateway Dark shell palette", () => {
    const dark = defaultConsoleThemes.find(
      (theme) => theme.id === "gateway-dark",
    )!;

    applyConsoleTheme(dark, document.documentElement);

    expect(document.documentElement.style.getPropertyValue("--ag-sider")).toBe(
      "rgba(12, 13, 16, 0.96)",
    );
    expect(
      document.documentElement.style.getPropertyValue("--ag-surface"),
    ).toBe("rgba(24, 24, 27, 0.55)");
    expect(
      document.documentElement.style.getPropertyValue("--ag-brand-soft"),
    ).toBe("rgba(6, 182, 212, 0.12)");

    const config = buildConsoleThemeConfig(dark);
    expect(config.components?.Menu?.darkItemBg).toBe("transparent");
    expect(config.components?.Menu?.darkItemSelectedColor).toBe("#a5f3fc");
    expect(config.components?.Button?.defaultBg).toBe("#18181b");
    expect(config.components?.Input?.activeShadow).toBe(
      "0 0 0 2px rgba(6, 182, 212, 0.18)",
    );
    expect(config.components?.Segmented?.trackBg).toBe("rgba(39, 39, 42, 0.7)");
    expect(config.components?.Table?.headerColor).toBe("#8f8f9a");
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
});
