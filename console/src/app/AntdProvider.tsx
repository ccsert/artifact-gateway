import { App as AntdApp, ConfigProvider, theme as antdTheme } from "antd";
import zhCN from "antd/locale/zh_CN";
import type { ReactNode } from "react";

const fontSans =
  "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif";

export function AntdProvider({ children }: { children: ReactNode }) {
  return (
    <ConfigProvider
      componentSize="medium"
      locale={zhCN}
      theme={{
        algorithm: antdTheme.darkAlgorithm,
        components: {
          Button: {
            borderRadius: 8,
            controlHeight: 34,
            fontWeight: 500,
            primaryShadow: "0 2px 10px -2px rgba(6, 182, 212, 0.5)",
          },
          Card: {
            headerBg: "transparent",
          },
          Input: {
            borderRadius: 8,
            activeShadow: "0 0 0 2px rgba(6, 182, 212, 0.18)",
          },
          Modal: {
            borderRadiusLG: 12,
            headerBg: "transparent",
          },
          Tooltip: {
            borderRadius: 6,
          },
          Empty: {
            colorTextDisabled: "#52525b",
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
          Table: {
            borderColor: "#27272a",
            expandIconBg: "#18181b",
            headerBg: "transparent",
            headerColor: "#71717a",
            headerSplitColor: "#27272a",
            rowExpandedBg: "#111114",
            rowHoverBg: "rgba(39, 39, 42, 0.55)",
          },
        },
        token: {
          borderRadius: 10,
          colorBgBase: "#08090b",
          colorBgContainer: "#141417",
          colorBgElevated: "#1b1b1f",
          colorBgLayout: "#08090b",
          colorBgSpotlight: "#27272a",
          colorBorder: "rgba(63, 63, 70, 0.65)",
          colorBorderSecondary: "rgba(63, 63, 70, 0.35)",
          colorFillAlter: "rgba(63, 63, 70, 0.16)",
          colorFillContent: "rgba(63, 63, 70, 0.3)",
          colorFillContentHover: "rgba(63, 63, 70, 0.42)",
          colorInfo: "#06b6d4",
          colorPrimary: "#06b6d4",
          colorPrimaryHover: "#22d3ee",
          colorPrimaryActive: "#0891b2",
          colorText: "#e4e4e7",
          colorTextBase: "#e4e4e7",
          colorTextQuaternary: "#52525b",
          colorTextSecondary: "#a1a1aa",
          colorTextTertiary: "#71717a",
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
