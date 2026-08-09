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

type Mode = "oidc" | "password" | "token";

interface OIDCConfig {
  enabled: boolean;
  issuer?: string;
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

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    setError("");
    try {
      const res = await fetch("/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: username.trim(), password }),
      });
      if (!res.ok) {
        setError(
          res.status === 401
            ? t("auth.invalidCredentials")
            : t("auth.loginFailed", { status: res.status }),
        );
        return;
      }
      const body = (await res.json()) as { token?: string; role?: string };
      if (!body.token) {
        setError(t("auth.missingToken"));
        return;
      }
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
              <div className="mt-0.5 font-mono text-[10px] uppercase tracking-wider text-cyan-200">
                Native Hosted API v2
              </div>
            </div>
          </div>

          <div className="ag-login-brand-copy">
            <div className="font-mono text-[11px] uppercase tracking-wider text-cyan-300">
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
              <div className="mt-1 font-mono text-[10px] text-zinc-500">
                OIDC · Local session · Bearer token
              </div>
            </div>
          </div>
        </aside>

        <section className="ag-login-panel" aria-labelledby="login-heading">
          <header>
            <div className="font-mono text-[11px] uppercase tracking-wider text-cyan-400">
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

          <div className="ag-login-mode-body">
            {mode === "oidc" && oidc.enabled ? (
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
