import {
  CheckOutlined,
  GlobalOutlined,
  MoonOutlined,
  SunOutlined,
} from "@ant-design/icons";
import { Button, Dropdown, Space } from "antd";
import { useState } from "react";
import { flushSync } from "react-dom";
import { usePreferences, type AppLocale } from "../lib/preferences";

export function PreferenceControls({ compact = false }: { compact?: boolean }) {
  const [themeMenuOpen, setThemeMenuOpen] = useState(false);
  const {
    activeTheme,
    availableThemes,
    colorMode,
    locale,
    setLocale,
    setThemeId,
    themeId,
    t,
  } = usePreferences();
  const themeLabel = t("common.theme.current", { name: activeTheme.name });

  return (
    <Space size={compact ? 4 : 8}>
      <Dropdown
        destroyOnHidden
        classNames={{ root: "ag-theme-dropdown" }}
        open={themeMenuOpen}
        onOpenChange={setThemeMenuOpen}
        trigger={["click"]}
        menu={{
          selectable: true,
          selectedKeys: [themeId],
          items: availableThemes.map((theme) => ({
            key: theme.id,
            icon: theme.id === themeId ? <CheckOutlined /> : null,
            label: (
              <span className="ag-theme-menu-item">
                <span
                  className="ag-theme-menu-swatch"
                  style={{ backgroundColor: theme.token.colorPrimary }}
                />
                <span>{theme.name}</span>
                <small>{theme.mode === "dark" ? "Dark" : "Light"}</small>
              </span>
            ),
          })),
          onClick: ({ key }) => {
            // Close the overlay before the View Transition captures its old
            // snapshot; otherwise a desktop-width popup can briefly overflow
            // after the viewport changes to mobile.
            flushSync(() => setThemeMenuOpen(false));
            setThemeId(key);
          },
        }}
      >
        <Button
          className="ag-theme-toggle"
          type="text"
          aria-label={`${t("common.theme.select")}，${themeLabel}`}
          data-color-mode={colorMode}
          data-theme-id={themeId}
          icon={
            <span className="ag-theme-toggle-icon">
              {colorMode === "dark" ? <MoonOutlined /> : <SunOutlined />}
            </span>
          }
        />
      </Dropdown>
      <Dropdown
        trigger={["click"]}
        menu={{
          selectable: true,
          selectedKeys: [locale],
          items: [
            { key: "zh-CN", label: t("common.language.zh") },
            { key: "en-US", label: t("common.language.en") },
          ],
          onClick: ({ key }) => setLocale(key as AppLocale),
        }}
      >
        <Button
          type="text"
          aria-label={t("common.language")}
          icon={<GlobalOutlined />}
        >
          {locale === "zh-CN" ? "中" : "EN"}
        </Button>
      </Dropdown>
    </Space>
  );
}
