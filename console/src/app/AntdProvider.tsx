import { App as AntdApp, ConfigProvider } from "antd";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import type { ReactNode } from "react";
import { buildConsoleThemeConfig } from "../lib/consoleTheme";
import { usePreferences } from "../lib/preferences";

export function AntdProvider({ children }: { children: ReactNode }) {
  const { activeTheme, locale } = usePreferences();

  return (
    <ConfigProvider
      componentSize="medium"
      locale={locale === "zh-CN" ? zhCN : enUS}
      theme={buildConsoleThemeConfig(activeTheme)}
      variant="outlined"
    >
      <AntdApp className="min-h-screen">{children}</AntdApp>
    </ConfigProvider>
  );
}
