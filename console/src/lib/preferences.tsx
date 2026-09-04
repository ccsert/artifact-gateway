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
export type AppLocale = "zh-CN" | "en-US";

interface ThemeViewTransition {
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

const zhCN = {
  "common.cancel": "取消",
  "common.save": "保存",
  "common.loading": "正在加载…",
  "common.theme.light": "切换到亮色模式",
  "common.theme.dark": "切换到暗色模式",
  "common.theme.select": "选择主题",
  "common.theme.current": "当前主题：{name}",
  "common.language": "语言",
  "common.language.zh": "中文",
  "common.language.en": "English",
  "nav.runtime": "运行",
  "nav.governance": "治理",
  "nav.management": "管理",
  "nav.dashboard": "总览",
  "nav.repositories": "仓库",
  "nav.search": "制品搜索",
  "nav.operations": "任务中心",
  "nav.groups": "分组",
  "nav.access": "访问控制",
  "nav.audits": "审计日志",
  "nav.auditRetention": "审计保留",
  "nav.apiKeys": "API 密钥",
  "nav.serviceAccounts": "服务账号",
  "nav.users": "用户",
  "nav.authentication": "身份认证",
  "nav.siteSettings": "站点设置",
  "nav.expand": "展开导航",
  "nav.collapse": "收起导航",
  "nav.open": "打开导航",
  "nav.close": "关闭导航",
  "header.search": "跨仓库搜索制品…",
  "auth.loginTitle": "Console 登录",
  "auth.welcome": "登录控制台",
  "auth.signInHint": "选择身份验证方式以继续。",
  "auth.controlPlane": "制品基础设施控制台",
  "auth.protectedAccess": "受保护的管理入口",
  "auth.passwordMode": "账号密码",
  "auth.tokenMode": "访问令牌",
  "auth.ssoMode": "企业登录",
  "auth.username": "用户名",
  "auth.password": "密码",
  "auth.changePasswordTitle": "更新初始密码",
  "auth.changePasswordHint": "管理员要求你在继续前设置一个新的个人密码。",
  "auth.newPassword": "新密码",
  "auth.confirmPassword": "确认新密码",
  "auth.passwordTooShort": "新密码至少需要 8 个字符。",
  "auth.passwordTooLong": "新密码不能超过 72 字节。",
  "auth.passwordMismatch": "两次输入的新密码不一致。",
  "auth.passwordReuse": "新密码不能与当前密码相同。",
  "auth.changePassword": "更新密码并继续",
  "auth.passwordChangeFailed": "密码更新失败（{status}）。",
  "auth.login": "登录",
  "auth.ssoLogin": "使用 SSO 登录",
  "auth.ssoConfigured": "由组织身份提供方统一认证",
  "auth.token": "访问令牌",
  "auth.tokenHint": "静态令牌、OIDC 或 API 密钥的 Bearer；仅保存在本浏览器。",
  "auth.tokenPlaceholder": "粘贴 Bearer Token…",
  "auth.verify": "验证并登录",
  "auth.publicBrowse": "浏览公开制品",
  "auth.invalidCredentials": "用户名或密码错误。",
  "auth.loginFailed": "登录失败（{status}）。",
  "auth.missingToken": "登录响应缺少令牌。",
  "auth.networkError": "网络错误，请重试。",
  "auth.invalidToken": "令牌无效或已过期，请检查后重试。",
  "auth.ssoFailed": "企业登录未完成，请重试。",
  "auth.ssoUnavailable": "身份提供方暂时不可用。",
  "auth.logout": "退出",
  "auth.tokenConfigured": "已配置 Token",
  "auth.setToken": "设置 Token",
  "auth.tokenDialog": "API 访问令牌",
  "auth.clearToken": "清除令牌",
  "auth.tokenDialogHint": "管理 API 使用 Bearer 认证，令牌仅保存在浏览器中。",
  "auth.saveToken": "保存",
  "public.browseTitle": "公开制品浏览",
  "public.catalogTitle": "公开制品",
  "public.description":
    "仅显示已启用匿名读取的仓库内容；写入与管理操作仍需登录。",
  "public.managementLogin": "管理登录",
  "public.managementConsole": "进入管理端",
} as const;

type MessageKey = keyof typeof zhCN;

const enUS: Record<MessageKey, string> = {
  "common.cancel": "Cancel",
  "common.save": "Save",
  "common.loading": "Loading…",
  "common.theme.light": "Switch to light mode",
  "common.theme.dark": "Switch to dark mode",
  "common.theme.select": "Choose theme",
  "common.theme.current": "Current theme: {name}",
  "common.language": "Language",
  "common.language.zh": "中文",
  "common.language.en": "English",
  "nav.runtime": "Runtime",
  "nav.governance": "Governance",
  "nav.management": "Management",
  "nav.dashboard": "Overview",
  "nav.repositories": "Repositories",
  "nav.search": "Artifact Search",
  "nav.operations": "Operations",
  "nav.groups": "Groups",
  "nav.access": "Access Control",
  "nav.audits": "Audit Log",
  "nav.auditRetention": "Audit Retention",
  "nav.apiKeys": "API Keys",
  "nav.serviceAccounts": "Service Accounts",
  "nav.users": "Users",
  "nav.authentication": "Authentication",
  "nav.siteSettings": "Site Settings",
  "nav.expand": "Expand navigation",
  "nav.collapse": "Collapse navigation",
  "nav.open": "Open navigation",
  "nav.close": "Close navigation",
  "header.search": "Search artifacts across repositories…",
  "auth.loginTitle": "Console Sign In",
  "auth.welcome": "Sign in to the console",
  "auth.signInHint": "Choose an authentication method to continue.",
  "auth.controlPlane": "Artifact infrastructure console",
  "auth.protectedAccess": "Protected management access",
  "auth.passwordMode": "Username & Password",
  "auth.tokenMode": "Access Token",
  "auth.ssoMode": "Enterprise SSO",
  "auth.username": "Username",
  "auth.password": "Password",
  "auth.changePasswordTitle": "Change initial password",
  "auth.changePasswordHint":
    "An administrator requires you to set a personal password before continuing.",
  "auth.newPassword": "New password",
  "auth.confirmPassword": "Confirm new password",
  "auth.passwordTooShort":
    "The new password must contain at least 8 characters.",
  "auth.passwordTooLong": "The new password must not exceed 72 bytes.",
  "auth.passwordMismatch": "The new passwords do not match.",
  "auth.passwordReuse":
    "The new password must differ from the current password.",
  "auth.changePassword": "Change password and continue",
  "auth.passwordChangeFailed": "Password change failed ({status}).",
  "auth.login": "Sign in",
  "auth.ssoLogin": "Continue with SSO",
  "auth.ssoConfigured":
    "Authenticate with your organization's identity provider",
  "auth.token": "Access token",
  "auth.tokenHint":
    "Bearer token from a static credential, OIDC, or API key. Stored only in this browser.",
  "auth.tokenPlaceholder": "Paste Bearer token…",
  "auth.verify": "Verify and sign in",
  "auth.publicBrowse": "Browse public artifacts",
  "auth.invalidCredentials": "Incorrect username or password.",
  "auth.loginFailed": "Sign in failed ({status}).",
  "auth.missingToken": "The login response did not include a token.",
  "auth.networkError": "Network error. Try again.",
  "auth.invalidToken": "The token is invalid or expired.",
  "auth.ssoFailed": "Enterprise sign in did not complete. Try again.",
  "auth.ssoUnavailable": "The identity provider is temporarily unavailable.",
  "auth.logout": "Sign out",
  "auth.tokenConfigured": "Token configured",
  "auth.setToken": "Set token",
  "auth.tokenDialog": "API access token",
  "auth.clearToken": "Clear token",
  "auth.tokenDialogHint":
    "Management APIs use Bearer authentication. The token is stored only in this browser.",
  "auth.saveToken": "Save",
  "public.browseTitle": "Public Artifact Browser",
  "public.catalogTitle": "Public artifacts",
  "public.description":
    "Only repositories with anonymous reads enabled are shown. Writes and management still require sign-in.",
  "public.managementLogin": "Management sign in",
  "public.managementConsole": "Open management console",
};

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
  t: (key: MessageKey, variables?: Record<string, string | number>) => string;
  text: (chinese: string, english: string) => string;
}

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
    try {
      localStorage.setItem(THEME_ID_KEY, activeTheme.id);
      localStorage.setItem(THEME_MODE_KEY, colorMode);
    } catch {
      // Preferences still work for this session when storage is unavailable.
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
      if (
        nextThemeID === themeId ||
        !availableThemes.some((theme) => theme.id === nextThemeID)
      )
        return;
      const root = document.documentElement;
      const reduceMotion = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      const transitionDocument = document as ThemeTransitionDocument;
      const commit = () => flushSync(() => setThemeIdState(nextThemeID));
      const commitSequence = ++themeCommitSequence.current;

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
      const transition = transitionDocument.startViewTransition(async () => {
        commit();
        // Let Ant Design v6 publish its CSS-variable theme before the browser
        // captures the new snapshot. A task boundary works while rendering is
        // paused; requestAnimationFrame would deadlock the View Transition.
        await new Promise<void>((resolve) => window.setTimeout(resolve, 0));
      });
      activeThemeTransition.current = transition;
      void transition.finished.finally(() => {
        if (activeThemeTransition.current === transition) {
          activeThemeTransition.current = null;
          delete root.dataset.themeTransition;
          clearThemeRevealGeometry(root);
        }
      });
    },
    [availableThemes, themeId],
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
  const t = useCallback(
    (key: MessageKey, variables?: Record<string, string | number>) => {
      let message: string = (locale === "zh-CN" ? zhCN : enUS)[key];
      for (const [name, value] of Object.entries(variables ?? {})) {
        message = message.replaceAll(`{${name}}`, String(value));
      }
      return message;
    },
    [locale],
  );
  const text = useCallback(
    (chinese: string, english: string) =>
      locale === "zh-CN" ? chinese : english,
    [locale],
  );

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
      text,
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
      text,
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
