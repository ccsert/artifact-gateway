import { useEffect, useState } from 'react';
import { NavLink, Outlet, useNavigate, useSearchParams, useLocation, Navigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { Modal, useDisclosure } from '../components/Modal';
import { Field, inputClass, btnPrimary, btnSecondary } from '../components/Layout';

const navItems = [
  { to: '/', label: '总览', end: true, icon: IconDashboard, group: '运行' },
  { to: '/repositories', label: '仓库', end: false, icon: IconRepo, group: '运行' },
  { to: '/search', label: '制品搜索', end: false, icon: IconSearch, group: '运行' },
  { to: '/groups', label: '分组', end: false, icon: IconGroup, group: '治理' },
  { to: '/access', label: '访问控制', end: false, icon: IconAccess, group: '治理' },
  { to: '/audits', label: '审计日志', end: false, icon: IconAudit, group: '治理' },
  { to: '/audit-retention', label: '审计保留', end: false, icon: IconRetention, group: '治理' },
  { to: '/keys', label: 'API 密钥', end: false, icon: IconKey, group: '管理' },
  { to: '/users', label: '用户', end: false, icon: IconUser, group: '管理' },
];

function IconDashboard() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="3" width="7" height="9" rx="1" />
      <rect x="14" y="3" width="7" height="5" rx="1" />
      <rect x="14" y="12" width="7" height="9" rx="1" />
      <rect x="3" y="16" width="7" height="5" rx="1" />
    </svg>
  );
}
function IconRepo() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
      <path d="m3.3 7 8.7 5 8.7-5M12 22V12" />
    </svg>
  );
}
function IconGroup() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  );
}
function IconAudit() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <path d="M14 2v6h6M16 13H8M16 17H8M10 9H8" />
    </svg>
  );
}
function IconRetention() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="10" />
      <path d="M12 6v6l4 2" />
    </svg>
  );
}
function IconKey() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="m21 2-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  );
}
function IconUser() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
      <circle cx="12" cy="7" r="4" />
    </svg>
  );
}
function IconSearch() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-4-4" strokeLinecap="round" />
    </svg>
  );
}
function IconMenu() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M4 7h16M4 12h16M4 17h16" strokeLinecap="round" />
    </svg>
  );
}
function IconAccess() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  );
}
function GlobalSearchBox() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [value, setValue] = useState(params.get('q') ?? '');
  useEffect(() => {
    setValue(params.get('q') ?? '');
  }, [params]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const query = value.trim();
        if (query) navigate(`/search?q=${encodeURIComponent(query)}`);
      }}
      className="relative w-full max-w-md"
    >
      <input
        className={`${inputClass} pr-9`}
        placeholder="跨仓库搜索制品…"
        value={value}
        onChange={(e) => setValue(e.target.value)}
      />
      <button
        type="submit"
        aria-label="搜索"
        className="absolute inset-y-0 right-2 my-auto flex h-7 items-center text-zinc-500 hover:text-zinc-300"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="11" cy="11" r="7" />
          <path d="m21 21-4.3-4.3" strokeLinecap="round" />
        </svg>
      </button>
    </form>
  );
}

function TokenDialog() {
  const { token, setToken, clearToken } = useAuth();
  const dialog = useDisclosure();
  const [draft, setDraft] = useState('');

  return (
    <>
      <button
        onClick={() => {
          setDraft(token);
          dialog.show();
        }}
        className={`flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs ${
          token
            ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
            : 'border-amber-500/30 bg-amber-500/10 text-amber-300'
        }`}
      >
        <span className={`h-1.5 w-1.5 rounded-full ${token ? 'bg-emerald-400' : 'bg-amber-400'}`} />
        {token ? '已配置 Token' : '设置 Token'}
      </button>
      <Modal
        open={dialog.open}
        title="API 访问令牌"
        onClose={dialog.hide}
        footer={
          <>
            {token && (
              <button
                onClick={() => {
                  clearToken();
                  dialog.hide();
                }}
                className="mr-auto rounded-md border border-rose-500/40 px-3 py-1.5 text-sm text-rose-300 hover:bg-rose-500/10"
              >
                清除
              </button>
            )}
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button
              onClick={() => {
                setToken(draft);
                dialog.hide();
              }}
              className={btnPrimary}
            >
              保存
            </button>
          </>
        }
      >
        <Field label="Bearer Token" hint="管理 API 使用 JWT Bearer 认证，令牌仅保存在浏览器 localStorage。">
          <textarea
            className={`${inputClass} h-28 resize-none font-mono text-xs`}
            placeholder="粘贴 JWT…"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
          />
        </Field>
      </Modal>
    </>
  );
}

export function AppLayout() {
  const { token, role, clearToken } = useAuth();
  const [menuOpen, setMenuOpen] = useState(false);
  const visibleNavItems = navItems.filter((item) => !['/keys', '/users'].includes(item.to) || role === 'admin');
  const location = useLocation();
  if (!token) {
    if (location.pathname === '/' || location.pathname === '/search') {
      return <Navigate to={`/browse${location.search}`} replace />;
    }
    const target = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?redirect=${target}`} replace />;
  }
  return (
    <div className="flex min-h-screen">
      {menuOpen && <button aria-label="关闭导航" className="fixed inset-0 z-30 bg-black/60 lg:hidden" onClick={() => setMenuOpen(false)} />}
      <aside className={`fixed inset-y-0 left-0 z-40 flex w-56 flex-col border-r border-zinc-800/80 bg-zinc-925 bg-zinc-900/95 transition-transform lg:translate-x-0 lg:bg-zinc-900/40 ${menuOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="flex items-center gap-2.5 px-5 py-5">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-cyan-500 to-blue-600 text-sm font-bold text-white">
            AG
          </div>
          <div>
            <div className="text-sm font-semibold text-zinc-100">Artifact Gateway</div>
            <div className="text-[10px] uppercase tracking-widest text-zinc-600">Console</div>
          </div>
        </div>
        <nav className="mt-2 flex-1 space-y-5 px-3">
          {(['运行', '治理', '管理'] as const).map((group) => {
            const items = visibleNavItems.filter((item) => item.group === group);
            if (items.length === 0) return null;
            return (
              <div key={group}>
                <div className="mb-1 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-zinc-600">{group}</div>
                <div className="space-y-0.5">
                  {items.map((item) => (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      end={item.end}
                      onClick={() => setMenuOpen(false)}
                      className={({ isActive }) =>
                        `group relative flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors ${
                          isActive
                            ? 'bg-cyan-400/10 font-medium text-cyan-200'
                            : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                        }`
                      }
                    >
                      <span className="text-zinc-500 transition-colors group-hover:text-zinc-300"><item.icon /></span>
                      {item.label}
                    </NavLink>
                  ))}
                </div>
              </div>
            );
          })}
        </nav>
        <div className="border-t border-zinc-800/80 px-4 py-3 text-[10px] leading-4 text-zinc-600">
          Native Hosted API v2
        </div>
      </aside>
      <div className="flex min-h-screen flex-1 flex-col lg:ml-56">
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-zinc-800/80 bg-zinc-950/80 px-4 py-3 backdrop-blur lg:px-6">
          <button aria-label="打开导航" onClick={() => setMenuOpen(true)} className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-zinc-700 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100 lg:hidden"><IconMenu /></button>
          <GlobalSearchBox />
          <div className="ml-auto flex items-center gap-2">
            <TokenDialog />
            <button
              onClick={() => clearToken()}
              className="rounded-md border border-zinc-700 px-2.5 py-1.5 text-xs text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
              title="退出登录"
            >
              退出
            </button>
          </div>
        </header>
        <main className="flex-1 px-4 py-5 lg:px-6 lg:py-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
