import { GlobalOutlined, MoonOutlined, SunOutlined } from "@ant-design/icons";
import { Button, Dropdown, Space, Tooltip } from "antd";
import { usePreferences, type AppLocale } from "../lib/preferences";

export function PreferenceControls({ compact = false }: { compact?: boolean }) {
  const { colorMode, locale, setLocale, toggleColorMode, t } = usePreferences();
  const themeLabel = t(
    colorMode === "dark" ? "common.theme.light" : "common.theme.dark",
  );

  return (
    <Space size={compact ? 4 : 8}>
      <Tooltip title={themeLabel}>
        <Button
          type="text"
          aria-label={themeLabel}
          icon={colorMode === "dark" ? <SunOutlined /> : <MoonOutlined />}
          onClick={toggleColorMode}
        />
      </Tooltip>
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
