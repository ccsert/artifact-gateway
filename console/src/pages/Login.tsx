import { useState, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { client } from '../client/client.gen';
import { listRepositories } from '../client';
import { Field, inputClass, btnPrimary } from '../components/Layout';

type Mode = 'password' | 'token';

// Standalone login route. The primary path is username/password against
// POST /auth/login; a token mode remains for static/OIDC/API-key bearers.
// Tokens are verified before persistence so an invalid one never reaches the
// app shell.
export function LoginPage() {
  const { setToken } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const redirect = params.get('redirect') || '/';

  const [mode, setMode] = useState<Mode>('password');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [token, setTokenDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const finish = (next: string, role?: string) => {
    setToken(next, role);
    navigate(redirect, { replace: true });
  };

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    setError('');
    try {
      const res = await fetch('/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim(), password }),
      });
      if (!res.ok) {
        setError(res.status === 401 ? '用户名或密码错误。' : `登录失败 (${res.status})。`);
        return;
      }
      const body = (await res.json()) as { token?: string; role?: string };
      if (!body.token) {
        setError('登录响应缺少令牌。');
        return;
      }
      finish(body.token, body.role);
    } catch {
      setError('网络错误，请重试。');
    } finally {
      setBusy(false);
    }
  };

  const submitToken = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = token.trim();
    if (!trimmed) return;
    setBusy(true);
    setError('');
    client.setConfig({ baseUrl: '/api/v2', auth: () => trimmed });
    const { error: err } = await listRepositories({ query: { pageSize: 1 } });
    setBusy(false);
    if (err) {
      setError('令牌无效或已过期，请检查后重试。');
      return;
    }
    finish(trimmed);
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-3">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-br from-cyan-500 to-blue-600 text-lg font-bold text-white">
            AG
          </div>
          <div className="text-center">
            <div className="text-lg font-semibold text-zinc-100">Artifact Gateway</div>
            <div className="text-xs uppercase tracking-widest text-zinc-600">Console 登录</div>
          </div>
        </div>

        <div className="mb-4 flex rounded-lg border border-zinc-800 p-0.5 text-xs">
          {(['password', 'token'] as const).map((m) => (
            <button
              key={m}
              onClick={() => {
                setMode(m);
                setError('');
              }}
              className={`flex-1 rounded-md py-1.5 transition-colors ${
                mode === m ? 'bg-cyan-500/10 font-medium text-cyan-300' : 'text-zinc-500 hover:text-zinc-300'
              }`}
            >
              {m === 'password' ? '账号密码' : '访问令牌'}
            </button>
          ))}
        </div>

        {mode === 'password' ? (
          <form onSubmit={submitPassword} className="space-y-4 rounded-xl border border-zinc-800 bg-zinc-900/60 p-6">
            <Field label="用户名">
              <input
                className={inputClass}
                placeholder="alice"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
            <Field label="密码">
              <input
                type="password"
                className={inputClass}
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
            {error && (
              <div className="rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                {error}
              </div>
            )}
            <button type="submit" disabled={busy || !username.trim() || !password} className={`${btnPrimary} w-full`}>
              {busy ? '登录中…' : '登录'}
            </button>
          </form>
        ) : (
          <form onSubmit={submitToken} className="space-y-4 rounded-xl border border-zinc-800 bg-zinc-900/60 p-6">
            <Field label="访问令牌" hint="静态令牌、OIDC 或 API 密钥的 Bearer；仅保存在本浏览器 localStorage。">
              <textarea
                className={`${inputClass} h-28 resize-none font-mono text-xs`}
                placeholder="粘贴 Bearer Token…"
                value={token}
                onChange={(e) => setTokenDraft(e.target.value)}
              />
            </Field>
            {error && (
              <div className="rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                {error}
              </div>
            )}
            <button type="submit" disabled={busy || !token.trim()} className={`${btnPrimary} w-full`}>
              {busy ? '验证中…' : '登录'}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
