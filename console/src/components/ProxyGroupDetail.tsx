import { useCallback, useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import { getGroupOperations, fetchViaProxy, listCacheEntries } from '../lib/v1proxy';
import type { ProxyFormat, V1Group, GroupOperations, CacheEntry } from '../lib/v1proxy';
import { Card, DataTable, inputClass, btnSecondary } from './Layout';
import { ErrorBanner, EmptyState } from './Feedback';
import { StateBadge, Badge } from './Badge';
import { formatBytes, formatNumber, shortDigest } from '../lib/format';

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          /* ignore */
        }
      }}
      className="shrink-0 rounded border border-zinc-700 px-2 py-0.5 text-[10px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
    >
      {copied ? '已复制 ✓' : '复制'}
    </button>
  );
}

function CommandBlock({ label, command }: { label: string; command: string }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <div className="mb-1 flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-zinc-500">{label}</span>
        <CopyButton text={command} />
      </div>
      <code className="block break-all font-mono text-xs leading-5 text-cyan-300">{command}</code>
    </div>
  );
}

// 把 GAV 坐标转成 maven 路径与 dependency XML
function mavenPath(gav: string): string | null {
  const parts = gav.trim().split(':');
  if (parts.length < 3) return null;
  const [g, a, v] = parts;
  const packaging = parts[3] ?? 'jar';
  const base = `${g.replaceAll('.', '/')}/${a}/${v}`;
  const file = `${a}-${v}.${packaging === 'pom' ? 'pom' : packaging}`;
  return `${base}/${file}`;
}

function mavenDependencyXml(gav: string): string | null {
  const parts = gav.trim().split(':');
  if (parts.length < 3) return null;
  const [g, a, v] = parts;
  return `<dependency>\n  <groupId>${g}</groupId>\n  <artifactId>${a}</artifactId>\n  <version>${v}</version>\n</dependency>`;
}

function gatewayBase(): string {
  return window.location.origin;
}

export function ProxyGroupDetail({ format, group }: { format: ProxyFormat; group: V1Group }) {
  const { token } = useAuth();
  const [ops, setOps] = useState<GroupOperations | null>(null);
  const [entries, setEntries] = useState<CacheEntry[] | null>(null);
  const [coordinate, setCoordinate] = useState('');
  const [fetching, setFetching] = useState(false);
  const [fetchResult, setFetchResult] = useState<{ status: number; bytes: number } | null>(null);
  const [fetchError, setFetchError] = useState<unknown>(null);

  const loadOps = useCallback(async () => {
    try {
      setOps(await getGroupOperations(token, group.name));
    } catch {
      setOps(null);
    }
  }, [token, group.name]);

  const loadEntries = useCallback(async () => {
    try {
      setEntries(await listCacheEntries(token, format, group.name));
    } catch {
      setEntries([]);
    }
  }, [token, format, group.name]);

  useEffect(() => {
    setCoordinate('');
    setFetchResult(null);
    setFetchError(null);
    void loadOps();
    void loadEntries();
  }, [loadOps, loadEntries]);

  const base = gatewayBase();
  const mvnPath = format === 'maven' ? mavenPath(coordinate) : null;
  const depXml = format === 'maven' ? mavenDependencyXml(coordinate) : null;

  // 各格式的拉取命令
  const commands: { label: string; command: string }[] = [];
  if (format === 'oci') {
    const image = coordinate.trim() || 'alpine:latest';
    commands.push(
      { label: 'Docker 拉取', command: `docker pull ${base.replace(/^https?:\/\//, '')}/${group.name}/${image}` },
      { label: '镜像名格式', command: `${group.name}/<image>:<tag>  例如 ${group.name}/alpine:latest（group 名即上游 namespace）` },
    );
  } else if (format === 'maven') {
    if (mvnPath) {
      commands.push({
        label: '直接下载 URL',
        command: `${base}/maven/${group.name}/${mvnPath}`,
      });
    }
    commands.push({
      label: 'Maven settings.xml',
      command: `<mirror>\n  <id>${group.name}</id>\n  <mirrorOf>central</mirrorOf>\n  <url>${base}/maven/${group.name}</url>\n</mirror>`,
    });
    if (depXml) commands.push({ label: '依赖坐标 pom.xml', command: depXml });
  } else if (format === 'raw') {
    const p = coordinate.trim() || 'owner/repo/main/file.txt';
    commands.push({ label: '直接下载 URL', command: `${base}/raw/${group.name}/${p}` });
  } else if (format === 'conan') {
    commands.push(
      { label: '添加 remote', command: `conan remote add ${group.name} ${base}/conan/v2/${group.name}` },
      { label: '安装包', command: `conan install --requires=${coordinate.trim() || 'hello/1.0'} -r ${group.name}` },
    );
  }

  const tryFetch = async () => {
    setFetching(true);
    setFetchResult(null);
    setFetchError(null);
    try {
      let path = coordinate.trim();
      if (format === 'maven') {
        if (!mvnPath) throw new Error('坐标格式应为 groupId:artifactId:version');
        path = mvnPath;
      } else if (format === 'oci') {
        // 拉 manifest
        const ref = path.includes(':') ? path : `${path}:latest`;
        const [img, tag] = ref.split(':');
        path = `${img}/manifests/${tag}`;
      }
      const r = await fetchViaProxy(token, format, group.name, path);
      setFetchResult(r);
      void loadOps();
      // 拉取后延迟刷新缓存清单（等待 index 落盘）
      setTimeout(() => void loadEntries(), 800);
    } catch (e) {
      setFetchError(e);
    } finally {
      setFetching(false);
    }
  };

  const coordinatePlaceholder: Record<ProxyFormat, string> = {
    oci: 'alpine:latest',
    maven: 'junit:junit:4.13.2',
    raw: 'owner/repo/main/README.md',
    conan: 'hello/1.0',
  };

  const hitRate = ops && ops.metrics.requests > 0 ? Math.round(ops.hit_rate * 100) : null;

  return (
    <div className="space-y-5">
      {/* 概要 + 指标 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-zinc-800 px-3 py-2.5">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">状态</div>
          <div className="mt-1"><StateBadge state={group.enabled ? 'enabled' : 'disabled'} /></div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2.5">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">请求数</div>
          <div className="mt-1 text-sm font-semibold text-zinc-100">{ops ? formatNumber(ops.metrics.requests) : '—'}</div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2.5">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">缓存命中率</div>
          <div className="mt-1 text-sm font-semibold text-zinc-100">
            {hitRate !== null ? `${hitRate}%` : '—'}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2.5">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">缓存配额</div>
          <div className="mt-1 text-sm font-semibold text-zinc-100">
            {group.cacheQuotaBytes ? formatBytes(group.cacheQuotaBytes) : '无限制'}
          </div>
        </div>
      </div>
      {ops && (
        <div className="flex flex-wrap gap-3 text-xs text-zinc-500">
          <span>命中 <span className="text-emerald-400">{formatNumber(ops.metrics.cache_hits)}</span></span>
          <span>未命中 <span className="text-amber-400">{formatNumber(ops.metrics.cache_misses)}</span></span>
          <span>上游错误 <span className="text-rose-400">{formatNumber(ops.metrics.upstream_errors)}</span></span>
        </div>
      )}

      {/* 拉取命令 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">拉取命令</div>
        {(format === 'maven' || format === 'oci' || format === 'conan' || format === 'raw') && (
          <div className="mb-3 flex gap-2">
            <input
              className={`${inputClass} font-mono text-xs`}
              placeholder={coordinatePlaceholder[format]}
              value={coordinate}
              onChange={(e) => setCoordinate(e.target.value)}
            />
            <button onClick={tryFetch} disabled={fetching || !coordinate.trim()} className={btnSecondary}>
              {fetching ? '拉取中…' : '试拉取'}
            </button>
          </div>
        )}
        {fetchError !== null && <div className="mb-3"><ErrorBanner error={fetchError} /></div>}
        {fetchResult && (
          <div
            className={`mb-3 rounded-lg border px-3 py-2 text-sm ${
              fetchResult.status < 400
                ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
                : 'border-rose-500/30 bg-rose-500/10 text-rose-300'
            }`}
          >
            {fetchResult.status < 400
              ? `✓ 拉取成功（HTTP ${fetchResult.status}，${formatBytes(fetchResult.bytes)}）`
              : `✗ 拉取失败（HTTP ${fetchResult.status}）`}
          </div>
        )}
        <div className="space-y-2">
          {commands.map((c) => (
            <CommandBlock key={c.label} label={c.label} command={c.command} />
          ))}
        </div>
      </div>

      {/* 缓存制品 */}
      <div>
        <div className="mb-2 flex items-center justify-between">
          <div className="text-sm font-medium text-zinc-200">缓存制品（{entries?.length ?? 0}）</div>
          <button onClick={() => void loadEntries()} className="text-xs text-zinc-500 hover:text-zinc-300">
            刷新
          </button>
        </div>
        {!entries ? (
          <p className="py-4 text-center text-sm text-zinc-500">加载中…</p>
        ) : entries.length === 0 ? (
          <Card>
            <EmptyState
              title="暂无缓存制品"
              hint="通过上方「试拉取」或客户端拉取后，制品会缓存并显示在这里"
            />
          </Card>
        ) : (
          <Card>
            <DataTable columns={['制品坐标', '摘要', '大小', '类型', '来源']}>
              {entries.map((e, i) => (
                <tr key={`${e.repository}-${e.digest}-${i}`} className="hover:bg-zinc-800/30">
                  <td className="max-w-md truncate px-4 py-2.5 font-mono text-xs text-zinc-200" title={e.repository}>
                    {e.repository}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-500" title={e.digest}>
                    {shortDigest(e.digest)}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-zinc-400">{formatBytes(e.size)}</td>
                  <td className="max-w-40 truncate px-4 py-2.5 text-xs text-zinc-500" title={e.contentType}>
                    {e.contentType ?? '—'}
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone="amber">{e.member ?? 'proxy'}</Badge>
                  </td>
                </tr>
              ))}
            </DataTable>
          </Card>
        )}
      </div>

      {/* 成员 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">成员（按解析顺序，hosted 优先、proxy 兜底）</div>
        <Card>
          <DataTable columns={['#', '名称', '类型', '上游地址', '允许主机']}>
            {[...group.members]
              .sort((a, b) => a.position - b.position)
              .map((m) => (
                <tr key={m.position} className="hover:bg-zinc-800/30">
                  <td className="px-4 py-2 font-mono text-xs text-zinc-500">{m.position}</td>
                  <td className="px-4 py-2 text-xs text-zinc-200">{m.name}</td>
                  <td className="px-4 py-2">
                    <Badge tone={m.type === 'proxy' ? 'amber' : 'cyan'}>{m.type}</Badge>
                  </td>
                  <td className="max-w-56 truncate px-4 py-2 font-mono text-xs text-zinc-400" title={m.endpoint}>
                    {m.endpoint || '—'}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-zinc-500">
                    {m.allowedHosts?.length ? m.allowedHosts.join(', ') : '—'}
                  </td>
                </tr>
              ))}
          </DataTable>
        </Card>
      </div>
    </div>
  );
}
