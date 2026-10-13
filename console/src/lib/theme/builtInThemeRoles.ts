import type { RoleOverrides } from "./semanticThemeTypes";

export const mix = (
  color: string,
  percentage: number,
  background = "transparent",
) => `color-mix(in srgb, ${color} ${percentage}%, ${background})`;

// Design language v2 ("refined engineering console", issue 319): neutral layers
// carry hierarchy, the primary action is a solid foreground fill, and the
// signal color is reserved for links, focus, and current location.
export const builtInRoleOverrides: Readonly<Record<string, RoleOverrides>> = {
  "gateway-dark": {
    surface: {
      canvas: "#09090b",
      canvasGradientStart: "#09090b",
      canvasGradientEnd: "#09090b",
      container: "#111114",
      containerTranslucent: "rgba(17, 17, 20, 0.82)",
      hover: "rgba(255, 255, 255, 0.045)",
      elevated: "#18181c",
      disabled: "rgba(255, 255, 255, 0.04)",
      sider: "#0c0c0f",
      menu: "transparent",
      topbar: "rgba(9, 9, 11, 0.86)",
      tableHeader: "transparent",
      tableHover: "rgba(255, 255, 255, 0.035)",
    },
    content: {
      strong: "#fafafa",
      primary: "#ededef",
      secondary: "#a1a1aa",
      tertiary: "#8b8b94",
      disabled: "#85858f",
      onAction: "#09090b",
      onIdentity: "#09090b",
    },
    border: {
      default: "rgba(255, 255, 255, 0.1)",
      subtle: "rgba(255, 255, 255, 0.07)",
      row: "rgba(255, 255, 255, 0.06)",
      strong: "rgba(255, 255, 255, 0.18)",
    },
    action: {
      // content.strong, kept distinct from the body text that selection mixes.
      primary: "#fafafa",
      hover: "#ffffff",
      active: "#e4e4e7",
      soft: "rgba(255, 255, 255, 0.08)",
      shadow: "none",
    },
    link: {
      default: "#22d3ee",
      hover: "#67e8f9",
      active: "#0891b2",
    },
    focus: { ring: "rgba(34, 211, 238, 0.4)" },
    navigation: {
      indicatorStart: "#22d3ee",
      indicatorEnd: "#22d3ee",
      selectedBackgroundStart: "rgba(255, 255, 255, 0.06)",
      selectedBackgroundEnd: "rgba(255, 255, 255, 0.06)",
      selectedText: "#fafafa",
    },
    identity: {
      gradientStart: "#fafafa",
      gradientMiddle: "#ededef",
      gradientEnd: "#d4d4d8",
      outline: "rgba(255, 255, 255, 0.12)",
      outlineHover: "rgba(255, 255, 255, 0.24)",
      glow: "rgba(255, 255, 255, 0)",
      glowHover: "rgba(255, 255, 255, 0.08)",
    },
    effect: {
      shadowStructural: "none",
      shadowElevated:
        "0 16px 48px -12px rgba(0, 0, 0, 0.6), 0 0 0 1px rgba(255, 255, 255, 0.06)",
      scrollbar: "#2a2a30",
      scrollbarHover: "#3f3f46",
    },
  },
  "gateway-light": {
    surface: {
      canvas: "#f7f7f8",
      canvasGradientStart: "#f7f7f8",
      canvasGradientEnd: "#f7f7f8",
      container: "#ffffff",
      containerTranslucent: "rgba(255, 255, 255, 0.9)",
      hover: "#f2f2f4",
      elevated: "#ffffff",
      disabled: "#f4f4f5",
      sider: "#fbfbfc",
      menu: "transparent",
      topbar: "rgba(247, 247, 248, 0.86)",
      tableHeader: "transparent",
      tableHover: "#f7f7f8",
    },
    content: {
      strong: "#09090b",
      primary: "#18181b",
      secondary: "#52525b",
      tertiary: "#63636c",
      disabled: "#6b6b74",
      onAction: "#ffffff",
      onIdentity: "#ffffff",
    },
    border: {
      default: "#e4e4e7",
      subtle: "#ececef",
      row: "#f0f0f2",
      strong: "#d4d4d8",
    },
    action: {
      // content.strong, kept distinct from the body text that selection mixes.
      primary: "#09090b",
      hover: "#27272a",
      active: "#3f3f46",
      soft: "rgba(24, 24, 27, 0.06)",
      shadow: "none",
    },
    link: {
      default: "#0e7490",
      hover: "#164e63",
      active: "#155e75",
    },
    focus: { ring: "rgba(14, 116, 144, 0.35)" },
    navigation: {
      indicatorStart: "#0e7490",
      indicatorEnd: "#0e7490",
      selectedBackgroundStart: "rgba(24, 24, 27, 0.05)",
      selectedBackgroundEnd: "rgba(24, 24, 27, 0.05)",
      selectedText: "#09090b",
    },
    identity: {
      gradientStart: "#18181b",
      gradientMiddle: "#27272a",
      gradientEnd: "#3f3f46",
      outline: "rgba(24, 24, 27, 0.12)",
      outlineHover: "rgba(24, 24, 27, 0.24)",
      glow: "rgba(24, 24, 27, 0)",
      glowHover: "rgba(24, 24, 27, 0.08)",
    },
    effect: {
      shadowStructural: "none",
      shadowElevated:
        "0 16px 40px -12px rgba(24, 24, 27, 0.16), 0 0 0 1px rgba(24, 24, 27, 0.06)",
      scrollbar: "#d4d4d8",
      scrollbarHover: "#a1a1aa",
    },
  },
};
