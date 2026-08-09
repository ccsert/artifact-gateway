import { App as AntdApp, ConfigProvider, theme as antdTheme } from "antd";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import type { ReactNode } from "react";
import { usePreferences } from "../lib/preferences";

const fontSans =
  "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif";

export function AntdProvider({ children }: { children: ReactNode }) {
  const { colorMode, locale } = usePreferences();
  const dark = colorMode === "dark";

  return (
    <ConfigProvider
      componentSize="medium"
      locale={locale === "zh-CN" ? zhCN : enUS}
      theme={{
        algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
        components: {
          Button: {
            borderRadius: 8,
            controlHeight: 34,
            fontWeight: 500,
            defaultBg: dark ? "#18181b" : "#ffffff",
            defaultBorderColor: dark ? "#3f3f46" : "#d4d4d8",
            defaultColor: dark ? "#e4e4e7" : "#27272a",
            primaryShadow: dark
              ? "0 2px 10px -2px rgba(6, 182, 212, 0.5)"
              : "0 2px 8px -2px rgba(8, 145, 178, 0.35)",
          },
          Card: {
            headerBg: "transparent",
          },
          Input: {
            borderRadius: 8,
            activeShadow: dark
              ? "0 0 0 2px rgba(6, 182, 212, 0.18)"
              : "0 0 0 2px rgba(8, 145, 178, 0.14)",
          },
          Modal: {
            borderRadiusLG: 12,
            headerBg: "transparent",
          },
          Tooltip: {
            borderRadius: 6,
          },
          Empty: {
            colorTextDisabled: dark ? "#52525b" : "#a1a1aa",
          },
          Menu: {
            darkGroupTitleColor: "#52525b",
            darkItemBg: "transparent",
            darkItemColor: "#a1a1aa",
            darkItemHoverBg: "rgba(39, 39, 42, 0.65)",
            darkItemHoverColor: "#fafafa",
            darkItemSelectedBg: "rgba(6, 182, 212, 0.12)",
            darkItemSelectedColor: "#a5f3fc",
            darkSubMenuItemBg: "transparent",
            itemBorderRadius: 8,
            itemHeight: 38,
            itemMarginBlock: 3,
            itemMarginInline: 4,
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
            headerColor: dark ? "#71717a" : "#52525b",
            headerSplitColor: dark ? "#27272a" : "#e4e4e7",
            rowExpandedBg: dark ? "#111114" : "#fafafa",
            rowHoverBg: dark ? "rgba(39, 39, 42, 0.55)" : "#f4f4f5",
          },
        },
        token: {
          borderRadius: 10,
          colorBgBase: dark ? "#08090b" : "#f6f7f9",
          colorBgContainer: dark ? "#141417" : "#ffffff",
          colorBgElevated: dark ? "#1b1b1f" : "#ffffff",
          colorBgLayout: dark ? "#08090b" : "#f6f7f9",
          colorBgSpotlight: dark ? "#27272a" : "#27272a",
          colorBorder: dark ? "rgba(63, 63, 70, 0.65)" : "#d4d4d8",
          colorBorderSecondary: dark ? "rgba(63, 63, 70, 0.35)" : "#e4e4e7",
          colorFillAlter: dark ? "rgba(63, 63, 70, 0.16)" : "#f4f4f5",
          colorFillContent: dark ? "rgba(63, 63, 70, 0.3)" : "#e4e4e7",
          colorFillContentHover: dark ? "rgba(63, 63, 70, 0.42)" : "#d4d4d8",
          colorError: dark ? "#fb7185" : "#be123c",
          colorInfo: dark ? "#06b6d4" : "#0891b2",
          colorLink: dark ? "#22d3ee" : "#0e7490",
          colorLinkActive: dark ? "#0891b2" : "#155e75",
          colorLinkHover: dark ? "#67e8f9" : "#164e63",
          colorPrimary: dark ? "#06b6d4" : "#0891b2",
          colorPrimaryHover: dark ? "#22d3ee" : "#0e7490",
          colorPrimaryActive: dark ? "#0891b2" : "#155e75",
          colorSuccess: dark ? "#34d399" : "#047857",
          colorText: dark ? "#e4e4e7" : "#27272a",
          colorTextBase: dark ? "#e4e4e7" : "#27272a",
          colorTextDisabled: dark ? "#52525b" : "#71717a",
          colorTextQuaternary: dark ? "#52525b" : "#a1a1aa",
          colorTextSecondary: dark ? "#a1a1aa" : "#52525b",
          colorTextTertiary: dark ? "#71717a" : "#71717a",
          colorWarning: dark ? "#fbbf24" : "#a16207",
          controlHeight: 34,
          controlOutline: "rgba(6, 182, 212, 0.22)",
          fontFamily: fontSans,
          fontSize: 14,
          motionDurationMid: "0.18s",
          motionEaseOut: "cubic-bezier(0.16, 1, 0.3, 1)",
        },
      }}
      variant="outlined"
    >
      <AntdApp className="min-h-screen">{children}</AntdApp>
    </ConfigProvider>
  );
}
