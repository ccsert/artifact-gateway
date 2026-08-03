import { App as AntdApp, ConfigProvider, theme as antdTheme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import type { ReactNode } from 'react';

const fontSans =
  "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif";

export function AntdProvider({ children }: { children: ReactNode }) {
  return (
    <ConfigProvider
      componentSize="medium"
      locale={zhCN}
      theme={{
        algorithm: antdTheme.darkAlgorithm,
        token: {
          borderRadius: 6,
          colorBgBase: '#090a0c',
          colorBorder: '#3f3f46',
          colorInfo: '#06b6d4',
          colorPrimary: '#06b6d4',
          colorTextBase: '#e4e4e7',
          controlHeight: 36,
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
