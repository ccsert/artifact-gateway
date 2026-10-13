import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getSiteSettings, type SiteSettings } from "../client";
import { defaultConsoleThemes } from "./consoleTheme";
import { unwrap } from "./query/client";
import { siteKeys } from "./query/keys";

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

/**
 * The site's name, brand and themes. The provider and the Site settings page
 * share this one read; a save writes the answer here and every page follows.
 * A refusal never ends a session: anonymous pages read it too.
 */
export function useSiteSettingsQuery({ fresh = false } = {}) {
  return useQuery({
    queryKey: siteKeys.settings(),
    queryFn: async ({ signal }) => {
      const settings = await unwrap(getSiteSettings({ signal }));
      if (!settings) throw new Error("site settings are unavailable");
      return settings;
    },
    meta: { authFailure: "ignore", sharedAcrossIdentities: true },
    // The provider reads once; the settings page asks again on every visit.
    staleTime: fresh ? 0 : Infinity,
  });
}

export function SiteSettingsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const query = useSiteSettingsQuery();
  // The last answer stays in use while a cache reset (a sign-in or sign-out)
  // reads it again, and the defaults keep the Console usable if it fails.
  const [last, setLast] = useState(defaultSiteSettings);
  if (query.data && query.data !== last) setLast(query.data);
  const settings = query.data ?? last;
  const [ready, setReady] = useState(false);
  if (!ready && !query.isPending) setReady(true);

  const applySettings = useCallback(
    (next: SiteSettings) => queryClient.setQueryData(siteKeys.settings(), next),
    [queryClient],
  );
  const refetch = query.refetch;
  const reload = useCallback(
    async () => (await refetch()).data ?? null,
    [refetch],
  );

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
