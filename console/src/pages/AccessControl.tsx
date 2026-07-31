import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { listRepositories, listGrants } from '../client';
import type { Repository, Grant } from '../client';
import { PageHeader, Card, CardHeader, DataTable, inputClass } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { FormatBadge, Badge } from '../components/Badge';

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

function scopeLabel(scopes: string[]): { label: string; tone: 'red' | 'blue' | 'green' | 'zinc' } {
  if (scopes.includes('repositories:admin')) return { label: 'admin', tone: 'red' };
  const parts: string[] = [];
  if (scopes.includes('repositories:read')) parts.push('read');
  if (scopes.includes('repositories:write')) parts.push('write');
  if (parts.length === 0) return { label: scopes.join(', ') || '—', tone: 'zinc' };
  return { label: parts.join(' + '), tone: parts.includes('write') ? 'blue' : 'green' };
}

export function AccessControlPage() {
  const [rows, setRows] = useState<GrantRow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [principalFilter, setPrincipalFilter] = useState('');
  const [repoFilter, setRepoFilter] = useState('');

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

  const filtered = (rows ?? []).filter(
    (r) =>
      (!principalFilter || r.principal.toLowerCase().includes(principalFilter.toLowerCase())) &&
      (!repoFilter || r.repositoryName.toLowerCase().includes(repoFilter.toLowerCase())),
  );

  return (
    <div>
      <PageHeader
        title="访问控制"
        description="跨仓库的授权总览与角色能力说明。逐仓库的细粒度授权在各仓库的「访问授权」Tab 编辑。"
      />

      <Card className="mb-6 px-5 py-4">
        <div className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500">角色能力</div>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {ROLE_REFERENCE.map((r) => (
            <div key={r.role} className="rounded-lg border border-zinc-800 px-3 py-2">
              <Badge tone={r.tone}>{r.role}</Badge>
              <p className="mt-1.5 text-xs text-zinc-400">{r.desc}</p>
            </div>
          ))}
        </div>
        <p className="mt-3 text-[11px] text-zinc-600">
          API 密钥与 OIDC 身份携带的全局角色在全仓库范围内生效，优先于逐仓库授权；逐仓库授权（下表）用于细粒度按主体/前缀控制。
        </p>
      </Card>

      <Card>
        <CardHeader title={`授权记录（${filtered.length}）`} />
        <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 px-4 py-3">
          <input
            className={`${inputClass} w-52`}
            placeholder="按主体过滤…"
            value={principalFilter}
            onChange={(e) => setPrincipalFilter(e.target.value)}
          />
          <input
            className={`${inputClass} w-52`}
            placeholder="按仓库过滤…"
            value={repoFilter}
            onChange={(e) => setRepoFilter(e.target.value)}
          />
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
