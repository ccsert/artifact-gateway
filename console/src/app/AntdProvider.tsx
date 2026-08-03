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
        components: {
          Menu: {
            darkGroupTitleColor: '#52525b',
            darkItemBg: 'transparent',
            darkItemColor: '#a1a1aa',
            darkItemHoverBg: 'rgba(39, 39, 42, 0.65)',
            darkItemHoverColor: '#e4e4e7',
            darkItemSelectedBg: 'rgba(6, 182, 212, 0.12)',
            darkItemSelectedColor: '#a5f3fc',
            darkSubMenuItemBg: 'transparent',
            itemBorderRadius: 6,
            itemHeight: 36,
            itemMarginBlock: 2,
            itemMarginInline: 4,
          },
        },
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
