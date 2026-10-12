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
  const { text } = usePreferences();
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
            {theme.mode === "dark"
              ? text("深色", "Dark")
              : text("浅色", "Light")}
          </span>
          <span className="ag-theme-option-source" data-source={source}>
            {source === "managed"
              ? text("已上传", "Uploaded")
              : source === "directory"
                ? text("目录", "Directory")
                : text("内置", "Built in")}
          </span>
        </span>
      </div>
      <ThemePreview theme={theme} />
      <p>{theme.description}</p>
      <div className="ag-theme-option-footer">
        <Radio checked={isDefault} disabled={!enabled} onChange={onMakeDefault}>
          {text("设为默认", "Use as default")}
        </Radio>
        {source === "managed" && (
          <Popconfirm
            disabled={deletionBlocked}
            title={text("删除这个上传主题？", "Delete this uploaded theme?")}
            description={text(
              "删除后无法恢复；若仍需使用，需要重新上传主题包。",
              "This cannot be undone; upload the package again to restore it.",
            )}
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={onDelete}
          >
            <Button
              danger
              type="text"
              size="small"
              icon={<DeleteOutlined />}
              aria-label={text("删除", "Delete")}
              loading={deleting}
              disabled={deletionBlocked}
              title={
                deletionBlocked
                  ? text(
                      "请先取消启用并保存站点设置",
                      "Disable the theme and save site settings first",
                    )
                  : undefined
              }
            >
              {text("删除", "Delete")}
            </Button>
          </Popconfirm>
        )}
      </div>
    </article>
  );
}
