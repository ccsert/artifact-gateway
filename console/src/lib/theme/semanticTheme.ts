import { theme as antdTheme, type ThemeConfig } from "antd";
import type { ConsoleTheme } from "../../client";

type ResolvedAntDesignToken = ReturnType<typeof antdTheme.getDesignToken>;

export interface ConsoleStatusRoles {
  readonly foreground: string;
  readonly soft: string;
  readonly border: string;
}

export interface ConsoleThemeRoles {
  readonly surface: {
    readonly canvas: string;
    readonly canvasGradientStart: string;
    readonly canvasGradientEnd: string;
    readonly container: string;
    readonly containerTranslucent: string;
    readonly hover: string;
    readonly elevated: string;
    readonly disabled: string;
    readonly sider: string;
    readonly menu: string;
    readonly topbar: string;
    readonly tableHeader: string;
    readonly tableHover: string;
  };
  readonly content: {
    readonly strong: string;
    readonly primary: string;
    readonly secondary: string;
    readonly tertiary: string;
    readonly disabled: string;
    readonly onAction: string;
    readonly onIdentity: string;
  };
  readonly border: {
    readonly default: string;
    readonly subtle: string;
    readonly row: string;
    readonly strong: string;
  };
  readonly action: {
    readonly primary: string;
    readonly hover: string;
    readonly active: string;
    readonly soft: string;
    readonly shadow: string;
  };
  readonly link: {
    readonly default: string;
    readonly hover: string;
    readonly active: string;
  };
  readonly focus: {
    readonly ring: string;
  };
  readonly selection: {
    readonly background: string;
    readonly foreground: string;
  };
  readonly navigation: {
    readonly indicatorStart: string;
    readonly indicatorEnd: string;
    readonly selectedBackgroundStart: string;
    readonly selectedBackgroundEnd: string;
    readonly selectedText: string;
  };
  readonly status: {
    readonly success: ConsoleStatusRoles;
    readonly warning: ConsoleStatusRoles;
    readonly danger: ConsoleStatusRoles;
    readonly info: ConsoleStatusRoles;
  };
  readonly visualization: {
    readonly categorical: readonly [
      string,
      string,
      string,
      string,
      string,
      string,
      string,
      string,
    ];
    readonly fallback: string;
    readonly trendPrimary: string;
    readonly trendSecondary: string;
  };
  readonly identity: {
    readonly gradientStart: string;
    readonly gradientMiddle: string;
    readonly gradientEnd: string;
    readonly outline: string;
    readonly outlineHover: string;
    readonly glow: string;
    readonly glowHover: string;
  };
  readonly effect: {
    readonly shadowStructural: string;
    readonly shadowElevated: string;
    readonly scrollbar: string;
    readonly scrollbarHover: string;
    readonly radiusSurface: string;
    readonly radiusControl: string;
  };
}

export interface ResolvedConsoleTheme {
  readonly id: string;
  readonly mode: ConsoleTheme["mode"];
  readonly token: ResolvedAntDesignToken;
  readonly roles: ConsoleThemeRoles;
  readonly antDesign: ThemeConfig;
  readonly cssVariables: Readonly<Record<string, string>>;
}

type RoleOverrides = {
  surface?: Partial<ConsoleThemeRoles["surface"]>;
  content?: Partial<ConsoleThemeRoles["content"]>;
  border?: Partial<ConsoleThemeRoles["border"]>;
  action?: Partial<ConsoleThemeRoles["action"]>;
  link?: Partial<ConsoleThemeRoles["link"]>;
  focus?: Partial<ConsoleThemeRoles["focus"]>;
  selection?: Partial<ConsoleThemeRoles["selection"]>;
  navigation?: Partial<ConsoleThemeRoles["navigation"]>;
  status?: Partial<{
    [Key in keyof ConsoleThemeRoles["status"]]: Partial<ConsoleStatusRoles>;
  }>;
  visualization?: Partial<ConsoleThemeRoles["visualization"]>;
  identity?: Partial<ConsoleThemeRoles["identity"]>;
  effect?: Partial<ConsoleThemeRoles["effect"]>;
};

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

const mix = (color: string, percentage: number, background = "transparent") =>
  `color-mix(in srgb, ${color} ${percentage}%, ${background})`;

const builtInRoleOverrides: Readonly<Record<string, RoleOverrides>> = {
  "gateway-dark": {
    surface: {
      canvas: "#08090b",
      canvasGradientStart: "#0a0e13",
      canvasGradientEnd: "#08090b",
      container: "#141417",
      containerTranslucent: "rgba(24, 24, 27, 0.55)",
      hover: "rgba(39, 39, 42, 0.55)",
      elevated: "#1b1b1f",
      disabled: "rgba(63, 63, 70, 0.16)",
      sider: "rgba(12, 13, 16, 0.96)",
      menu: "transparent",
      topbar: "rgba(8, 9, 11, 0.9)",
      tableHeader: "rgba(9, 10, 12, 0.4)",
      tableHover: "rgba(39, 39, 42, 0.32)",
    },
    content: {
      strong: "#fafafa",
      primary: "#e4e4e7",
      secondary: "#b4b4bd",
      tertiary: "#8f8f9a",
      disabled: "#85858f",
      onAction: "#083344",
      onIdentity: "#083344",
    },
    border: {
      default: "rgba(63, 63, 70, 0.65)",
      subtle: "rgba(63, 63, 70, 0.35)",
      row: "rgba(63, 63, 70, 0.25)",
      strong: "#52525b",
    },
    action: {
      primary: "#06b6d4",
      hover: "#22d3ee",
      active: "#0891b2",
      soft: "rgba(6, 182, 212, 0.12)",
      shadow: "0 2px 10px -2px rgba(6, 182, 212, 0.5)",
    },
    link: {
      default: "#22d3ee",
      hover: "#67e8f9",
      active: "#0891b2",
    },
    focus: { ring: "rgba(6, 182, 212, 0.35)" },
    navigation: {
      indicatorStart: "#22d3ee",
      indicatorEnd: "#0891b2",
      selectedBackgroundStart: "rgba(6, 182, 212, 0.14)",
      selectedBackgroundEnd: "rgba(6, 182, 212, 0.05)",
      selectedText: "#a5f3fc",
    },
    identity: {
      gradientStart: "#0891b2",
      gradientMiddle: "#06b6d4",
      gradientEnd: "#22d3ee",
      outline: "rgba(34, 211, 238, 0.25)",
      outlineHover: "rgba(34, 211, 238, 0.45)",
      glow: "rgba(6, 182, 212, 0.55)",
      glowHover: "rgba(6, 182, 212, 0.7)",
    },
    effect: {
      shadowStructural:
        "0 1px 2px rgba(0, 0, 0, 0.35), 0 4px 16px -8px rgba(0, 0, 0, 0.4)",
      shadowElevated:
        "0 8px 32px -8px rgba(0, 0, 0, 0.6), 0 2px 8px rgba(0, 0, 0, 0.4)",
      scrollbar: "#3f3f46",
      scrollbarHover: "#52525b",
    },
  },
  "gateway-light": {
    surface: {
      canvas: "#f6f7f9",
      canvasGradientStart: "#f6f7f9",
      canvasGradientEnd: "#f6f7f9",
      container: "#ffffff",
      containerTranslucent: "rgba(255, 255, 255, 0.9)",
      hover: "#f4f4f5",
      elevated: "#ffffff",
      disabled: "#f4f4f5",
      sider: "rgba(255, 255, 255, 0.96)",
      menu: "#ffffff",
      topbar: "rgba(255, 255, 255, 0.9)",
      tableHeader: "#fafafa",
      tableHover: "#f4f4f5",
    },
    content: {
      strong: "#18181b",
      primary: "#27272a",
      secondary: "#52525b",
      tertiary: "#5f5f68",
      disabled: "#6b6b74",
      onAction: "#ffffff",
      onIdentity: "#ffffff",
    },
    border: {
      default: "#d4d4d8",
      subtle: "#e4e4e7",
      row: "#eeeeef",
      strong: "#a1a1aa",
    },
    action: {
      primary: "#0891b2",
      hover: "#0e7490",
      active: "#155e75",
      soft: "rgba(8, 145, 178, 0.1)",
      shadow: "0 2px 8px -2px rgba(8, 145, 178, 0.35)",
    },
    link: {
      default: "#0e7490",
      hover: "#164e63",
      active: "#155e75",
    },
    focus: { ring: "rgba(8, 145, 178, 0.25)" },
    navigation: {
      indicatorStart: "#22d3ee",
      indicatorEnd: "#0891b2",
      selectedBackgroundStart: "rgba(6, 182, 212, 0.14)",
      selectedBackgroundEnd: "rgba(6, 182, 212, 0.05)",
      selectedText: "#0891b2",
    },
    identity: {
      gradientStart: "#155e75",
      gradientMiddle: "#0891b2",
      gradientEnd: "#22d3ee",
      outline: "rgba(8, 145, 178, 0.25)",
      outlineHover: "rgba(8, 145, 178, 0.42)",
      glow: "rgba(8, 145, 178, 0.35)",
      glowHover: "rgba(8, 145, 178, 0.5)",
    },
    effect: {
      shadowStructural: "0 1px 2px rgba(24, 24, 27, 0.06)",
      shadowElevated:
        "0 16px 40px -12px rgba(24, 24, 27, 0.18), 0 2px 8px rgba(24, 24, 27, 0.08)",
      scrollbar: "#d4d4d8",
      scrollbarHover: "#a1a1aa",
    },
  },
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

function buildCSSVariables(
  roles: ConsoleThemeRoles,
): Readonly<Record<string, string>> {
  return {
    "--ag-surface-canvas": roles.surface.canvas,
    "--ag-surface-canvas-gradient-start": roles.surface.canvasGradientStart,
    "--ag-surface-canvas-gradient-end": roles.surface.canvasGradientEnd,
    "--ag-surface-container": roles.surface.container,
    "--ag-surface-container-translucent": roles.surface.containerTranslucent,
    "--ag-surface-hover": roles.surface.hover,
    "--ag-surface-elevated": roles.surface.elevated,
    "--ag-surface-disabled": roles.surface.disabled,
    "--ag-surface-sider": roles.surface.sider,
    "--ag-surface-menu": roles.surface.menu,
    "--ag-surface-topbar": roles.surface.topbar,
    "--ag-surface-table-header": roles.surface.tableHeader,
    "--ag-surface-table-hover": roles.surface.tableHover,
    "--ag-content-strong": roles.content.strong,
    "--ag-content-primary": roles.content.primary,
    "--ag-content-secondary": roles.content.secondary,
    "--ag-content-tertiary": roles.content.tertiary,
    "--ag-content-disabled": roles.content.disabled,
    "--ag-content-on-action": roles.content.onAction,
    "--ag-content-on-identity": roles.content.onIdentity,
    "--ag-border-default": roles.border.default,
    "--ag-border-subtle": roles.border.subtle,
    "--ag-border-row": roles.border.row,
    "--ag-border-strong": roles.border.strong,
    "--ag-action-primary": roles.action.primary,
    "--ag-action-primary-hover": roles.action.hover,
    "--ag-action-primary-active": roles.action.active,
    "--ag-action-primary-soft": roles.action.soft,
    "--ag-action-primary-shadow": roles.action.shadow,
    "--ag-link": roles.link.default,
    "--ag-link-hover": roles.link.hover,
    "--ag-link-active": roles.link.active,
    "--ag-focus-ring": roles.focus.ring,
    "--ag-selection-background": roles.selection.background,
    "--ag-selection-foreground": roles.selection.foreground,
    "--ag-navigation-indicator-start": roles.navigation.indicatorStart,
    "--ag-navigation-indicator-end": roles.navigation.indicatorEnd,
    "--ag-navigation-selected-start": roles.navigation.selectedBackgroundStart,
    "--ag-navigation-selected-end": roles.navigation.selectedBackgroundEnd,
    "--ag-navigation-selected-text": roles.navigation.selectedText,
    "--ag-status-success": roles.status.success.foreground,
    "--ag-status-success-soft": roles.status.success.soft,
    "--ag-status-success-border": roles.status.success.border,
    "--ag-status-warning": roles.status.warning.foreground,
    "--ag-status-warning-soft": roles.status.warning.soft,
    "--ag-status-warning-border": roles.status.warning.border,
    "--ag-status-danger": roles.status.danger.foreground,
    "--ag-status-danger-soft": roles.status.danger.soft,
    "--ag-status-danger-border": roles.status.danger.border,
    "--ag-status-info": roles.status.info.foreground,
    "--ag-status-info-soft": roles.status.info.soft,
    "--ag-status-info-border": roles.status.info.border,
    "--ag-visualization-1": roles.visualization.categorical[0],
    "--ag-visualization-2": roles.visualization.categorical[1],
    "--ag-visualization-3": roles.visualization.categorical[2],
    "--ag-visualization-4": roles.visualization.categorical[3],
    "--ag-visualization-5": roles.visualization.categorical[4],
    "--ag-visualization-6": roles.visualization.categorical[5],
    "--ag-visualization-7": roles.visualization.categorical[6],
    "--ag-visualization-8": roles.visualization.categorical[7],
    "--ag-visualization-fallback": roles.visualization.fallback,
    "--ag-visualization-trend-primary": roles.visualization.trendPrimary,
    "--ag-visualization-trend-secondary": roles.visualization.trendSecondary,
    "--ag-identity-gradient-start": roles.identity.gradientStart,
    "--ag-identity-gradient-middle": roles.identity.gradientMiddle,
    "--ag-identity-gradient-end": roles.identity.gradientEnd,
    "--ag-identity-outline": roles.identity.outline,
    "--ag-identity-outline-hover": roles.identity.outlineHover,
    "--ag-identity-glow": roles.identity.glow,
    "--ag-identity-glow-hover": roles.identity.glowHover,
    "--ag-shadow-structural": roles.effect.shadowStructural,
    "--ag-shadow-elevated": roles.effect.shadowElevated,
    "--ag-scrollbar": roles.effect.scrollbar,
    "--ag-scrollbar-hover": roles.effect.scrollbarHover,
    "--ag-radius-surface": roles.effect.radiusSurface,
    "--ag-radius-control": roles.effect.radiusControl,
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

const legacyThemeVariables = [
  "--ag-bg",
  "--ag-bg-grad-a",
  "--ag-bg-grad-b",
  "--ag-surface",
  "--ag-surface-solid",
  "--ag-overlay",
  "--ag-border",
  "--ag-brand",
  "--ag-brand-soft",
  "--ag-brand-glow",
  "--ag-danger",
  "--ag-danger-soft",
  "--ag-danger-border",
  "--ag-success",
  "--ag-success-soft",
  "--ag-success-border",
  "--ag-warning",
  "--ag-warning-soft",
  "--ag-warning-border",
  "--ag-info",
  "--ag-info-soft",
  "--ag-info-border",
  "--ag-text",
  "--ag-text-strong",
  "--ag-text-dim",
  "--ag-text-mute",
  "--ag-text-faint",
  "--ag-shadow-card",
  "--ag-shadow-pop",
  "--ag-sider",
  "--ag-topbar",
  "--ag-table-header",
  "--ag-table-row-border",
  "--ag-table-hover",
  "--ag-selection-bg",
  "--ag-selection-text",
  "--ag-nav-indicator-start",
  "--ag-nav-indicator-end",
  "--ag-nav-selected-bg-start",
  "--ag-nav-selected-bg-end",
  "--ag-radius",
  "--ag-radius-sm",
] as const;

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
