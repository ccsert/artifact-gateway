import { useState, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { client } from '../client/client.gen';
import { listRepositories } from '../client';
import { Field, inputClass, btnPrimary } from '../components/Layout';

// Standalone login route. The token is verified against the management API
// before it is persisted, so an invalid token never reaches the app shell.
// The OIDC single-sign-on button is reserved for a backend auth-code flow.
export function LoginPage() {
  const { setToken } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const redirect = params.get('redirect') || '/';

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const token = draft.trim();
    if (!token) return;
    setBusy(true);
    setError('');
    client.setConfig({ baseUrl: '/api/v2', auth: () => token });
    const { error: err } = await listRepositories({ query: { pageSize: 1 } });
    setBusy(false);
    if (err) {
      setError('令牌无效或已过期，请检查后重试。');
      return;
    }
    setToken(token);
    navigate(redirect, { replace: true });
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
        <form onSubmit={submit} className="space-y-4 rounded-xl border border-zinc-800 bg-zinc-900/60 p-6">
          <Field label="访问令牌" hint="管理 API 的 JWT Bearer 令牌；仅保存在本浏览器 localStorage。">
            <textarea
              className={`${inputClass} h-28 resize-none font-mono text-xs`}
              placeholder="粘贴 Bearer Token…"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
            />
          </Field>
          {error && (
            <div className="rounded-md border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
              {error}
            </div>
          )}
          <button type="submit" disabled={busy || !draft.trim()} className={`${btnPrimary} w-full`}>
            {busy ? '验证中…' : '登录'}
          </button>
        </form>
        <p className="mt-4 text-center text-[11px] text-zinc-600">
          通过 OIDC 单点登录将在后端支持后出现在此处。
        </p>
      </div>
    </div>
  );
}
