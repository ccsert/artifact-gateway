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
  const { t } = usePreferences();
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
        new Error(t("settings.theThemePackageMustBeAJSONFile")),
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
        throw new Error(t("settings.theThemePackageIsNotValidJSON"));
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
            ? t("settings.themeInstalledAndAddedToTheEnabledSet")
            : t("settings.themeInstalledThe32ThemeEnabledLimitHas")
          : t("settings.themePackageReplacedEnabledThemesWillUseThe"),
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
      void message.success(t("settings.uploadedThemeDeleted"));
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
