import { theme as antdTheme, type ThemeConfig } from "antd";
import type { ConsoleTheme } from "../client";

const consoleComponentInvariants: NonNullable<ThemeConfig["components"]> = {
  Button: {
    borderRadius: 8,
    controlHeight: 34,
    fontWeight: 500,
  },
  Card: {
    headerBg: "transparent",
  },
  Input: {
    borderRadius: 8,
  },
  Menu: {
    collapsedWidth: 80,
    itemBorderRadius: 8,
    itemHeight: 38,
    itemMarginBlock: 3,
    itemMarginInline: 4,
  },
  Modal: {
    borderRadiusLG: 12,
    headerBg: "transparent",
  },
  Tooltip: {
    borderRadius: 6,
  },
};

const consoleTokenInvariants: NonNullable<ThemeConfig["token"]> = {
  borderRadius: 10,
  controlHeight: 34,
  fontFamily:
    "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif",
  fontFamilyCode:
    "JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace",
  fontSize: 14,
  motionDurationMid: "0.18s",
  motionEaseOut: "cubic-bezier(0.16, 1, 0.3, 1)",
};

const gatewayShellCompatibility: Record<
  "gateway-dark" | "gateway-light",
  Record<string, string>
> = {
  "gateway-dark": {
    "--ag-bg": "#08090b",
    "--ag-bg-grad-a": "#0a0e13",
    "--ag-bg-grad-b": "#08090b",
    "--ag-surface": "rgba(24, 24, 27, 0.55)",
    "--ag-surface-solid": "#141417",
    "--ag-surface-hover": "rgba(39, 39, 42, 0.55)",
    "--ag-overlay": "#1b1b1f",
    "--ag-border": "rgba(63, 63, 70, 0.65)",
    "--ag-border-subtle": "rgba(63, 63, 70, 0.35)",
    "--ag-brand": "#06b6d4",
    "--ag-brand-soft": "rgba(6, 182, 212, 0.12)",
    "--ag-brand-glow": "rgba(6, 182, 212, 0.35)",
    "--ag-text": "#e4e4e7",
    "--ag-text-strong": "#fafafa",
    "--ag-text-dim": "#b4b4bd",
    "--ag-text-mute": "#8f8f9a",
    "--ag-text-faint": "#85858f",
    "--ag-shadow-card":
      "0 1px 2px rgba(0, 0, 0, 0.35), 0 4px 16px -8px rgba(0, 0, 0, 0.4)",
    "--ag-shadow-pop":
      "0 8px 32px -8px rgba(0, 0, 0, 0.6), 0 2px 8px rgba(0, 0, 0, 0.4)",
    "--ag-sider": "rgba(12, 13, 16, 0.96)",
    "--ag-topbar": "rgba(8, 9, 11, 0.9)",
    "--ag-table-header": "rgba(9, 10, 12, 0.4)",
    "--ag-table-row-border": "rgba(63, 63, 70, 0.25)",
    "--ag-table-hover": "rgba(39, 39, 42, 0.32)",
    "--ag-scrollbar": "#3f3f46",
    "--ag-scrollbar-hover": "#52525b",
    "--ag-selection-bg": "rgba(6, 182, 212, 0.3)",
    "--ag-selection-text": "#cffafe",
    "--ag-nav-indicator-start": "#22d3ee",
    "--ag-nav-indicator-end": "#0891b2",
    "--ag-nav-selected-bg-start": "rgba(6, 182, 212, 0.14)",
    "--ag-nav-selected-bg-end": "rgba(6, 182, 212, 0.05)",
    "--ag-radius": "10px",
    "--ag-radius-sm": "8px",
  },
  "gateway-light": {
    "--ag-bg": "#f6f7f9",
    "--ag-bg-grad-a": "#f6f7f9",
    "--ag-bg-grad-b": "#f6f7f9",
    "--ag-surface": "rgba(255, 255, 255, 0.9)",
    "--ag-surface-solid": "#ffffff",
    "--ag-surface-hover": "#f4f4f5",
    "--ag-overlay": "#ffffff",
    "--ag-border": "#d4d4d8",
    "--ag-border-subtle": "#e4e4e7",
    "--ag-brand": "#0891b2",
    "--ag-brand-soft": "rgba(8, 145, 178, 0.1)",
    "--ag-brand-glow": "rgba(8, 145, 178, 0.25)",
    "--ag-text": "#27272a",
    "--ag-text-strong": "#18181b",
    "--ag-text-dim": "#52525b",
    "--ag-text-mute": "#5f5f68",
    "--ag-text-faint": "#6b6b74",
    "--ag-shadow-card": "0 1px 2px rgba(24, 24, 27, 0.06)",
    "--ag-shadow-pop":
      "0 16px 40px -12px rgba(24, 24, 27, 0.18), 0 2px 8px rgba(24, 24, 27, 0.08)",
    "--ag-sider": "rgba(255, 255, 255, 0.96)",
    "--ag-topbar": "rgba(255, 255, 255, 0.9)",
    "--ag-table-header": "#fafafa",
    "--ag-table-row-border": "#eeeeef",
    "--ag-table-hover": "#f4f4f5",
    "--ag-scrollbar": "#d4d4d8",
    "--ag-scrollbar-hover": "#a1a1aa",
    "--ag-selection-bg": "rgba(8, 145, 178, 0.2)",
    "--ag-selection-text": "#164e63",
    "--ag-nav-indicator-start": "#22d3ee",
    "--ag-nav-indicator-end": "#0891b2",
    "--ag-nav-selected-bg-start": "rgba(6, 182, 212, 0.14)",
    "--ag-nav-selected-bg-end": "rgba(6, 182, 212, 0.05)",
    "--ag-radius": "10px",
    "--ag-radius-sm": "8px",
  },
};

function buildConsoleComponents(
  theme: ConsoleTheme,
): NonNullable<ThemeConfig["components"]> {
  if (theme.id === "gateway-dark" || theme.id === "gateway-light") {
    const dark = theme.id === "gateway-dark";
    return {
      ...consoleComponentInvariants,
      Button: {
        ...consoleComponentInvariants.Button,
        defaultBg: dark ? "#18181b" : "#ffffff",
        defaultBorderColor: dark ? "#3f3f46" : "#d4d4d8",
        defaultColor: dark ? "#e4e4e7" : "#27272a",
        primaryShadow: dark
          ? "0 2px 10px -2px rgba(6, 182, 212, 0.5)"
          : "0 2px 8px -2px rgba(8, 145, 178, 0.35)",
      },
      Empty: {
        colorTextDisabled: dark ? "#52525b" : "#a1a1aa",
      },
      Input: {
        ...consoleComponentInvariants.Input,
        activeShadow: dark
          ? "0 0 0 2px rgba(6, 182, 212, 0.18)"
          : "0 0 0 2px rgba(8, 145, 178, 0.14)",
      },
      Menu: {
        ...consoleComponentInvariants.Menu,
        darkGroupTitleColor: "#85858f",
        darkItemBg: "transparent",
        darkItemColor: "#a1a1aa",
        darkItemHoverBg: "rgba(39, 39, 42, 0.65)",
        darkItemHoverColor: "#fafafa",
        darkItemSelectedBg: "rgba(6, 182, 212, 0.12)",
        darkItemSelectedColor: "#a5f3fc",
        darkSubMenuItemBg: "transparent",
      },
      Segmented: {
        itemColor: dark ? "#a1a1aa" : "#52525b",
        itemHoverBg: dark ? "rgba(63, 63, 70, 0.28)" : "#e4e7eb",
        itemHoverColor: dark ? "#fafafa" : "#18181b",
        itemSelectedBg: dark ? "#27272a" : "#ffffff",
        itemSelectedColor: dark ? "#fafafa" : "#18181b",
        trackBg: dark ? "rgba(39, 39, 42, 0.7)" : "#eef0f3",
      },
      Table: {
        borderColor: dark ? "#27272a" : "#e4e4e7",
        expandIconBg: dark ? "#18181b" : "#ffffff",
        headerBg: "transparent",
        headerColor: dark ? "#8f8f9a" : "#52525b",
        headerSplitColor: dark ? "#27272a" : "#e4e4e7",
        rowExpandedBg: dark ? "#111114" : "#fafafa",
        rowHoverBg: dark ? "rgba(39, 39, 42, 0.55)" : "#f4f4f5",
      },
    };
  }

  const token = theme.token;
  return {
    ...consoleComponentInvariants,
    Menu: {
      ...consoleComponentInvariants.Menu,
      darkGroupTitleColor:
        token.colorTextQuaternary ?? token.colorTextSecondary,
      darkItemBg: "transparent",
      darkItemColor: token.colorTextSecondary ?? token.colorTextBase,
      darkItemHoverBg:
        token.colorFillContentHover ?? token.colorBgElevated ?? "transparent",
      darkItemHoverColor: token.colorText ?? token.colorTextBase,
      darkItemSelectedBg: token.colorPrimaryBg,
      darkItemSelectedColor:
        token.colorPrimaryHover ?? token.colorPrimary ?? token.colorTextBase,
      darkSubMenuItemBg: "transparent",
      groupTitleColor: token.colorTextQuaternary ?? token.colorTextSecondary,
      itemBg: "transparent",
      itemColor: token.colorTextSecondary ?? token.colorTextBase,
      itemHoverBg:
        token.colorFillContentHover ?? token.colorFillContent ?? "transparent",
      itemHoverColor: token.colorText ?? token.colorTextBase,
      itemSelectedBg: token.colorPrimaryBg,
      itemSelectedColor: token.colorPrimary,
      subMenuItemBg: "transparent",
    },
  };
}

export const defaultConsoleThemes: ConsoleTheme[] = [
  {
    schemaVersion: 1,
    id: "gateway-dark",
    name: "Gateway Dark",
    description:
      "Artifact Gateway 原有深色主题，使用青色操作信号与中性黑色表面。",
    mode: "dark",
    token: {
      colorPrimary: "#06B6D4",
      colorSuccess: "#34D399",
      colorWarning: "#FBBF24",
      colorError: "#FB7185",
      colorInfo: "#06B6D4",
      colorTextBase: "#E4E4E7",
      colorBgBase: "#08090B",
      colorText: "#E4E4E7",
      colorTextSecondary: "#B4B4BD",
      colorTextTertiary: "#8F8F9A",
      colorTextQuaternary: "#85858F",
      colorTextDisabled: "#52525B",
      colorBgContainer: "#141417",
      colorBgElevated: "#1B1B1F",
      colorBgLayout: "#08090B",
      colorBgSpotlight: "#27272A",
      colorBorder: "rgba(63, 63, 70, 0.65)",
      colorBorderSecondary: "rgba(63, 63, 70, 0.35)",
      colorFillAlter: "rgba(63, 63, 70, 0.16)",
      colorFillContent: "rgba(63, 63, 70, 0.30)",
      colorFillContentHover: "rgba(63, 63, 70, 0.42)",
      colorLink: "#22D3EE",
      colorLinkHover: "#67E8F9",
      colorLinkActive: "#0891B2",
      colorPrimaryHover: "#22D3EE",
      colorPrimaryActive: "#0891B2",
      controlOutline: "rgba(6, 182, 212, 0.22)",
    },
  },
  {
    schemaVersion: 1,
    id: "gateway-light",
    name: "Gateway Light",
    description:
      "Artifact Gateway 原有浅色主题，使用青蓝色操作信号与中性灰白表面。",
    mode: "light",
    token: {
      colorPrimary: "#0891B2",
      colorSuccess: "#047857",
      colorWarning: "#A16207",
      colorError: "#BE123C",
      colorInfo: "#0891B2",
      colorTextBase: "#27272A",
      colorBgBase: "#F6F7F9",
      colorText: "#27272A",
      colorTextSecondary: "#52525B",
      colorTextTertiary: "#5F5F68",
      colorTextQuaternary: "#6B6B74",
      colorTextDisabled: "#71717A",
      colorBgContainer: "#FFFFFF",
      colorBgElevated: "#FFFFFF",
      colorBgLayout: "#F6F7F9",
      colorBgSpotlight: "#27272A",
      colorBorder: "#D4D4D8",
      colorBorderSecondary: "#E4E4E7",
      colorFillAlter: "#F4F4F5",
      colorFillContent: "#E4E4E7",
      colorFillContentHover: "#D4D4D8",
      colorLink: "#0E7490",
      colorLinkHover: "#164E63",
      colorLinkActive: "#155E75",
      colorPrimaryHover: "#0E7490",
      colorPrimaryActive: "#155E75",
      controlOutline: "rgba(6, 182, 212, 0.22)",
    },
  },
  {
    schemaVersion: 1,
    id: "aerok-dark",
    name: "Aerok Dark",
    description: "Aerok 深色控制台主题，使用品牌蓝色操作信号与层次化深蓝表面。",
    mode: "dark",
    token: {
      colorPrimary: "#3258D0",
      colorSuccess: "#73D13D",
      colorWarning: "#D89614",
      colorError: "#DC4446",
      colorInfo: "#2F83DC",
      colorTextBase: "#E7EAF2",
      colorBgBase: "#090D16",
      colorText: "#E7EAF2",
      colorTextSecondary: "#B0B6C5",
      colorTextTertiary: "#8B94A7",
      colorTextQuaternary: "#808AA0",
      colorTextDisabled: "#808AA0",
      colorBgContainer: "#121722",
      colorBgElevated: "#1B2230",
      colorBgLayout: "#090D16",
      colorBgSpotlight: "#273142",
      colorBorder: "rgba(95, 112, 156, 0.32)",
      colorBorderSecondary: "rgba(95, 112, 156, 0.18)",
      colorFillAlter: "rgba(105, 121, 158, 0.08)",
      colorFillContent: "rgba(105, 121, 158, 0.13)",
      colorFillContentHover: "rgba(105, 121, 158, 0.18)",
      colorLink: "#7796F5",
      colorLinkHover: "#9AAFFC",
      colorLinkActive: "#5879E3",
      colorPrimaryHover: "#6686EA",
      colorPrimaryActive: "#2847AD",
      colorPrimaryBg: "#17203A",
      controlOutline: "rgba(70, 105, 222, 0.28)",
    },
  },
  {
    schemaVersion: 1,
    id: "aerok-light",
    name: "Aerok Light",
    description: "Aerok 浅色控制台主题，使用沉稳蓝色操作信号与高可读白色表面。",
    mode: "light",
    token: {
      colorPrimary: "#26499D",
      colorSuccess: "#25B444",
      colorWarning: "#E46F31",
      colorError: "#B2154E",
      colorInfo: "#0085BA",
      colorTextBase: "#1D1F25",
      colorBgBase: "#FFFFFF",
      colorText: "rgba(8, 17, 44, 0.88)",
      colorTextSecondary: "rgba(8, 17, 44, 0.65)",
      colorTextTertiary: "rgba(8, 17, 44, 0.45)",
      colorTextQuaternary: "rgba(8, 17, 44, 0.25)",
      colorTextDisabled: "rgba(8, 17, 44, 0.25)",
      colorBgSpotlight: "rgba(0, 0, 0, 0.85)",
      colorFillAlter: "rgba(8, 17, 44, 0.02)",
      colorFillContent: "rgba(8, 17, 44, 0.06)",
      colorFillContentHover: "rgba(8, 17, 44, 0.15)",
      colorLink: "#4E6FCA",
      colorLinkHover: "#26499D",
      colorLinkActive: "#18388B",
      colorPrimaryHover: "#4E6FCA",
      colorPrimaryActive: "#18388B",
      colorPrimaryBg: "#E0EAFD",
      controlOutline: "rgba(38, 73, 157, 0.18)",
    },
  },
];

export function buildConsoleThemeConfig(theme: ConsoleTheme): ThemeConfig {
  const baseAlgorithm =
    theme.mode === "dark"
      ? antdTheme.darkAlgorithm
      : antdTheme.defaultAlgorithm;
  const algorithm: typeof antdTheme.darkAlgorithm = (seedToken, mapToken) => ({
    ...baseAlgorithm(seedToken, mapToken),
    // Theme Package v1 stores stable Ant Design Seed/Alias tokens. Applying
    // explicit aliases after the mode algorithm keeps exported values exact
    // (darkAlgorithm otherwise remaps values such as colorPrimary a second time).
    ...theme.token,
  });
  return {
    algorithm,
    components: buildConsoleComponents(theme),
    cssVar: { key: theme.id, prefix: "ag-ant" },
    token: {
      ...(theme.token as ThemeConfig["token"]),
      ...consoleTokenInvariants,
    },
  };
}

export function resolveConsoleTheme(theme: ConsoleTheme) {
  return antdTheme.getDesignToken(buildConsoleThemeConfig(theme));
}

export function applyConsoleTheme(theme: ConsoleTheme, root: HTMLElement) {
  const token = resolveConsoleTheme(theme);
  const variables: Record<string, string> = {
    "--ag-bg": token.colorBgLayout,
    "--ag-bg-grad-a": token.colorBgLayout,
    "--ag-bg-grad-b": token.colorBgBase,
    "--ag-surface": token.colorBgContainer,
    "--ag-surface-solid": token.colorBgContainer,
    "--ag-surface-hover": token.colorFillContent,
    "--ag-overlay": token.colorBgElevated,
    "--ag-border": token.colorBorder,
    "--ag-border-subtle": token.colorBorderSecondary,
    "--ag-brand": token.colorPrimary,
    "--ag-brand-soft": token.colorPrimaryBg,
    "--ag-brand-glow": token.controlOutline,
    "--ag-danger": token.colorError,
    "--ag-danger-soft": token.colorErrorBg,
    "--ag-danger-border": token.colorErrorBorder,
    "--ag-success": token.colorSuccess,
    "--ag-success-soft": token.colorSuccessBg,
    "--ag-success-border": token.colorSuccessBorder,
    "--ag-warning": token.colorWarning,
    "--ag-warning-soft": token.colorWarningBg,
    "--ag-warning-border": token.colorWarningBorder,
    "--ag-info": token.colorInfo,
    "--ag-info-soft": token.colorInfoBg,
    "--ag-info-border": token.colorInfoBorder,
    "--ag-text": token.colorText,
    "--ag-text-strong": token.colorTextHeading,
    "--ag-text-dim": token.colorTextSecondary,
    "--ag-text-mute": token.colorTextTertiary,
    "--ag-text-faint": token.colorTextQuaternary,
    "--ag-shadow-card": token.boxShadowTertiary,
    "--ag-shadow-pop": token.boxShadowSecondary,
    "--ag-sider": token.colorBgContainer,
    "--ag-topbar": token.colorBgContainer,
    "--ag-table-header": token.colorFillAlter,
    "--ag-table-row-border": token.colorSplit,
    "--ag-table-hover": token.colorFillContent,
    "--ag-scrollbar": token.colorBorder,
    "--ag-scrollbar-hover": token.colorTextQuaternary,
    "--ag-selection-bg": token.colorPrimaryBg,
    "--ag-selection-text": token.colorPrimaryText,
    "--ag-nav-indicator-start": token.colorPrimaryHover,
    "--ag-nav-indicator-end": token.colorPrimaryActive,
    "--ag-nav-selected-bg-start": `color-mix(in srgb, ${token.colorPrimary} 14%, transparent)`,
    "--ag-nav-selected-bg-end": `color-mix(in srgb, ${token.colorPrimary} 5%, transparent)`,
    "--ag-radius": `${token.borderRadiusLG}px`,
    "--ag-radius-sm": `${token.borderRadius}px`,
    ...(theme.id === "gateway-dark" || theme.id === "gateway-light"
      ? gatewayShellCompatibility[theme.id]
      : {}),
  };
  for (const [name, value] of Object.entries(variables)) {
    root.style.setProperty(name, value);
  }
}
