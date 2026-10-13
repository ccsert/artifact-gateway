import { theme as antdTheme, type ThemeConfig } from "antd";
import type { ConsoleTheme } from "../../client";

export type ResolvedAntDesignToken = ReturnType<
  typeof antdTheme.getDesignToken
>;

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

export type RoleOverrides = {
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
