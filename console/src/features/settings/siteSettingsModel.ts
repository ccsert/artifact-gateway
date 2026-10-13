import type { ConsoleThemePackageValidation, SiteSettings } from "../../client";

export const maxLogoBytes = 192 * 1024;
export const maxThemePackageBytes = 256 * 1024;
export const maxEnabledThemes = 32;
export const supportedLogoTypes = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
]);

export interface SiteSettingsDraft {
  siteName: string;
  logoUrl: string;
  brandMark: string;
  enabledThemeIds: string[];
  defaultThemeId: string;
}

export interface PendingThemePackage {
  fileName: string;
  validation: ConsoleThemePackageValidation;
}

export function toDraft(settings: SiteSettings): SiteSettingsDraft {
  return {
    siteName: settings.siteName,
    logoUrl: settings.logoUrl,
    brandMark: settings.brandMark,
    enabledThemeIds: [...settings.enabledThemeIds],
    defaultThemeId: settings.defaultThemeId,
  };
}

export function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.readAsDataURL(file);
  });
}

export function readAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.readAsText(file);
  });
}
