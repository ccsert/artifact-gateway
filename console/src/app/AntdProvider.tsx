import { App as AntdApp, ConfigProvider } from "antd";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import type { ReactNode } from "react";
import { usePreferences } from "../lib/preferences";

export function AntdProvider({ children }: { children: ReactNode }) {
  const { locale, resolvedTheme } = usePreferences();

  return (
    <ConfigProvider
      // Chinese labels keep their natural spacing (DESIGN.md): no space is
      // inserted between the two characters of a label like 删除.
      button={{ autoInsertSpace: false }}
      componentSize="medium"
      locale={locale === "zh-CN" ? zhCN : enUS}
      theme={resolvedTheme.antDesign}
      variant="outlined"
    >
      <AntdApp className="min-h-screen">{children}</AntdApp>
    </ConfigProvider>
  );
}
