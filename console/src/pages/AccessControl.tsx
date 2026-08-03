import { useEffect, useState } from 'react';
import { Button, Input, Popconfirm, Select, Space, Switch } from 'antd';
import { ClearOutlined } from '@ant-design/icons';
import { Link } from 'react-router-dom';
import { getAnonymousAccessPolicy, listRepositories, listGrants, replaceAnonymousAccessPolicy } from '../client';
import type { AnonymousAccessPolicy, Repository, Grant } from '../client';
import { PageHeader, Card, CardHeader, DataTable } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { FormatBadge, Badge } from '../components/Badge';
import { useAuth } from '../lib/auth';

interface GrantRow {
  repositoryId: string;
  repositoryName: string;
  format: Repository['format'];
  principal: string;
  scopes: string[];
  resourcePrefix: string;
}

// Aggregates every repository's managed grant set into one view so an
// administrator can see who can access what without visiting each repository.
const ROLE_REFERENCE: { role: string; tone: 'red' | 'blue' | 'green'; desc: string }[] = [
  { role: 'admin', tone: 'red', desc: '全部操作：浏览、发布、删除、授权、密钥管理' },
  { role: 'writer', tone: 'blue', desc: '读 + 写（发布 / 编辑 / 复制取消），不可管理密钥与 admin 授权' },
  { role: 'reader', tone: 'green', desc: '只读：浏览 / 搜索 / 拉取' },
];

const AUTHORIZATION_STEPS = [
  { title: '1. 先看身份', text: '管理员身份直接放行；用户、API Key 或 OIDC 身份会先带着自己的全局角色进入判定。' },
  { title: '2. 再看全局角色', text: 'admin 允许全部操作，writer 允许读取和写入，reader 只允许读取。' },
  { title: '3. 再看仓库规则', text: '已配置仓库授权时，主体、权限级别和资源前缀都匹配才放行；未匹配会拒绝。' },
  { title: '4. 最后兼容旧策略', text: '仓库还没有被正式管理时，才回退到旧版静态仓库策略；匿名访问另按全局和仓库开关判断。' },
];

function scopeLabel(scopes: string[]): { label: string; tone: 'red' | 'blue' | 'green' | 'zinc' } {
  if (scopes.includes('repositories:admin')) return { label: 'admin', tone: 'red' };
  const parts: string[] = [];
  if (scopes.includes('repositories:read')) parts.push('read');
  if (scopes.includes('repositories:write')) parts.push('write');
  if (parts.length === 0) return { label: scopes.join(', ') || '—', tone: 'zinc' };
  return { label: parts.join(' + '), tone: parts.includes('write') ? 'blue' : 'green' };
}

export function AccessControlPage() {
  const { role } = useAuth();
  const canManageAnonymousPolicy = role === '' || role === 'admin';
  const [rows, setRows] = useState<GrantRow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [principalFilter, setPrincipalFilter] = useState('');
  const [repoFilter, setRepoFilter] = useState('');
  const [scopeFilter, setScopeFilter] = useState<'all' | 'read' | 'write' | 'admin'>('all');
  const [anonymousPolicy, setAnonymousPolicy] = useState<AnonymousAccessPolicy | null>(null);
  const [anonymousPolicyError, setAnonymousPolicyError] = useState<unknown>(null);
  const [savingAnonymousPolicy, setSavingAnonymousPolicy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setError(null);
      try {
        const { data, error: err } = await listRepositories({ query: { pageSize: 200 } });
        if (err || !data) throw err ?? new Error('加载仓库失败');
        const repos = data.items;
        const results = await Promise.all(
          repos.map((r) => listGrants({ path: { repositoryId: r.id } }).catch(() => null)),
        );
        if (cancelled) return;
        const out: GrantRow[] = [];
        repos.forEach((repo, i) => {
          const grants = (results[i] as { data?: Grant[] } | null)?.data ?? [];
          for (const g of grants) {
            out.push({
              repositoryId: repo.id,
              repositoryName: repo.name,
              format: repo.format,
              principal: g.principal,
              scopes: g.scopes,
              resourcePrefix: g.resourcePrefix ?? '',
            });
          }
        });
        out.sort(
          (a, b) => a.principal.localeCompare(b.principal) || a.repositoryName.localeCompare(b.repositoryName),
        );
        setRows(out);
      } catch (e) {
        if (!cancelled) setError(e);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setAnonymousPolicyError(null);
      const { data, error: err } = await getAnonymousAccessPolicy();
      if (cancelled) return;
      if (err || !data) {
        setAnonymousPolicyError(err ?? new Error('加载匿名访问策略失败'));
        return;
      }
      setAnonymousPolicy(data);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const updateAnonymousPolicy = async (enabled: boolean) => {
    if (!anonymousPolicy || savingAnonymousPolicy) return;
    setSavingAnonymousPolicy(true);
    setAnonymousPolicyError(null);
    const { data, error: err } = await replaceAnonymousAccessPolicy({
      body: { ...anonymousPolicy, enabled },
      headers: { 'If-Match': anonymousPolicy.version },
    });
    setSavingAnonymousPolicy(false);
    if (err || !data) {
      setAnonymousPolicyError(err ?? new Error('保存匿名访问策略失败'));
      return;
    }
    setAnonymousPolicy(data);
  };

  const filtered = (rows ?? []).filter(
    (r) =>
      (!principalFilter || r.principal.toLowerCase().includes(principalFilter.toLowerCase())) &&
      (!repoFilter || r.repositoryName.toLowerCase().includes(repoFilter.toLowerCase())),
  ).filter((r) => {
    const scope = scopeLabel(r.scopes).label;
    return scopeFilter === 'all' || scope === scopeFilter || (scopeFilter === 'write' && scope.includes('write'));
  });

  const grants = rows ?? [];
  const principalCount = new Set(grants.map((grant) => grant.principal)).size;
  const adminCount = grants.filter((grant) => grant.scopes.includes('repositories:admin')).length;
  const writeCount = grants.filter((grant) => grant.scopes.includes('repositories:write') && !grant.scopes.includes('repositories:admin')).length;

  return (
    <div>
      <PageHeader
        title="访问控制"
        description="跨仓库查看匿名策略与逐仓库授权；规则在对应仓库中编辑。"
      />

      <div className="mb-5 grid grid-cols-3 divide-x divide-zinc-800 overflow-hidden rounded-lg border border-zinc-800/80 bg-zinc-900/35">
        <div className="px-4 py-3"><div className="text-[10px] uppercase tracking-wider text-zinc-600">授权主体</div><div className="mt-1 text-lg font-semibold text-zinc-100">{principalCount}</div></div>
        <div className="px-4 py-3"><div className="text-[10px] uppercase tracking-wider text-zinc-600">写入授权</div><div className="mt-1 text-lg font-semibold text-zinc-100">{writeCount}</div></div>
        <div className="px-4 py-3"><div className="text-[10px] uppercase tracking-wider text-zinc-600">管理员授权</div><div className={adminCount > 0 ? 'mt-1 text-lg font-semibold text-rose-300' : 'mt-1 text-lg font-semibold text-zinc-100'}>{adminCount}</div></div>
      </div>

      <Card className="mb-5 px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="text-sm font-medium text-zinc-200">全局匿名读取</div>
            <p className="mt-1 text-xs text-zinc-500">仅为已启用匿名读取的仓库和组开放未认证协议读取。</p>
          </div>
          {anonymousPolicy && canManageAnonymousPolicy ? (
            <Popconfirm
              title={anonymousPolicy.enabled ? '确认停用全局匿名读取？' : '确认启用全局匿名读取？'}
              description={anonymousPolicy.enabled
                ? '停用后，所有未认证协议读取都会被拒绝，即使仓库或组允许匿名读取。'
                : '启用后，满足仓库或组匿名读取策略的制品可被未认证客户端读取。'}
              okText="继续"
              cancelText="取消"
              okButtonProps={{ danger: anonymousPolicy.enabled, loading: savingAnonymousPolicy }}
              onConfirm={() => updateAnonymousPolicy(!anonymousPolicy.enabled)}
            >
              <Switch checked={anonymousPolicy.enabled} loading={savingAnonymousPolicy} aria-label="切换全局匿名读取" onChange={() => undefined} />
            </Popconfirm>
          ) : canManageAnonymousPolicy ? (
            <span className="text-xs text-zinc-500">加载中…</span>
          ) : null}
        </div>
        {anonymousPolicyError !== null && <div className="mt-3"><ErrorBanner error={anonymousPolicyError} /></div>}
      </Card>

      <details className="group mb-5 border-y border-zinc-800/80 py-2.5">
        <summary className="flex cursor-pointer list-none items-center justify-between text-xs font-medium text-zinc-300">
          <span>权限判定顺序与角色能力</span>
          <span className="text-[11px] font-normal text-zinc-600 group-open:hidden">展开查看</span>
          <span className="hidden text-[11px] font-normal text-zinc-600 group-open:inline">收起</span>
        </summary>
        <p className="mt-3 text-xs leading-5 text-zinc-500">仓库规则只追加权限，不能撤销全局角色。</p>
        <div className="mt-3 grid grid-cols-4 gap-4">
          {AUTHORIZATION_STEPS.map((step) => (
            <div key={step.title} className="border-l border-zinc-700 pl-3">
              <div className="text-xs font-medium text-zinc-300">{step.title}</div>
              <p className="mt-1 text-[11px] leading-5 text-zinc-500">{step.text}</p>
            </div>
          ))}
        </div>
        <div className="mt-4 grid grid-cols-3 gap-2 border-t border-zinc-800/80 pt-3">
          {ROLE_REFERENCE.map((r) => (
            <div key={r.role} className="flex items-center gap-2 text-xs text-zinc-400">
              <Badge tone={r.tone}>{r.role}</Badge><span>{r.desc}</span>
            </div>
          ))}
        </div>
      </details>

      <Card>
        <CardHeader title={`授权记录（${filtered.length}）`} />
        <div className="overflow-x-auto border-b border-zinc-800/80 px-4 py-3">
          <div className="grid min-w-[820px] grid-cols-[minmax(220px,1fr)_minmax(220px,1fr)_180px_auto] items-end gap-3">
            <label>
              <span className="mb-1.5 block text-[11px] font-medium text-zinc-500">授权主体</span>
              <Input allowClear placeholder="用户名、API Key 或 actor" value={principalFilter} onChange={(e) => setPrincipalFilter(e.target.value)} />
            </label>
            <label>
              <span className="mb-1.5 block text-[11px] font-medium text-zinc-500">仓库</span>
              <Input allowClear placeholder="仓库名称" value={repoFilter} onChange={(e) => setRepoFilter(e.target.value)} />
            </label>
            <label>
              <span className="mb-1.5 block text-[11px] font-medium text-zinc-500">权限级别</span>
              <Select
                className="w-full"
                value={scopeFilter}
                options={[
                  { value: 'all', label: '全部权限' },
                  { value: 'read', label: '只读 · 浏览 / 拉取' },
                  { value: 'write', label: '写入 · 发布 / 编辑' },
                  { value: 'admin', label: '管理员 · 授权 / 删除' },
                ]}
                onChange={(value: typeof scopeFilter) => setScopeFilter(value)}
              />
            </label>
            <Space>
              <Button
                icon={<ClearOutlined />}
                disabled={!principalFilter && !repoFilter && scopeFilter === 'all'}
                onClick={() => { setPrincipalFilter(''); setRepoFilter(''); setScopeFilter('all'); }}
              >
                清除
              </Button>
            </Space>
          </div>
        </div>
        {error ? (
          <div className="px-4 py-4">
            <ErrorBanner error={error} />
          </div>
        ) : !rows ? (
          <Loading />
        ) : filtered.length === 0 ? (
          <EmptyState
            title={rows.length === 0 ? '暂无逐仓库授权' : '没有匹配的授权记录'}
            hint={rows.length === 0 ? '在各仓库的「访问授权」Tab 添加主体即可在此汇总' : '换个过滤条件试试'}
          />
        ) : (
          <DataTable columns={['主体', '仓库', '格式', '权限', '资源前缀', '']}>
            {filtered.map((r, i) => {
              const scope = scopeLabel(r.scopes);
              return (
                <tr key={`${r.repositoryId}-${r.principal}-${i}`} className="hover:bg-zinc-800/30">
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-200">{r.principal}</td>
                  <td className="px-4 py-2.5">
                    <Link
                      to={`/repositories/${r.repositoryId}`}
                      className="text-xs text-cyan-400 hover:text-cyan-300"
                    >
                      {r.repositoryName}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5">
                    <FormatBadge format={r.format} />
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone={scope.tone}>{scope.label}</Badge>
                    {scope.label === 'admin' && <span className="ml-2 text-[10px] text-rose-300">高权限</span>}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">{r.resourcePrefix || '—'}</td>
                  <td className="px-4 py-2.5 text-right">
                    <Link
                      to={`/repositories/${r.repositoryId}`}
                      className="text-xs text-zinc-500 hover:text-cyan-300"
                    >
                      编辑 →
                    </Link>
                  </td>
                </tr>
              );
            })}
          </DataTable>
        )}
      </Card>
    </div>
  );
}
