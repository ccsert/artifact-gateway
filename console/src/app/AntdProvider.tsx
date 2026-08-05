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
          Card: {
            headerBg: "transparent",
          },
          Menu: {
            darkGroupTitleColor: "#52525b",
            darkItemBg: "transparent",
            darkItemColor: "#a1a1aa",
            darkItemHoverBg: "rgba(39, 39, 42, 0.65)",
            darkItemHoverColor: "#e4e4e7",
            darkItemSelectedBg: "rgba(6, 182, 212, 0.12)",
            darkItemSelectedColor: "#a5f3fc",
            darkSubMenuItemBg: "transparent",
            itemBorderRadius: 6,
            itemHeight: 36,
            itemMarginBlock: 2,
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
          borderRadius: 6,
          colorBgBase: "#090a0c",
          colorBgContainer: "#18181b",
          colorBgElevated: "#18181b",
          colorBgLayout: "#090a0c",
          colorBgSpotlight: "#27272a",
          colorBorder: "#3f3f46",
          colorBorderSecondary: "#27272a",
          colorFillAlter: "rgba(63, 63, 70, 0.18)",
          colorFillContent: "rgba(63, 63, 70, 0.32)",
          colorFillContentHover: "rgba(63, 63, 70, 0.44)",
          colorInfo: "#06b6d4",
          colorPrimary: "#06b6d4",
          colorText: "#e4e4e7",
          colorTextBase: "#e4e4e7",
          colorTextQuaternary: "#52525b",
          colorTextSecondary: "#a1a1aa",
          colorTextTertiary: "#71717a",
          controlHeight: 36,
          controlOutline: "rgba(6, 182, 212, 0.22)",
          fontFamily: fontSans,
          fontSize: 14,
        },
      }}
      variant="outlined"
    >
      <AntdApp className="min-h-screen">{children}</AntdApp>
    </ConfigProvider>
  );
}
