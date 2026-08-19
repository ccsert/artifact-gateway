import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { getSiteSettings, type SiteSettings } from "../client";
import { defaultConsoleThemes } from "./consoleTheme";

export const defaultSiteSettings: SiteSettings = {
  version: "1",
  siteName: "Artifact Gateway",
  logoUrl: "",
  brandMark: "AG",
  enabledThemeIds: [
    "gateway-dark",
    "gateway-light",
    "aerok-dark",
    "aerok-light",
  ],
  defaultThemeId: "gateway-dark",
  availableThemes: defaultConsoleThemes,
  updatedAt: "1970-01-01T00:00:00Z",
};

interface SiteSettingsContextValue {
  settings: SiteSettings;
  applySettings: (settings: SiteSettings) => void;
  reload: () => Promise<SiteSettings | null>;
}

const defaultContext: SiteSettingsContextValue = {
  settings: defaultSiteSettings,
  applySettings: () => undefined,
  reload: async () => null,
};

const SiteSettingsContext = createContext<SiteSettingsContextValue | null>(
  null,
);

export function SiteSettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState(defaultSiteSettings);
  const [ready, setReady] = useState(false);

  const applySettings = useCallback((next: SiteSettings) => {
    setSettings(next);
  }, []);

  const reload = useCallback(async () => {
    try {
      const { data } = await getSiteSettings();
      if (data) {
        setSettings(data);
        return data;
      }
    } catch {
      // The default identity keeps the Console usable during a transient read failure.
    }
    return null;
  }, []);

  useEffect(() => {
    let active = true;
    void reload().finally(() => {
      if (active) setReady(true);
    });
    return () => {
      active = false;
    };
  }, [reload]);

  useEffect(() => {
    document.title = `${settings.siteName} Console`;
    const icon = document.querySelector<HTMLLinkElement>('link[rel~="icon"]');
    if (!icon) return;
    const defaultHref = icon.dataset.defaultHref ?? icon.href;
    icon.dataset.defaultHref = defaultHref;
    icon.href = settings.logoUrl || defaultHref;
  }, [settings.logoUrl, settings.siteName]);

  const value = useMemo(
    () => ({ settings, applySettings, reload }),
    [settings, applySettings, reload],
  );

  return (
    <SiteSettingsContext.Provider value={value}>
      {ready ? (
        children
      ) : (
        <div className="ag-app-fallback flex min-h-screen items-center justify-center">
          <span
            className="ag-brand-bootstrap"
            aria-label="Loading site settings"
          />
        </div>
      )}
    </SiteSettingsContext.Provider>
  );
}

export function useSiteSettings(): SiteSettingsContextValue {
  return useContext(SiteSettingsContext) ?? defaultContext;
}
