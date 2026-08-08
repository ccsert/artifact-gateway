import { useState, type FormEvent } from "react";
import { LoginOutlined } from "@ant-design/icons";
import { Alert, Button, Input, Segmented } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { client } from "../client/client.gen";
import { getCurrentIdentity } from "../client";
import { Field } from "../components/Layout";

type Mode = "password" | "token";

// Standalone login route. The primary path is username/password against
// POST /auth/login; a token mode remains for static/OIDC/API-key bearers.
// Tokens are verified before persistence so an invalid one never reaches the
// app shell.
export function LoginPage() {
  const { setToken } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const redirect = params.get("redirect") || "/";

  const [mode, setMode] = useState<Mode>("password");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setTokenDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

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
            ? "用户名或密码错误。"
            : `登录失败 (${res.status})。`,
        );
        return;
      }
      const body = (await res.json()) as { token?: string; role?: string };
      if (!body.token) {
        setError("登录响应缺少令牌。");
        return;
      }
      finish(body.token, body.role);
    } catch {
      setError("网络错误，请重试。");
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
      setError("令牌无效或已过期，请检查后重试。");
      return;
    }
    finish(trimmed, identity.role);
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <div className="w-full max-w-md">
        <div className="mb-6 flex flex-col items-center gap-3">
          <div className="flex h-12 w-12 items-center justify-center rounded-lg bg-cyan-600 text-lg font-bold text-white">
            AG
          </div>
          <div className="text-center">
            <div className="text-lg font-semibold text-zinc-100">
              Artifact Gateway
            </div>
            <div className="text-xs uppercase tracking-widest text-zinc-600">
              Console 登录
            </div>
          </div>
        </div>

        <Segmented<Mode>
          block
          className="mb-4"
          value={mode}
          options={[
            { value: "password", label: "账号密码" },
            { value: "token", label: "访问令牌" },
          ]}
          onChange={(nextMode) => {
            setMode(nextMode);
            setError("");
          }}
        />

        {mode === "password" ? (
          <form
            onSubmit={submitPassword}
            className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/60 p-6"
          >
            <Field label="用户名">
              <Input
                placeholder="alice"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
            <Field label="密码">
              <Input.Password
                placeholder="••••••••"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
            {error && <Alert type="error" showIcon title={error} />}
            <Button
              type="primary"
              htmlType="submit"
              block
              icon={<LoginOutlined />}
              loading={busy}
              disabled={!username.trim() || !password}
            >
              登录
            </Button>
          </form>
        ) : (
          <form
            onSubmit={submitToken}
            className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/60 p-6"
          >
            <Field
              label="访问令牌"
              hint="静态令牌、OIDC 或 API 密钥的 Bearer；仅保存在本浏览器 localStorage。"
            >
              <Input.TextArea
                className="font-mono text-xs"
                autoSize={{ minRows: 4, maxRows: 8 }}
                placeholder="粘贴 Bearer Token…"
                value={token}
                onChange={(e) => setTokenDraft(e.target.value)}
              />
            </Field>
            {error && <Alert type="error" showIcon title={error} />}
            <Button
              type="primary"
              htmlType="submit"
              block
              icon={<LoginOutlined />}
              loading={busy}
              disabled={!token.trim()}
            >
              验证并登录
            </Button>
          </form>
        )}
        <div className="mt-5 text-center text-xs text-zinc-600">
          <Link to="/browse" className="text-zinc-400 hover:text-cyan-300">
            浏览公开制品
          </Link>
        </div>
      </div>
    </div>
  );
}
