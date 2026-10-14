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
  const { t } = usePreferences();
  return (
    <Modal
      open={pending !== null}
      width={680}
      busy={installing}
      title={
        pending?.validation.status === "replaceable"
          ? t("settings.replaceUploadedTheme")
          : t("settings.installThemePackage")
      }
      onClose={onClose}
      footer={
        <Space>
          <Button onClick={onClose} disabled={installing}>
            {t("settings.cancel")}
          </Button>
          <Button
            type="primary"
            disabled={pending?.validation.status === "reserved"}
            loading={installing}
            onClick={onCommit}
          >
            {pending?.validation.status === "replaceable"
              ? t("settings.replaceTheme")
              : pending?.validation.status === "reserved"
                ? t("settings.iDUnavailable")
                : t("settings.installAndEnable")}
          </Button>
        </Space>
      }
    >
      {pending && (
        <div className="ag-theme-package-review">
          {error !== null && (
            <ErrorBanner
              error={error}
              title={t("settings.themePackageFailed")}
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
                ? t("settings.validationPassedReadyToInstall")
                : pending.validation.status === "replaceable"
                  ? t("settings.thisIDBelongsToAnUploadedThemeA")
                  : t("settings.thisIDBelongsToABuiltInOr")
            }
          />
          <ThemePreview theme={pending.validation.theme} />
          <dl className="ag-theme-package-details">
            <div>
              <dt>{t("settings.name")}</dt>
              <dd>{pending.validation.theme.name}</dd>
            </div>
            <div>
              <dt>ID</dt>
              <dd>{pending.validation.theme.id}</dd>
            </div>
            <div>
              <dt>{t("settings.mode")}</dt>
              <dd>
                {pending.validation.theme.mode === "dark"
                  ? t("settings.dark")
                  : t("settings.light")}
              </dd>
            </div>
            <div>
              <dt>{t("settings.file")}</dt>
              <dd>{pending.fileName}</dd>
            </div>
          </dl>
          {pending.validation.theme.description && (
            <p className="ag-theme-package-description">
              {pending.validation.theme.description}
            </p>
          )}
          <p className="ag-theme-package-safety-note">
            {t("settings.theServerStrictlyParsesThePackageAgainstV1")}
          </p>
        </div>
      )}
    </Modal>
  );
}
