import { useEffect, useState, type FormEvent } from "react";
import {
  ArrowRightOutlined,
  KeyOutlined,
  LoginOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Alert, Button, Input, Segmented } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { client } from "../client/client.gen";
import { getCurrentIdentity } from "../client";
import { Field } from "../components/Layout";
import { PreferenceControls } from "../components/PreferenceControls";
import { usePreferences } from "../lib/preferences";
import {
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./users/passwordPolicy";

type Mode = "oidc" | "password" | "token";

interface OIDCConfig {
  enabled: boolean;
  issuer?: string;
}

interface LocalLoginResponse {
  token?: string;
  role?: string;
  mustChangePassword?: boolean;
}

interface PendingPasswordChange {
  token: string;
  currentPassword: string;
}

// Standalone login route. The primary path is username/password against
// POST /auth/login; a token mode remains for static/OIDC/API-key bearers.
// Tokens are verified before persistence so an invalid one never reaches the
// app shell.
export function LoginPage() {
  const { authenticated, identityLoading, setToken } = useAuth();
  const { t } = usePreferences();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const redirect = params.get("redirect") || "/";

  const [mode, setMode] = useState<Mode>("password");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setTokenDraft] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pendingPasswordChange, setPendingPasswordChange] =
    useState<PendingPasswordChange | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [oidc, setOIDC] = useState<OIDCConfig>({ enabled: false });

  useEffect(() => {
    let cancelled = false;
    void fetch("/auth/oidc/config", { credentials: "include" })
      .then(async (response) => {
        if (!response.ok) return;
        const config = (await response.json()) as OIDCConfig;
        if (cancelled) return;
        setOIDC(config);
        if (config.enabled) setMode("oidc");
      })
      .catch(() => {
        // Local and token login remain available when discovery is offline.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const oidcError = params.get("oidc_error");
    if (oidcError) setError(t("auth.ssoFailed"));
    if (params.get("oidc") !== "success" || identityLoading) return;
    if (authenticated) navigate(redirect, { replace: true });
    else setError(t("auth.ssoFailed"));
  }, [authenticated, identityLoading, navigate, params, redirect, t]);

  const finish = (next: string, role?: string) => {
    setToken(next, role);
    navigate(redirect, { replace: true });
  };

  const loginWithPassword = async (
    currentUsername: string,
    currentPassword: string,
  ): Promise<LocalLoginResponse | null> => {
    const response = await fetch("/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        username: currentUsername.trim(),
        password: currentPassword,
      }),
    });
    if (!response.ok) {
      setError(
        response.status === 401
          ? t("auth.invalidCredentials")
          : t("auth.loginFailed", { status: response.status }),
      );
      return null;
    }
    const body = (await response.json()) as LocalLoginResponse;
    if (!body.token) {
      setError(t("auth.missingToken"));
      return null;
    }
    return body;
  };

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    setError("");
    try {
      const body = await loginWithPassword(username, password);
      if (!body?.token) return;
      if (body.mustChangePassword) {
        setPendingPasswordChange({
          token: body.token,
          currentPassword: password,
        });
        setNewPassword("");
        setConfirmPassword("");
        return;
      }
      finish(body.token, body.role);
    } catch {
      setError(t("auth.networkError"));
    } finally {
      setBusy(false);
    }
  };

  const submitPasswordChange = async (e: FormEvent) => {
    e.preventDefault();
    if (!pendingPasswordChange) return;
    if (!localPasswordMeetsMinimum(newPassword)) {
      setError(t("auth.passwordTooShort"));
      return;
    }
    if (!localPasswordFitsBcrypt(newPassword)) {
      setError(t("auth.passwordTooLong"));
      return;
    }
    if (newPassword !== confirmPassword) {
      setError(t("auth.passwordMismatch"));
      return;
    }
    if (newPassword === pendingPasswordChange.currentPassword) {
      setError(t("auth.passwordReuse"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/auth/change-password", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${pendingPasswordChange.token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          currentPassword: pendingPasswordChange.currentPassword,
          newPassword,
        }),
      });
      if (!response.ok) {
        setError(t("auth.passwordChangeFailed", { status: response.status }));
        return;
      }
      const body = await loginWithPassword(username, newPassword);
      if (!body?.token) return;
      setPassword(newPassword);
      setPendingPasswordChange(null);
      finish(body.token, body.role);
    } catch {
      setError(t("auth.networkError"));
    } finally {
      setBusy(false);
    }
  };

  const submitToken = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = token.trim();
    if (!trimmed) return;
    setBusy(true);
    setError("");
    client.setConfig({ baseUrl: "/api/v2", auth: () => trimmed });
    const { data: identity, error: err } = await getCurrentIdentity();
    setBusy(false);
    if (err || !identity) {
      setError(t("auth.invalidToken"));
      return;
    }
    finish(trimmed, identity.role);
  };

  return (
    <div className="ag-login-shell">
      <div className="ag-login-toolbar">
        <PreferenceControls />
      </div>

      <main className="ag-login-frame">
        <aside className="ag-login-brand-panel">
          <div className="ag-login-brand-lockup">
            <div className="ag-brand-mark flex h-11 w-11 items-center justify-center rounded-lg text-base font-bold text-white">
              AG
            </div>
            <div>
              <div className="text-sm font-semibold text-white">Console</div>
              <div className="mt-0.5 font-mono text-xs uppercase tracking-wider text-cyan-200">
                Native Hosted API v2
              </div>
            </div>
          </div>

          <div className="ag-login-brand-copy">
            <div className="font-mono text-xs uppercase tracking-wider text-cyan-300">
              {t("auth.loginTitle")}
            </div>
            <h1 className="mt-4 text-4xl font-semibold text-white">
              Artifact Gateway
            </h1>
            <p className="mt-3 max-w-xs text-sm leading-6 text-zinc-400">
              {t("auth.controlPlane")}
            </p>
          </div>

          <div className="ag-login-security-note">
            <SafetyCertificateOutlined />
            <div>
              <div className="text-xs font-medium text-zinc-200">
                {t("auth.protectedAccess")}
              </div>
              <div className="mt-1 font-mono text-xs text-zinc-500">
                OIDC · Local session · Bearer token
              </div>
            </div>
          </div>
        </aside>

        <section className="ag-login-panel" aria-labelledby="login-heading">
          <header>
            <div className="font-mono text-xs uppercase tracking-wider text-cyan-400">
              {t("auth.loginTitle")}
            </div>
            <h2
              id="login-heading"
              className="mt-2 text-2xl font-semibold text-zinc-100"
            >
              {t("auth.welcome")}
            </h2>
            <p className="mt-2 text-sm text-zinc-500">{t("auth.signInHint")}</p>
          </header>

          {!pendingPasswordChange && (
            <Segmented<Mode>
              block
              size="large"
              className="ag-login-modes mt-7"
              value={mode}
              options={[
                ...(oidc.enabled
                  ? [
                      {
                        value: "oidc" as const,
                        label: t("auth.ssoMode"),
                        icon: <SafetyCertificateOutlined />,
                      },
                    ]
                  : []),
                {
                  value: "password",
                  label: t("auth.passwordMode"),
                  icon: <UserOutlined />,
                },
                {
                  value: "token",
                  label: t("auth.tokenMode"),
                  icon: <KeyOutlined />,
                },
              ]}
              onChange={(nextMode) => {
                setMode(nextMode);
                setError("");
              }}
            />
          )}

          <div className="ag-login-mode-body">
            {pendingPasswordChange ? (
              <form onSubmit={submitPasswordChange} className="ag-login-form">
                <Alert
                  type="info"
                  showIcon
                  title={t("auth.changePasswordTitle")}
                  description={t("auth.changePasswordHint")}
                />
                <Field label={t("auth.newPassword")}>
                  <Input.Password
                    size="large"
                    autoComplete="new-password"
                    maxLength={72}
                    value={newPassword}
                    onChange={(event) => setNewPassword(event.target.value)}
                  />
                </Field>
                <Field label={t("auth.confirmPassword")}>
                  <Input.Password
                    size="large"
                    autoComplete="new-password"
                    maxLength={72}
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                  />
                </Field>
                {error && <Alert type="error" showIcon title={error} />}
                <Button
                  type="primary"
                  size="large"
                  htmlType="submit"
                  block
                  icon={<KeyOutlined />}
                  loading={busy}
                  disabled={!newPassword || !confirmPassword}
                >
                  {t("auth.changePassword")}
                </Button>
              </form>
            ) : mode === "oidc" && oidc.enabled ? (
              <div className="ag-login-sso text-center">
                <div className="ag-login-mode-icon">
                  <SafetyCertificateOutlined />
                </div>
                <div className="mt-4 text-sm font-medium text-zinc-100">
                  {t("auth.ssoConfigured")}
                </div>
                {oidc.issuer && (
                  <div
                    className="mt-2 truncate font-mono text-xs text-zinc-500"
                    title={oidc.issuer}
                  >
                    {oidc.issuer}
                  </div>
                )}
                {error && (
                  <Alert
                    className="mt-5 text-left"
                    type="error"
                    showIcon
                    title={error}
                  />
                )}
                <Button
                  className="mt-6"
                  type="primary"
                  size="large"
                  block
                  icon={<LoginOutlined />}
                  onClick={() => {
                    window.location.assign(
                      `/auth/oidc/login?redirect=${encodeURIComponent(redirect)}`,
                    );
                  }}
                >
                  {t("auth.ssoLogin")}
                </Button>
              </div>
            ) : mode === "password" ? (
              <form onSubmit={submitPassword} className="ag-login-form">
                <Field label={t("auth.username")}>
                  <Input
                    size="large"
                    prefix={<UserOutlined />}
                    placeholder="alice"
                    autoComplete="username"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                  />
                </Field>
                <Field label={t("auth.password")}>
                  <Input.Password
                    size="large"
                    placeholder="••••••••"
                    autoComplete="current-password"
                    maxLength={72}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </Field>
                {error && <Alert type="error" showIcon title={error} />}
                <Button
                  type="primary"
                  size="large"
                  htmlType="submit"
                  block
                  icon={<LoginOutlined />}
                  loading={busy}
                  disabled={!username.trim() || !password}
                >
                  {t("auth.login")}
                </Button>
              </form>
            ) : (
              <form onSubmit={submitToken} className="ag-login-form">
                <Field label={t("auth.token")} hint={t("auth.tokenHint")}>
                  <Input.TextArea
                    className="font-mono text-xs"
                    autoSize={{ minRows: 5, maxRows: 8 }}
                    placeholder={t("auth.tokenPlaceholder")}
                    value={token}
                    onChange={(e) => setTokenDraft(e.target.value)}
                  />
                </Field>
                {error && <Alert type="error" showIcon title={error} />}
                <Button
                  type="primary"
                  size="large"
                  htmlType="submit"
                  block
                  icon={<LoginOutlined />}
                  loading={busy}
                  disabled={!token.trim()}
                >
                  {t("auth.verify")}
                </Button>
              </form>
            )}
          </div>

          <div className="ag-login-public">
            <Link to="/browse">
              {t("auth.publicBrowse")}
              <ArrowRightOutlined />
            </Link>
          </div>
        </section>
      </main>
    </div>
  );
}
