import { useState } from "react";
import { App } from "antd";
import {
  deleteConsoleThemePackage,
  installConsoleThemePackage,
  replaceConsoleThemePackage,
  validateConsoleThemePackage,
  type ConsoleTheme,
  type ConsoleThemePackage,
  type SiteSettings,
} from "../../client";
import { usePreferences } from "../../lib/preferences";
import {
  maxEnabledThemes,
  maxThemePackageBytes,
  readAsText,
  type PendingThemePackage,
  type SiteSettingsDraft,
} from "./siteSettingsModel";

/**
 * Uploaded theme packages: validate a file, install or replace it after
 * review, and delete an uploaded theme no one has enabled. Installed themes
 * update the saved settings at once; enabling one stays a draft change.
 */
export function useThemePackages({
  settings,
  draft,
  setSettings,
  setDraft,
}: {
  settings: SiteSettings | null;
  draft: SiteSettingsDraft;
  setSettings: (next: SiteSettings) => void;
  setDraft: (update: (current: SiteSettingsDraft) => SiteSettingsDraft) => void;
}) {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const [themePackageError, setThemePackageError] = useState<unknown>(null);
  const [pendingThemePackage, setPendingThemePackage] =
    useState<PendingThemePackage | null>(null);
  const [validatingThemePackage, setValidatingThemePackage] = useState(false);
  const [installingThemePackage, setInstallingThemePackage] = useState(false);
  const [deletingThemeID, setDeletingThemeID] = useState<string | null>(null);

  const selectThemePackage = async (file: File) => {
    setThemePackageError(null);
    setPendingThemePackage(null);
    if (file.size <= 0 || file.size > maxThemePackageBytes) {
      setThemePackageError(
        new Error(
          text(
            "主题包必须是 1 字节到 256 KiB 的 JSON 文件",
            "The theme package must be a JSON file between 1 byte and 256 KiB",
          ),
        ),
      );
      return;
    }

    setValidatingThemePackage(true);
    try {
      let themePackage: ConsoleThemePackage;
      try {
        themePackage = JSON.parse(
          await readAsText(file),
        ) as ConsoleThemePackage;
      } catch {
        throw new Error(
          text(
            "主题包不是有效的 JSON 文件",
            "The theme package is not valid JSON",
          ),
        );
      }
      const { data, error } = await validateConsoleThemePackage({
        body: themePackage,
      });
      if (error || !data) {
        throw error ?? new Error("theme package validation failed");
      }
      setPendingThemePackage({ fileName: file.name, validation: data });
    } catch (error) {
      setThemePackageError(error);
    } finally {
      setValidatingThemePackage(false);
    }
  };

  const commitThemePackage = async () => {
    if (
      !settings ||
      !pendingThemePackage ||
      pendingThemePackage.validation.status === "reserved"
    ) {
      return;
    }
    const { validation } = pendingThemePackage;
    setInstallingThemePackage(true);
    setThemePackageError(null);
    try {
      const result =
        validation.status === "available"
          ? await installConsoleThemePackage({ body: validation.theme })
          : validation.existingVersion
            ? await replaceConsoleThemePackage({
                body: validation.theme,
                path: { themeId: validation.theme.id },
                headers: { "If-Match": validation.existingVersion },
              })
            : {
                error: new Error("managed theme version is unavailable"),
                data: undefined,
              };
      if (result.error || !result.data) {
        throw result.error ?? new Error("theme package mutation failed");
      }

      const replacingExistingTheme = settings.availableThemes.some(
        (theme) => theme.id === result.data.id,
      );
      const availableThemes = replacingExistingTheme
        ? settings.availableThemes.map((theme) =>
            theme.id === result.data.id ? result.data : theme,
          )
        : [...settings.availableThemes, result.data];
      const nextSettings = { ...settings, availableThemes };
      setSettings(nextSettings);
      const stageForEnable =
        validation.status === "available" &&
        draft.enabledThemeIds.length < maxEnabledThemes;
      if (stageForEnable) {
        setDraft((current) => ({
          ...current,
          enabledThemeIds: current.enabledThemeIds.includes(result.data.id)
            ? current.enabledThemeIds
            : [...current.enabledThemeIds, result.data.id],
        }));
      }
      setPendingThemePackage(null);
      void message.success(
        validation.status === "available"
          ? stageForEnable
            ? text(
                "主题已安装并加入启用列表；请保存并应用后对用户生效",
                "Theme installed and added to the enabled set; save and apply to make it available to users",
              )
            : text(
                "主题已安装；启用主题已达到 32 个上限，请先停用其他主题",
                "Theme installed; the 32-theme enabled limit has been reached, so disable another theme first",
              )
          : text(
              "主题包已替换；已启用主题将在重新载入设置时使用新版本",
              "Theme package replaced; enabled themes will use the new version when settings reload",
            ),
      );
    } catch (error) {
      setThemePackageError(error);
    } finally {
      setInstallingThemePackage(false);
    }
  };

  const deleteManagedTheme = async (theme: ConsoleTheme) => {
    if (!settings || theme.source !== "managed" || !theme.version) return;
    const inUse =
      settings.enabledThemeIds.includes(theme.id) ||
      draft.enabledThemeIds.includes(theme.id);
    if (inUse) return;

    setDeletingThemeID(theme.id);
    setThemePackageError(null);
    try {
      const { error } = await deleteConsoleThemePackage({
        path: { themeId: theme.id },
        headers: { "If-Match": theme.version },
      });
      if (error) throw error;

      const nextSettings = {
        ...settings,
        availableThemes: settings.availableThemes.filter(
          (item) => item.id !== theme.id,
        ),
      };
      setSettings(nextSettings);
      setDraft((current) => ({
        ...current,
        enabledThemeIds: current.enabledThemeIds.filter(
          (themeID) => themeID !== theme.id,
        ),
      }));
      void message.success(text("已删除上传主题", "Uploaded theme deleted"));
    } catch (error) {
      setThemePackageError(error);
    } finally {
      setDeletingThemeID(null);
    }
  };

  return {
    themePackageError,
    pendingThemePackage,
    closeThemePackage: () => setPendingThemePackage(null),
    validatingThemePackage,
    installingThemePackage,
    deletingThemeID,
    selectThemePackage,
    commitThemePackage,
    deleteManagedTheme,
  };
}
