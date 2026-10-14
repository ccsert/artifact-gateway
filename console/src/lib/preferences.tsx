import type { MessageKey } from "../messages";
import {
  createTranslator,
  type MessageLocale,
  type MessageVariables,
} from "../messages/translate";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { flushSync } from "react-dom";
import type { ConsoleTheme } from "../client";
import {
  applyResolvedConsoleTheme,
  defaultConsoleThemes,
  resolveConsoleTheme,
  type ResolvedConsoleTheme,
} from "./consoleTheme";
import { useSiteSettings } from "./siteSettings";

export type ColorMode = "dark" | "light";
export type AppLocale = MessageLocale;

interface ThemeViewTransition {
  ready?: Promise<void>;
  updateCallbackDone?: Promise<void>;
  finished: Promise<void>;
  skipTransition: () => void;
}

interface ThemeTransitionOrigin {
  x: number;
  y: number;
}

type ThemeTransitionDocument = Document & {
  startViewTransition?: (
    update: () => void | Promise<void>,
  ) => ThemeViewTransition;
};

const THEME_MODE_KEY = "ag.console.theme";
const THEME_ID_KEY = "ag.console.theme.id";
const LOCALE_KEY = "ag.console.locale";

interface PreferencesContextValue {
  themeId: string;
  activeTheme: ConsoleTheme;
  resolvedTheme: ResolvedConsoleTheme;
  availableThemes: ConsoleTheme[];
  colorMode: ColorMode;
  locale: AppLocale;
  setThemeId: (id: string, origin?: ThemeTransitionOrigin) => void;
  setColorMode: (mode: ColorMode) => void;
  setLocale: (locale: AppLocale) => void;
  toggleColorMode: () => void;
  t: (key: MessageKey, variables?: MessageVariables) => string;
}

/** The Console's translator: a message key plus its `{placeholder}` values. */
export type Translate = PreferencesContextValue["t"];

const PreferencesContext = createContext<PreferencesContextValue | null>(null);

function setThemeRevealGeometry(
  root: HTMLElement,
  origin?: ThemeTransitionOrigin,
) {
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;
  const candidateX =
    origin?.x !== undefined && Number.isFinite(origin.x)
      ? origin.x
      : viewportWidth;
  const candidateY =
    origin?.y !== undefined && Number.isFinite(origin.y) ? origin.y : 0;
  const x = Math.min(Math.max(candidateX, 0), viewportWidth);
  const y = Math.min(Math.max(candidateY, 0), viewportHeight);
  const radius = Math.ceil(
    Math.hypot(Math.max(x, viewportWidth - x), Math.max(y, viewportHeight - y)),
  );

  root.style.setProperty("--ag-theme-reveal-x", `${x}px`);
  root.style.setProperty("--ag-theme-reveal-y", `${y}px`);
  root.style.setProperty("--ag-theme-reveal-radius", `${radius}px`);
}

function clearThemeRevealGeometry(root: HTMLElement) {
  root.style.removeProperty("--ag-theme-reveal-x");
  root.style.removeProperty("--ag-theme-reveal-y");
  root.style.removeProperty("--ag-theme-reveal-radius");
}

function storedThemeId(themes: ConsoleTheme[], defaultThemeId: string): string {
  try {
    const storedID = localStorage.getItem(THEME_ID_KEY);
    if (storedID && themes.some((theme) => theme.id === storedID)) {
      return storedID;
    }
    const legacyMode = localStorage.getItem(THEME_MODE_KEY);
    if (legacyMode === "dark" || legacyMode === "light") {
      const legacyTheme =
        themes.find((theme) => theme.id === `gateway-${legacyMode}`) ??
        themes.find((theme) => theme.mode === legacyMode);
      if (legacyTheme) return legacyTheme.id;
    }
    return (
      themes.find((theme) => theme.id === defaultThemeId)?.id ??
      themes[0]?.id ??
      defaultConsoleThemes[0].id
    );
  } catch {
    return defaultThemeId;
  }
}

function storedLocale(): AppLocale {
  try {
    return localStorage.getItem(LOCALE_KEY) === "en-US" ? "en-US" : "zh-CN";
  } catch {
    return "zh-CN";
  }
}

function persistThemePreference(theme: Pick<ConsoleTheme, "id" | "mode">) {
  try {
    localStorage.setItem(THEME_ID_KEY, theme.id);
    localStorage.setItem(THEME_MODE_KEY, theme.mode);
  } catch {
    // Preferences still work for this session when storage is unavailable.
  }
}

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const { settings } = useSiteSettings();
  const availableThemes = useMemo(() => {
    // Site-settings payloads from older builds may omit the theme lists; a
    // missing list must degrade to the built-in themes, not blank the app.
    const enabled = new Set(settings.enabledThemeIds ?? []);
    const themes = (settings.availableThemes ?? []).filter((theme) =>
      enabled.has(theme.id),
    );
    return themes.length > 0 ? themes : defaultConsoleThemes;
  }, [settings.availableThemes, settings.enabledThemeIds]);
  const [themeId, setThemeIdState] = useState(() =>
    storedThemeId(availableThemes, settings.defaultThemeId),
  );
  const [locale, setLocaleState] = useState<AppLocale>(storedLocale);
  const activeThemeTransition = useRef<ThemeViewTransition | null>(null);
  const themeCommitSequence = useRef(0);
  const requestedThemeId = useRef(themeId);
  // Keep a just-disabled theme mounted for one render so the effect below can
  // animate to the configured default instead of swapping the whole UI first.
  const activeTheme =
    (settings.availableThemes ?? []).find((theme) => theme.id === themeId) ??
    availableThemes.find((theme) => theme.id === settings.defaultThemeId) ??
    availableThemes[0] ??
    defaultConsoleThemes[0];
  const colorMode: ColorMode = activeTheme.mode;
  const resolvedTheme = useMemo(
    () => resolveConsoleTheme(activeTheme),
    [activeTheme],
  );

  useLayoutEffect(() => {
    const root = document.documentElement;
    root.dataset.themeId = activeTheme.id;
    root.dataset.theme = colorMode;
    root.classList.toggle("dark", colorMode === "dark");
    root.style.colorScheme = colorMode;
    applyResolvedConsoleTheme(resolvedTheme, root);
    if (requestedThemeId.current === activeTheme.id) {
      persistThemePreference({ id: activeTheme.id, mode: colorMode });
    }
  }, [activeTheme.id, colorMode, resolvedTheme]);

  useEffect(() => {
    document.documentElement.lang = locale;
    try {
      localStorage.setItem(LOCALE_KEY, locale);
    } catch {
      // Preferences still work for this session when storage is unavailable.
    }
  }, [locale]);

  const setThemeId = useCallback(
    (nextThemeID: string, origin?: ThemeTransitionOrigin) => {
      const nextTheme = availableThemes.find(
        (theme) => theme.id === nextThemeID,
      );
      if (nextThemeID === requestedThemeId.current || !nextTheme) return;
      const root = document.documentElement;
      const reduceMotion = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      const transitionDocument = document as ThemeTransitionDocument;
      const commitSequence = ++themeCommitSequence.current;
      requestedThemeId.current = nextThemeID;
      // Persist the accepted choice before an optional browser capture starts.
      // Document navigation can destroy its callback before the palette commit.
      persistThemePreference(nextTheme);
      const commit = () => {
        // Skipping a View Transition does not cancel its asynchronous update
        // callback. An older snapshot must never replace a later user choice.
        if (themeCommitSequence.current === commitSequence) {
          flushSync(() => setThemeIdState(nextThemeID));
        }
      };

      activeThemeTransition.current?.skipTransition();
      if (reduceMotion || !transitionDocument.startViewTransition) {
        activeThemeTransition.current = null;
        clearThemeRevealGeometry(root);
        root.dataset.themeTransition = "instant";
        commit();
        // Flush the new palette while transitions are disabled. Removing the
        // marker on the next frame cannot retroactively start per-component
        // interpolation, so unsupported/reduced-motion browsers switch atomically.
        void root.offsetWidth;
        window.requestAnimationFrame(() => {
          if (
            themeCommitSequence.current === commitSequence &&
            root.dataset.themeTransition === "instant"
          ) {
            delete root.dataset.themeTransition;
          }
        });
        return;
      }

      setThemeRevealGeometry(root, origin);
      root.dataset.themeTransition = "view";
      let transition: ThemeViewTransition;
      try {
        transition = transitionDocument.startViewTransition(async () => {
          commit();
          // Let Ant Design v6 publish its CSS-variable theme before the browser
          // captures the new snapshot. A task boundary works while rendering is
          // paused; requestAnimationFrame would deadlock the View Transition.
          await new Promise<void>((resolve) => window.setTimeout(resolve, 0));
        });
      } catch {
        // The reveal is optional; a browser capture failure cannot reject the
        // user's theme selection.
        activeThemeTransition.current = null;
        delete root.dataset.themeTransition;
        clearThemeRevealGeometry(root);
        commit();
        return;
      }
      // Native capture failures/skips can reject ready while finished fulfills.
      // Both promises report unhandled rejections even when never accessed.
      void transition.ready?.catch(() => undefined);
      void transition.updateCallbackDone?.catch(() => undefined);
      activeThemeTransition.current = transition;
      const finish = () => {
        if (activeThemeTransition.current === transition) {
          activeThemeTransition.current = null;
          delete root.dataset.themeTransition;
          clearThemeRevealGeometry(root);
        }
      };
      void transition.finished.then(finish, () => {
        commit();
        finish();
      });
    },
    [availableThemes],
  );
  useEffect(() => {
    if (!availableThemes.some((theme) => theme.id === themeId)) {
      const fallbackThemeID =
        availableThemes.find((theme) => theme.id === settings.defaultThemeId)
          ?.id ?? availableThemes[0].id;
      const timeout = window.setTimeout(() => setThemeId(fallbackThemeID), 0);
      return () => window.clearTimeout(timeout);
    }
    return undefined;
  }, [availableThemes, setThemeId, settings.defaultThemeId, themeId]);
  const setColorMode = useCallback(
    (mode: ColorMode) => {
      const next = availableThemes.find((theme) => theme.mode === mode);
      if (next) setThemeId(next.id);
    },
    [availableThemes, setThemeId],
  );
  const setLocale = useCallback(
    (nextLocale: AppLocale) => setLocaleState(nextLocale),
    [],
  );
  const toggleColorMode = useCallback(() => {
    const opposite = availableThemes.find((theme) => theme.mode !== colorMode);
    if (opposite) {
      setThemeId(opposite.id);
      return;
    }
    const currentIndex = availableThemes.findIndex(
      (theme) => theme.id === activeTheme.id,
    );
    const next = availableThemes[(currentIndex + 1) % availableThemes.length];
    if (next) setThemeId(next.id);
  }, [activeTheme.id, availableThemes, colorMode, setThemeId]);
  const t = useMemo(() => createTranslator(locale), [locale]);

  const value = useMemo(
    () => ({
      themeId: activeTheme.id,
      activeTheme,
      resolvedTheme,
      availableThemes,
      colorMode,
      locale,
      setThemeId,
      setColorMode,
      setLocale,
      toggleColorMode,
      t,
    }),
    [
      activeTheme,
      availableThemes,
      colorMode,
      locale,
      resolvedTheme,
      setThemeId,
      setColorMode,
      setLocale,
      toggleColorMode,
      t,
    ],
  );

  return (
    <PreferencesContext.Provider value={value}>
      {children}
    </PreferencesContext.Provider>
  );
}

export function usePreferences(): PreferencesContextValue {
  const value = useContext(PreferencesContext);
  if (!value)
    throw new Error("usePreferences must be used within PreferencesProvider");
  return value;
}
