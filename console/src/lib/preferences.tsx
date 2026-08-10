import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export type ColorMode = "dark" | "light";
export type AppLocale = "zh-CN" | "en-US";

const THEME_KEY = "ag.console.theme";
const LOCALE_KEY = "ag.console.locale";

const zhCN = {
  "common.cancel": "取消",
  "common.save": "保存",
  "common.loading": "正在加载…",
  "common.theme.light": "切换到亮色模式",
  "common.theme.dark": "切换到暗色模式",
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
  "nav.users": "用户",
  "nav.authentication": "身份认证",
  "nav.expand": "展开导航",
  "nav.collapse": "收起导航",
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
} as const;

type MessageKey = keyof typeof zhCN;

const enUS: Record<MessageKey, string> = {
  "common.cancel": "Cancel",
  "common.save": "Save",
  "common.loading": "Loading…",
  "common.theme.light": "Switch to light mode",
  "common.theme.dark": "Switch to dark mode",
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
  "nav.users": "Users",
  "nav.authentication": "Authentication",
  "nav.expand": "Expand navigation",
  "nav.collapse": "Collapse navigation",
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
};

interface PreferencesContextValue {
  colorMode: ColorMode;
  locale: AppLocale;
  setColorMode: (mode: ColorMode) => void;
  setLocale: (locale: AppLocale) => void;
  toggleColorMode: () => void;
  t: (key: MessageKey, variables?: Record<string, string | number>) => string;
  text: (chinese: string, english: string) => string;
}

const PreferencesContext = createContext<PreferencesContextValue | null>(null);

function storedColorMode(): ColorMode {
  try {
    return localStorage.getItem(THEME_KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
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
  const [colorMode, setColorModeState] = useState<ColorMode>(storedColorMode);
  const [locale, setLocaleState] = useState<AppLocale>(storedLocale);

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = colorMode;
    root.classList.toggle("dark", colorMode === "dark");
    root.style.colorScheme = colorMode;
    try {
      localStorage.setItem(THEME_KEY, colorMode);
    } catch {
      // Preferences still work for this session when storage is unavailable.
    }
  }, [colorMode]);

  useEffect(() => {
    document.documentElement.lang = locale;
    try {
      localStorage.setItem(LOCALE_KEY, locale);
    } catch {
      // Preferences still work for this session when storage is unavailable.
    }
  }, [locale]);

  const setColorMode = useCallback(
    (mode: ColorMode) => setColorModeState(mode),
    [],
  );
  const setLocale = useCallback(
    (nextLocale: AppLocale) => setLocaleState(nextLocale),
    [],
  );
  const toggleColorMode = useCallback(
    () =>
      setColorModeState((current) => (current === "dark" ? "light" : "dark")),
    [],
  );
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
      colorMode,
      locale,
      setColorMode,
      setLocale,
      toggleColorMode,
      t,
      text,
    }),
    [colorMode, locale, setColorMode, setLocale, toggleColorMode, t, text],
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
