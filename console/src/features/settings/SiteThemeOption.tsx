import { DeleteOutlined } from "@ant-design/icons";
import { Button, Checkbox, Popconfirm, Radio } from "antd";
import type { ConsoleTheme } from "../../client";
import { usePreferences } from "../../lib/preferences";
import { ThemePreview } from "./ThemePreview";

/** One theme in the site's theme list: enable it, make it default, delete it. */
export function SiteThemeOption({
  theme,
  enabled,
  onlyEnabled,
  deletionBlocked,
  isDefault,
  deleting,
  onToggle,
  onMakeDefault,
  onDelete,
}: {
  theme: ConsoleTheme;
  enabled: boolean;
  onlyEnabled: boolean;
  deletionBlocked: boolean;
  isDefault: boolean;
  deleting: boolean;
  onToggle: (enabled: boolean) => void;
  onMakeDefault: () => void;
  onDelete: () => void;
}) {
  const { t } = usePreferences();
  const source = theme.source ?? "builtin";
  return (
    <article
      key={theme.id}
      className="ag-theme-option"
      data-enabled={enabled}
      data-theme-mode={theme.mode}
    >
      <div className="ag-theme-option-header">
        <Checkbox
          checked={enabled}
          disabled={onlyEnabled}
          onChange={(event) => onToggle(event.target.checked)}
        >
          <span className="ag-theme-option-name">{theme.name}</span>
        </Checkbox>
        <span className="ag-theme-option-meta">
          <span className="ag-theme-option-mode">
            {theme.mode === "dark" ? t("settings.dark") : t("settings.light")}
          </span>
          <span className="ag-theme-option-source" data-source={source}>
            {source === "managed"
              ? t("settings.uploaded")
              : source === "directory"
                ? t("settings.directory")
                : t("settings.builtIn")}
          </span>
        </span>
      </div>
      <ThemePreview theme={theme} />
      <p>{theme.description}</p>
      <div className="ag-theme-option-footer">
        <Radio checked={isDefault} disabled={!enabled} onChange={onMakeDefault}>
          {t("settings.useAsDefault")}
        </Radio>
        {source === "managed" && (
          <Popconfirm
            disabled={deletionBlocked}
            title={t("settings.deleteThisUploadedTheme")}
            description={t("settings.thisCannotBeUndoneUploadThePackageAgain")}
            okText={t("settings.delete")}
            cancelText={t("settings.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={onDelete}
          >
            <Button
              danger
              type="text"
              size="small"
              icon={<DeleteOutlined />}
              aria-label={t("settings.delete")}
              loading={deleting}
              disabled={deletionBlocked}
              title={
                deletionBlocked
                  ? t("settings.disableTheThemeAndSaveSiteSettingsFirst")
                  : undefined
              }
            >
              {t("settings.delete")}
            </Button>
          </Popconfirm>
        )}
      </div>
    </article>
  );
}
