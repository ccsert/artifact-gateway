import { theme as antdTheme, type ThemeConfig } from "antd";
import type { ConsoleTheme } from "../../client";

import { builtInRoleOverrides, mix } from "./builtInThemeRoles";
import type {
  ConsoleStatusRoles,
  ConsoleThemeRoles,
  ResolvedConsoleTheme,
  ResolvedAntDesignToken,
  RoleOverrides,
} from "./semanticThemeTypes";

import { buildCSSVariables, legacyThemeVariables } from "./themeCssVariables";
export type {
  ConsoleStatusRoles,
  ConsoleThemeRoles,
  ResolvedConsoleTheme,
} from "./semanticThemeTypes";

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
    borderRadiusLG: 14,
    headerBg: "transparent",
  },
  Tooltip: {
    borderRadius: 6,
  },
};

const consoleTokenInvariants: NonNullable<ThemeConfig["token"]> = {
  borderRadius: 8,
  borderRadiusLG: 12,
  borderRadiusSM: 6,
  controlHeight: 34,
  fontFamily:
    "'Geist Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', 'PingFang SC', 'Noto Sans SC', 'Microsoft YaHei', sans-serif",
  fontFamilyCode:
    "'Geist Mono Variable', ui-monospace, SFMono-Regular, Menlo, monospace",
  fontSize: 14,
  motionDurationMid: "0.18s",
  motionEaseOut: "cubic-bezier(0.16, 1, 0.3, 1)",
};

function statusRoles(
  color: string,
  softAlpha: number,
  borderAlpha: number,
): ConsoleStatusRoles {
  return {
    foreground: color,
    soft: mix(color, softAlpha),
    border: mix(color, borderAlpha),
  };
}

function mergeRoleOverrides(
  roles: ConsoleThemeRoles,
  overrides: RoleOverrides | undefined,
): ConsoleThemeRoles {
  if (!overrides) return roles;
  return {
    surface: { ...roles.surface, ...overrides.surface },
    content: { ...roles.content, ...overrides.content },
    border: { ...roles.border, ...overrides.border },
    action: { ...roles.action, ...overrides.action },
    link: { ...roles.link, ...overrides.link },
    focus: { ...roles.focus, ...overrides.focus },
    selection: { ...roles.selection, ...overrides.selection },
    navigation: { ...roles.navigation, ...overrides.navigation },
    status: {
      success: { ...roles.status.success, ...overrides.status?.success },
      warning: { ...roles.status.warning, ...overrides.status?.warning },
      danger: { ...roles.status.danger, ...overrides.status?.danger },
      info: { ...roles.status.info, ...overrides.status?.info },
    },
    visualization: { ...roles.visualization, ...overrides.visualization },
    identity: { ...roles.identity, ...overrides.identity },
    effect: { ...roles.effect, ...overrides.effect },
  };
}

function buildSemanticRoles(
  theme: ConsoleTheme,
  token: ResolvedAntDesignToken,
): ConsoleThemeRoles {
  const dark = theme.mode === "dark";
  const softAlpha = dark ? 12 : 8;
  const borderAlpha = dark ? 28 : 20;
  const roles: ConsoleThemeRoles = {
    surface: {
      canvas: token.colorBgLayout,
      canvasGradientStart: token.colorBgLayout,
      canvasGradientEnd: token.colorBgBase,
      container: token.colorBgContainer,
      containerTranslucent: mix(token.colorBgContainer, dark ? 82 : 92),
      hover: token.colorFillContentHover,
      elevated: token.colorBgElevated,
      disabled: token.colorFillAlter,
      sider: mix(token.colorBgContainer, dark ? 94 : 96),
      menu: "transparent",
      topbar: mix(token.colorBgContainer, 90),
      tableHeader: token.colorFillAlter,
      tableHover: token.colorFillContent,
    },
    content: {
      strong: token.colorTextHeading,
      primary: token.colorText,
      secondary: token.colorTextSecondary,
      tertiary: token.colorTextTertiary,
      disabled: token.colorTextQuaternary,
      onAction: token.colorTextLightSolid,
      onIdentity: token.colorTextLightSolid,
    },
    border: {
      default: token.colorBorder,
      subtle: token.colorBorderSecondary,
      row: token.colorSplit,
      strong: token.colorTextQuaternary,
    },
    action: {
      primary: token.colorPrimary,
      hover: token.colorPrimaryHover,
      active: token.colorPrimaryActive,
      soft: token.colorPrimaryBg,
      shadow: `0 2px 10px -2px ${mix(token.colorPrimary, dark ? 48 : 32)}`,
    },
    link: {
      default: token.colorLink,
      hover: token.colorLinkHover,
      active: token.colorLinkActive,
    },
    focus: {
      ring: token.controlOutline,
    },
    selection: {
      // Native selection is content state, not product action. Mixing the
      // normal foreground into its container keeps it neutral in every theme.
      background: mix(token.colorText, dark ? 24 : 18, token.colorBgContainer),
      foreground: token.colorText,
    },
    navigation: {
      indicatorStart: token.colorPrimaryHover,
      indicatorEnd: token.colorPrimaryActive,
      selectedBackgroundStart: token.colorPrimaryBg,
      selectedBackgroundEnd: mix(token.colorPrimary, 5),
      selectedText: token.colorPrimaryHover,
    },
    status: {
      success: statusRoles(token.colorSuccess, softAlpha, borderAlpha),
      warning: statusRoles(token.colorWarning, softAlpha, borderAlpha),
      danger: statusRoles(token.colorError, softAlpha, borderAlpha),
      info: statusRoles(token.colorInfo, softAlpha, borderAlpha),
    },
    visualization: {
      // Categorical data must not borrow the current action or status roles.
      // Ant Design's stable preset seeds preserve category identity while its
      // chart theme handles mode-specific axes, labels, and surfaces.
      categorical: [
        token.cyan,
        token.gold,
        token.magenta,
        token.green,
        token.blue,
        token.purple,
        token.geekblue,
        token.volcano,
        token.lime,
      ],
      fallback: token.colorTextQuaternary,
      trendPrimary: token.blue,
      trendSecondary: token.gold,
    },
    identity: {
      gradientStart: token.colorPrimaryActive,
      gradientMiddle: token.colorPrimary,
      gradientEnd: token.colorPrimaryHover,
      outline: mix(token.colorPrimaryHover, 25),
      outlineHover: mix(token.colorPrimaryHover, 45),
      glow: mix(token.colorPrimary, 48),
      glowHover: mix(token.colorPrimary, 64),
    },
    effect: {
      shadowStructural: token.boxShadowTertiary,
      shadowElevated: token.boxShadowSecondary,
      scrollbar: token.colorBorder,
      scrollbarHover: token.colorTextQuaternary,
      radiusSurface: `${token.borderRadiusLG}px`,
      radiusControl: `${token.borderRadius}px`,
    },
  };
  return mergeRoleOverrides(roles, builtInRoleOverrides[theme.id]);
}

function buildBaseThemeConfig(theme: ConsoleTheme): ThemeConfig {
  const baseAlgorithm =
    theme.mode === "dark"
      ? antdTheme.darkAlgorithm
      : antdTheme.defaultAlgorithm;
  const algorithm: typeof antdTheme.darkAlgorithm = (seedToken, mapToken) => ({
    ...baseAlgorithm(seedToken, mapToken),
    // Theme Package v1 stores stable Ant Design Seed/Alias tokens. Applying
    // explicit aliases after the algorithm prevents a second hue remapping.
    ...theme.token,
  });
  return {
    algorithm,
    cssVar: { key: theme.id, prefix: "ag-ant" },
    token: {
      ...(theme.token as ThemeConfig["token"]),
      ...consoleTokenInvariants,
    },
  };
}

function buildConsoleComponents(
  token: ResolvedAntDesignToken,
  roles: ConsoleThemeRoles,
): NonNullable<ThemeConfig["components"]> {
  return {
    ...consoleComponentInvariants,
    Button: {
      ...consoleComponentInvariants.Button,
      defaultBg: roles.surface.container,
      defaultBorderColor: roles.border.default,
      defaultColor: roles.content.primary,
      // The Gateway compatibility roles correct only action colors. Keep
      // global solid text (including danger) and extension palettes intact.
      primaryColor: roles.content.onAction,
      colorPrimary: roles.action.primary,
      colorPrimaryHover: roles.action.hover,
      colorPrimaryActive: roles.action.active,
      primaryShadow: roles.action.shadow,
    },
    Empty: {
      colorTextDisabled: roles.content.disabled,
    },
    Input: {
      ...consoleComponentInvariants.Input,
      activeShadow: `0 0 0 2px ${roles.focus.ring}`,
    },
    Menu: {
      ...consoleComponentInvariants.Menu,
      darkGroupTitleColor: roles.content.disabled,
      darkItemBg: "transparent",
      darkItemColor: roles.content.secondary,
      darkItemHoverBg: roles.surface.hover,
      darkItemHoverColor: roles.content.strong,
      darkItemSelectedBg: roles.navigation.selectedBackgroundStart,
      darkItemSelectedColor: roles.navigation.selectedText,
      darkSubMenuItemBg: "transparent",
      groupTitleColor: roles.content.disabled,
      itemBg: roles.surface.menu,
      itemColor: roles.content.secondary,
      itemHoverBg: roles.surface.hover,
      itemHoverColor: roles.content.strong,
      itemSelectedBg: roles.navigation.selectedBackgroundStart,
      itemSelectedColor: roles.navigation.selectedText,
      subMenuItemBg: roles.surface.menu,
    },
    Segmented: {
      itemColor: roles.content.secondary,
      itemHoverBg: roles.surface.hover,
      itemHoverColor: roles.content.strong,
      itemSelectedBg: roles.surface.container,
      itemSelectedColor: roles.content.strong,
      trackBg: roles.surface.disabled,
    },
    Table: {
      borderColor: roles.border.subtle,
      expandIconBg: roles.surface.container,
      headerBg: "transparent",
      headerColor: roles.content.tertiary,
      headerSplitColor: roles.border.subtle,
      rowExpandedBg: token.colorFillAlter,
      rowHoverBg: roles.surface.hover,
    },
  };
}

export function resolveConsoleTheme(theme: ConsoleTheme): ResolvedConsoleTheme {
  const base = buildBaseThemeConfig(theme);
  const token = antdTheme.getDesignToken(base);
  const roles = buildSemanticRoles(theme, token);
  const antDesign: ThemeConfig = {
    ...base,
    components: buildConsoleComponents(token, roles),
  };
  return {
    id: theme.id,
    mode: theme.mode,
    token,
    roles,
    antDesign,
    cssVariables: buildCSSVariables(roles),
  };
}

export function buildConsoleThemeConfig(theme: ConsoleTheme): ThemeConfig {
  return resolveConsoleTheme(theme).antDesign;
}

export function applyResolvedConsoleTheme(
  resolved: ResolvedConsoleTheme,
  root: HTMLElement,
) {
  for (const name of legacyThemeVariables) root.style.removeProperty(name);
  for (const [name, value] of Object.entries(resolved.cssVariables)) {
    root.style.setProperty(name, value);
  }
  root.dataset.themeContract = "semantic-v1";
}

export function applyConsoleTheme(theme: ConsoleTheme, root: HTMLElement) {
  applyResolvedConsoleTheme(resolveConsoleTheme(theme), root);
}
