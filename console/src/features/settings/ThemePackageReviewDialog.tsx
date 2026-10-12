import { Alert, Button, Space } from "antd";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import type { PendingThemePackage } from "./siteSettingsModel";
import { ThemePreview } from "./ThemePreview";

/** Review a validated theme package before installing or replacing it. */
export function ThemePackageReviewDialog({
  pending,
  installing,
  error,
  onClose,
  onCommit,
}: {
  pending: PendingThemePackage | null;
  installing: boolean;
  error: unknown;
  onClose: () => void;
  onCommit: () => void;
}) {
  const { text } = usePreferences();
  return (
    <Modal
      open={pending !== null}
      width={680}
      busy={installing}
      title={
        pending?.validation.status === "replaceable"
          ? text("替换上传主题", "Replace uploaded theme")
          : text("安装主题包", "Install theme package")
      }
      onClose={onClose}
      footer={
        <Space>
          <Button onClick={onClose} disabled={installing}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            disabled={pending?.validation.status === "reserved"}
            loading={installing}
            onClick={onCommit}
          >
            {pending?.validation.status === "replaceable"
              ? text("确认替换", "Replace theme")
              : pending?.validation.status === "reserved"
                ? text("ID 不可用", "ID unavailable")
                : text("安装并启用", "Install and enable")}
          </Button>
        </Space>
      }
    >
      {pending && (
        <div className="ag-theme-package-review">
          {error !== null && (
            <ErrorBanner
              error={error}
              title={text("主题包处理失败", "Theme package failed")}
            />
          )}
          <Alert
            showIcon
            type={
              pending.validation.status === "available"
                ? "success"
                : pending.validation.status === "replaceable"
                  ? "warning"
                  : "error"
            }
            title={
              pending.validation.status === "available"
                ? text(
                    "校验通过，可以安装",
                    "Validation passed; ready to install",
                  )
                : pending.validation.status === "replaceable"
                  ? text(
                      "该 ID 已由上传主题使用，将创建新版本",
                      "This ID belongs to an uploaded theme; a new version will replace it",
                    )
                  : text(
                      "该 ID 属于内置或目录主题，不能通过上传覆盖",
                      "This ID belongs to a built-in or directory theme and cannot be overwritten",
                    )
            }
          />
          <ThemePreview theme={pending.validation.theme} />
          <dl className="ag-theme-package-details">
            <div>
              <dt>{text("名称", "Name")}</dt>
              <dd>{pending.validation.theme.name}</dd>
            </div>
            <div>
              <dt>ID</dt>
              <dd>{pending.validation.theme.id}</dd>
            </div>
            <div>
              <dt>{text("模式", "Mode")}</dt>
              <dd>
                {pending.validation.theme.mode === "dark"
                  ? text("深色", "Dark")
                  : text("浅色", "Light")}
              </dd>
            </div>
            <div>
              <dt>{text("文件", "File")}</dt>
              <dd>{pending.fileName}</dd>
            </div>
          </dl>
          {pending.validation.theme.description && (
            <p className="ag-theme-package-description">
              {pending.validation.theme.description}
            </p>
          )}
          <p className="ag-theme-package-safety-note">
            {text(
              "主题包将在服务端按严格 v1 规范再次解析；安装新主题后，还需要保存站点设置才会向用户开放。",
              "The server strictly parses the package against v1. After installing a new theme, save site settings to make it available to users.",
            )}
          </p>
        </div>
      )}
    </Modal>
  );
}
