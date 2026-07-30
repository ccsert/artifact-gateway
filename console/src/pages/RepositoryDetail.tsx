import { useCallback, useEffect, useState, Fragment } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  getRepository,
  updateRepository,
  searchRepositoryArtifacts,
  listOciImages,
  listMavenCoordinates,
  listConanReferences,
  listGrants,
  replaceGrants,
  getRetentionPolicy,
  replaceRetentionPolicy,
  dryRunRepositoryRetention,
  executeRepositoryRetention,
  getRepositoryCapacity,
  replaceRepositoryCapacity,
  listRepositoryLifecycleJobs,
  listRepositoryTombstones,
  restoreRepositoryArtifact,
  getRepositoryCapabilities,
  listRepositories,
  createRepositoryPromotion,
  listRepositoryReplications,
  createRepositoryReplication,
  getRepositoryReplication,
} from '../client';
import type {
  Repository,
  Grant,
  RetentionPolicy,
  RepositoryCapacity,
  LifecycleJob,
  ArtifactTombstone,
  RetentionDryRun,
  RepositoryCapabilities,
  ReplicationPlan,
  ReplicationPlanDetail,
} from '../client';
import { PageHeader, Card, CardHeader, DataTable, Pagination, Field, inputClass, btnPrimary, btnSecondary, btnDanger } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState, isNotFound } from '../components/Feedback';
import { FormatBadge, StateBadge, Badge } from '../components/Badge';
import { Modal, useDisclosure } from '../components/Modal';
import { OciImageDetail } from '../components/OciImageDetail';
import { MavenPublishWizard } from '../components/MavenPublishWizard';
import { MavenArtifactDetail, ConanArtifactDetail, RawArtifactDetail } from '../components/ArtifactRowDetail';
import { mavenGA, mavenVersion } from '../lib/usage';
import { formatBytes, formatDate, formatNumber, shortDigest } from '../lib/format';

type Tab = 'artifacts' | 'publish' | 'grants' | 'retention' | 'capacity' | 'distribute' | 'jobs' | 'tombstones';

const TABS: { key: Tab; label: string; formats?: string[] }[] = [
  { key: 'artifacts', label: '制品' },
  { key: 'publish', label: '发布', formats: ['maven'] },
  { key: 'grants', label: '访问授权' },
  { key: 'retention', label: '保留策略' },
  { key: 'capacity', label: '容量' },
  { key: 'distribute', label: '晋升 / 复制' },
  { key: 'jobs', label: '生命周期任务' },
  { key: 'tombstones', label: '墓碑' },
];

/* ---------------- Artifacts ---------------- */

// 统一的制品行，按格式从不同端点归一化而来
interface ArtifactRow {
  key: string;
  coordinate: string;
  digest?: string;
  createdAt?: string;
  state?: string;
  size?: number;
  contentType?: string;
  publisher?: string;
  // maven 聚合：同 group:artifact 的版本数与最新版本
  versionCount?: number;
  latestVersion?: string;
}

function ArtifactsTab({ repo }: { repo: Repository }) {
  const [q, setQ] = useState('');
  const [rows, setRows] = useState<ArtifactRow[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [expandedImage, setExpandedImage] = useState<string | null>(null);

  const format = repo.format;

  const load = useCallback(
    async (query: string, pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const page = { pageSize: 50, pageToken };
      let items: ArtifactRow[] = [];
      let next: string | undefined;
      let err: unknown = null;

      if (format === 'oci') {
        const r = await listOciImages({ path: { repositoryId: repo.id }, query: { q: query || undefined, ...page } });
        err = r.error;
        items = (r.data?.items ?? []).map((x) => ({ key: x.name, coordinate: x.name }));
        next = r.data?.nextPageToken;
      } else if (format === 'maven') {
        if (query) {
          const r = await searchRepositoryArtifacts({ path: { repositoryId: repo.id }, query: { q: query, ...page } });
          err = r.error;
          items = (r.data?.items ?? []).map((x, i) => ({
            key: `${x.coordinate}-${i}`,
            coordinate: x.coordinate,
            digest: x.digest,
            createdAt: x.createdAt,
            size: x.size,
            contentType: x.contentType,
          }));
          next = r.data?.nextPageToken;
        } else {
          const r = await listMavenCoordinates({ path: { repositoryId: repo.id }, query: page });
          err = r.error;
          // 按 group:artifact 聚合：每个制品一行，显示版本数与最新版本
          const byGA = new Map<string, { versions: { coordinate: string; digest?: string; createdAt?: string; publisher?: string }[] }>();
          for (const x of r.data?.items ?? []) {
            const ga = mavenGA(x.coordinate) ?? x.coordinate;
            if (!byGA.has(ga)) byGA.set(ga, { versions: [] });
            byGA.get(ga)!.versions.push(x);
          }
          items = Array.from(byGA.entries()).map(([ga, { versions }]) => {
            const sorted = [...versions].sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''));
            const latest = sorted[0];
            return {
              key: ga,
              coordinate: ga,
              digest: latest?.digest,
              createdAt: latest?.createdAt,
              publisher: latest?.publisher,
              versionCount: versions.length,
              latestVersion: mavenVersion(latest?.coordinate ?? '') ?? undefined,
            };
          });
          next = r.data?.nextPageToken;
        }
      } else if (format === 'conan') {
        const r = await listConanReferences({ path: { repositoryId: repo.id }, query: page });
        err = r.error;
        items = (r.data?.items ?? [])
          .filter((x) => !query || x.reference.includes(query))
          .map((x) => ({ key: x.reference, coordinate: x.reference, publisher: x.publisher }));
        next = r.data?.nextPageToken;
      } else {
        // raw：通用搜索端点
        const r = await searchRepositoryArtifacts({ path: { repositoryId: repo.id }, query: { q: query || undefined, ...page } });
        err = r.error;
        items = (r.data?.items ?? []).map((x, i) => ({
          key: `${x.coordinate}-${i}`,
          coordinate: x.coordinate,
          digest: x.digest,
          createdAt: x.createdAt,
          size: x.size,
          contentType: x.contentType,
        }));
        next = r.data?.nextPageToken;
      }

      setLoading(false);
      if (err) {
        setError(err);
        return;
      }
      setRows((prev) => (pageToken ? [...prev, ...items] : items));
      setNextToken(next);
    },
    [repo.id, format],
  );

  useEffect(() => {
    void load('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  const searchPlaceholder: Record<string, string> = {
    oci: '按镜像名前缀过滤…',
    maven: '搜索 GAV 坐标…',
    conan: '按引用名过滤…',
    raw: '搜索路径…',
  };

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void load(q);
          }}
          className="flex gap-2"
        >
          <input
            className={`${inputClass} w-72`}
            placeholder={searchPlaceholder[format] ?? '搜索…'}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button type="submit" className={btnSecondary}>
            搜索
          </button>
        </form>
        {q && (
          <button
            onClick={() => {
              setQ('');
              void load('');
            }}
            className="text-xs text-zinc-500 hover:text-zinc-300"
          >
            ← 返回完整列表
          </button>
        )}
      </div>
      {error !== null ? (
        isNotFound(error) ? (
          <NotEnabled feature="制品浏览" />
        ) : (
          <ErrorBanner error={error} onRetry={() => load(q)} />
        )
      ) : loading ? (
        <Loading />
      ) : rows.length === 0 ? (
        <EmptyState
          title={q ? '没有匹配的制品' : '暂无制品'}
          hint={q ? '换个关键词试试' : `通过 ${format} 客户端推送制品后会显示在这里`}
        />
      ) : (
        <>
          <DataTable columns={format === 'oci' || format === 'conan' ? ['名称'] : format === 'maven' ? ['制品', '版本', '最新版本', '更新时间'] : ['坐标', '摘要', '大小', '创建时间']}>
            {rows.map((r) => {
              const colCount = format === 'oci' || format === 'conan' ? 1 : 4;
              const expanded = expandedImage === r.coordinate;
              return (
                <Fragment key={r.key}>
                  <tr
                    className="cursor-pointer hover:bg-zinc-800/30"
                    onClick={() => setExpandedImage(expanded ? null : r.coordinate)}
                  >
                    <td className="max-w-md truncate px-4 py-2.5 font-mono text-xs text-zinc-200" title={r.coordinate}>
                      <span className="mr-1.5 text-zinc-600">{expanded ? '▾' : '▸'}</span>
                      {r.coordinate}
                    </td>
                    {format === 'maven' && (
                      <>
                        <td className="px-4 py-2.5">
                          <Badge tone="zinc">{r.versionCount ?? 1} 个版本</Badge>
                        </td>
                        <td className="px-4 py-2.5 font-mono text-xs text-cyan-300">{r.latestVersion ?? '—'}</td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(r.createdAt)}</td>
                      </>
                    )}
                    {format !== 'oci' && format !== 'conan' && format !== 'maven' && (
                      <>
                        <td className="px-4 py-2.5 font-mono text-xs text-zinc-500" title={r.digest}>
                          {shortDigest(r.digest)}
                        </td>
                        <td className="px-4 py-2.5 text-xs text-zinc-400">{formatBytes(r.size)}</td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(r.createdAt)}</td>
                      </>
                    )}
                  </tr>
                  {expanded && (
                    <tr className="bg-zinc-900/50">
                      <td className="px-4 py-4" colSpan={colCount}>
                        {format === 'oci' && (
                          <OciImageDetail repository={repo.name} image={r.coordinate} onDeleted={() => void load(q)} />
                        )}
                        {format === 'maven' && (
                          <MavenArtifactDetail
                            repoId={repo.id}
                            repoName={repo.name}
                            meta={{
                              coordinate: r.coordinate,
                              digest: r.digest,
                              size: r.size,
                              createdAt: r.createdAt,
                              publisher: r.publisher,
                            }}
                          />
                        )}
                        {format === 'conan' && (
                          <ConanArtifactDetail
                            repoId={repo.id}
                            repoName={repo.name}
                            meta={{ coordinate: r.coordinate, publisher: r.publisher }}
                          />
                        )}
                        {format === 'raw' && (
                          <RawArtifactDetail
                            repoName={repo.name}
                            meta={{
                              coordinate: r.coordinate,
                              digest: r.digest,
                              size: r.size,
                              createdAt: r.createdAt,
                            }}
                          />
                        )}
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </DataTable>
          <Pagination hasMore={!!nextToken} onMore={() => load(q, nextToken)} />
        </>
      )}
    </div>
  );
}

/* ---------------- Grants ---------------- */

const SCOPES = ['repositories:read', 'repositories:write', 'repositories:admin'] as const;

function GrantsTab({ repo }: { repo: Repository }) {
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [version, setVersion] = useState('');
  const editor = useDisclosure();
  const [draft, setDraft] = useState<Grant[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err, response } = await listGrants({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    setGrants(data ?? []);
    const etag = response.headers.get('ETag');
    setVersion(etag ? etag.replaceAll('"', '') : repo.version);
  }, [repo.id, repo.version]);

  useEffect(() => {
    void load();
  }, [load]);

  const openEditor = () => {
    setDraft(grants ? grants.map((g) => ({ ...g, scopes: [...g.scopes] })) : []);
    setSaveError(null);
    editor.show();
  };

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    const { error: err } = await replaceGrants({
      path: { repositoryId: repo.id },
      body: draft.filter((g) => g.principal.trim()),
      headers: { 'If-Match': version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    editor.hide();
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? <NotEnabled feature="访问授权" /> : <ErrorBanner error={error} onRetry={load} />;
  if (!grants) return <Loading />;

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <button onClick={openEditor} className={btnPrimary}>
          编辑授权
        </button>
      </div>
      {grants.length === 0 ? (
        <EmptyState title="暂无授权规则" hint="添加 principal 以开放仓库访问" />
      ) : (
        <DataTable columns={['Principal', 'Scopes', '资源前缀']}>
          {grants.map((g, i) => (
            <tr key={i} className="hover:bg-zinc-800/30">
              <td className="px-4 py-2.5 font-mono text-xs text-zinc-200">{g.principal}</td>
              <td className="px-4 py-2.5">
                <div className="flex flex-wrap gap-1">
                  {g.scopes.map((s) => (
                    <Badge key={s} tone={s.endsWith('admin') ? 'red' : s.endsWith('write') ? 'amber' : 'green'}>
                      {s.replace('repositories:', '')}
                    </Badge>
                  ))}
                </div>
              </td>
              <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">{g.resourcePrefix || '（整个仓库）'}</td>
            </tr>
          ))}
        </DataTable>
      )}
      <Modal
        open={editor.open}
        title="编辑访问授权"
        onClose={editor.hide}
        wide
        footer={
          <>
            <button onClick={editor.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={save} disabled={saving} className={btnPrimary}>
              {saving ? '保存中…' : '保存'}
            </button>
          </>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          {draft.map((g, i) => (
            <div key={i} className="flex flex-wrap items-center gap-2 rounded-lg border border-zinc-800 p-3">
              <input
                className={`${inputClass} w-48 font-mono text-xs`}
                placeholder="principal，如 user:alice"
                value={g.principal}
                onChange={(e) =>
                  setDraft((d) => d.map((x, j) => (j === i ? { ...x, principal: e.target.value } : x)))
                }
              />
              <input
                className={`${inputClass} w-40 font-mono text-xs`}
                placeholder="资源前缀（可空）"
                value={g.resourcePrefix ?? ''}
                onChange={(e) =>
                  setDraft((d) => d.map((x, j) => (j === i ? { ...x, resourcePrefix: e.target.value } : x)))
                }
              />
              <div className="flex gap-1.5">
                {SCOPES.map((s) => {
                  const active = g.scopes.includes(s);
                  return (
                    <button
                      key={s}
                      type="button"
                      onClick={() =>
                        setDraft((d) =>
                          d.map((x, j) =>
                            j === i
                              ? {
                                  ...x,
                                  scopes: active ? x.scopes.filter((v) => v !== s) : [...x.scopes, s],
                                }
                              : x,
                          ),
                        )
                      }
                      className={`rounded border px-2 py-1 font-mono text-[11px] ${
                        active
                          ? 'border-cyan-500/60 bg-cyan-500/10 text-cyan-300'
                          : 'border-zinc-700 text-zinc-500 hover:bg-zinc-800'
                      }`}
                    >
                      {s.replace('repositories:', '')}
                    </button>
                  );
                })}
              </div>
              <button
                onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}
                className="ml-auto rounded px-2 py-1 text-xs text-zinc-600 hover:bg-rose-500/10 hover:text-rose-400"
              >
                移除
              </button>
            </div>
          ))}
          <button
            onClick={() => setDraft((d) => [...d, { principal: '', scopes: ['repositories:read'] }])}
            className="w-full rounded-lg border border-dashed border-zinc-700 py-2 text-sm text-zinc-500 hover:border-zinc-500 hover:text-zinc-300"
          >
            + 添加授权
          </button>
        </div>
      </Modal>
    </div>
  );
}

/* ---------------- Retention ---------------- */

function RetentionTab({ repo }: { repo: Repository }) {
  const [policy, setPolicy] = useState<RetentionPolicy | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [keepDays, setKeepDays] = useState(0);
  const [minimumVersions, setMinimumVersions] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [dryRun, setDryRun] = useState<RetentionDryRun | null>(null);
  const [dryRunning, setDryRunning] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [notice, setNotice] = useState('');

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRetentionPolicy({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setPolicy(data);
      setKeepDays(data.keepDays);
      setMinimumVersions(data.minimumVersions);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    if (!policy) return;
    setSaving(true);
    setSaveError(null);
    setNotice('');
    const { error: err } = await replaceRetentionPolicy({
      path: { repositoryId: repo.id },
      body: { ...policy, keepDays, minimumVersions },
      headers: { 'If-Match': policy.version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice('策略已保存');
    void load();
  };

  const runDryRun = async () => {
    setDryRunning(true);
    setDryRun(null);
    const { data, error: err } = await dryRunRepositoryRetention({ path: { repositoryId: repo.id } });
    setDryRunning(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setDryRun(data ?? null);
  };

  const execute = async () => {
    setExecuting(true);
    const { error: err } = await executeRepositoryRetention({
      path: { repositoryId: repo.id },
      headers: { 'Idempotency-Key': crypto.randomUUID() },
    });
    setExecuting(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice('保留执行任务已提交，请在「生命周期任务」标签页查看进度');
    setDryRun(null);
  };

  if (error !== null)
    return isNotFound(error) ? <NotEnabled feature="保留策略" /> : <ErrorBanner error={error} onRetry={load} />;
  if (!policy) return <Loading />;

  return (
    <div className="space-y-6">
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-300">
          {notice}
        </div>
      )}
      <div className="grid max-w-md grid-cols-2 gap-4">
        <Field label="保留天数 (keepDays)">
          <input
            type="number"
            min={0}
            className={inputClass}
            value={keepDays}
            onChange={(e) => setKeepDays(Number(e.target.value))}
          />
        </Field>
        <Field label="最少保留版本数 (minimumVersions)">
          <input
            type="number"
            min={0}
            className={inputClass}
            value={minimumVersions}
            onChange={(e) => setMinimumVersions(Number(e.target.value))}
          />
        </Field>
      </div>
      <div className="flex gap-2">
        <button onClick={save} disabled={saving} className={btnPrimary}>
          {saving ? '保存中…' : '保存策略'}
        </button>
        <button onClick={runDryRun} disabled={dryRunning} className={btnSecondary}>
          {dryRunning ? '分析中…' : '试运行'}
        </button>
        {dryRun && dryRun.candidates.length > 0 && (
          <button onClick={execute} disabled={executing} className={btnDanger}>
            {executing ? '提交中…' : `执行清理（${dryRun.candidates.length} 个候选）`}
          </button>
        )}
      </div>
      {dryRun && (
        <Card>
          <CardHeader title={`试运行结果：${dryRun.candidates.length} 个候选制品（策略版本 ${dryRun.policyVersion}）`} />
          {dryRun.candidates.length === 0 ? (
            <EmptyState title="没有需要清理的制品" />
          ) : (
            <DataTable columns={['坐标', '摘要', '创建时间']}>
              {dryRun.candidates.map((c, i) => (
                <tr key={i}>
                  <td className="max-w-md truncate px-4 py-2.5 font-mono text-xs text-zinc-200">{c.coordinate}</td>
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">{shortDigest(c.digest)}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(c.createdAt)}</td>
                </tr>
              ))}
            </DataTable>
          )}
        </Card>
      )}
    </div>
  );
}

/* ---------------- Capacity ---------------- */

function CapacityTab({ repo }: { repo: Repository }) {
  const [capacity, setCapacity] = useState<RepositoryCapacity | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [quotaGiB, setQuotaGiB] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState('');

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRepositoryCapacity({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setCapacity(data);
      setQuotaGiB(Math.round(data.quotaBytes / 2 ** 30));
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    setNotice('');
    const { error: err } = await replaceRepositoryCapacity({
      path: { repositoryId: repo.id },
      body: { quotaBytes: quotaGiB * 2 ** 30 },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice('配额已更新');
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? <NotEnabled feature="容量管理" /> : <ErrorBanner error={error} onRetry={load} />;
  if (!capacity) return <Loading />;

  const pct = capacity.quotaBytes > 0 ? Math.min(100, (capacity.usedBytes / capacity.quotaBytes) * 100) : 0;

  return (
    <div className="space-y-6">
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-300">
          {notice}
        </div>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">已用空间</div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">{formatBytes(capacity.usedBytes)}</div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">对象数量</div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">{formatNumber(capacity.objectCount)}</div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">配额</div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {capacity.quotaBytes > 0 ? formatBytes(capacity.quotaBytes) : '无限制'}
          </div>
        </div>
      </div>
      {capacity.quotaBytes > 0 && (
        <div>
          <div className="mb-1.5 flex justify-between text-xs text-zinc-500">
            <span>使用率</span>
            <span>{pct.toFixed(1)}%</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-zinc-800">
            <div
              className={`h-full rounded-full transition-all ${pct > 90 ? 'bg-rose-500' : pct > 70 ? 'bg-amber-500' : 'bg-cyan-500'}`}
              style={{ width: `${pct}%` }}
            />
          </div>
        </div>
      )}
      <div className="flex max-w-md items-end gap-2">
        <Field label="配额 (GiB，0 表示无限制)">
          <input
            type="number"
            min={0}
            className={inputClass}
            value={quotaGiB}
            onChange={(e) => setQuotaGiB(Number(e.target.value))}
          />
        </Field>
        <button onClick={save} disabled={saving} className={btnPrimary}>
          {saving ? '保存中…' : '更新配额'}
        </button>
      </div>
    </div>
  );
}

/* ---------------- Distribute (Promotion / Replication) ---------------- */

function DistributeTab({ repo }: { repo: Repository }) {
  const [repos, setRepos] = useState<Repository[]>([]);
  const [targetId, setTargetId] = useState('');
  const [coordinate, setCoordinate] = useState('');
  const [digest, setDigest] = useState('');
  const [plans, setPlans] = useState<ReplicationPlan[] | null>(null);
  const [detail, setDetail] = useState<ReplicationPlanDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState<'promote' | 'replicate' | null>(null);

  const targets = repos.filter((r) => r.id !== repo.id && r.format === repo.format && r.state === 'active');

  const load = useCallback(async () => {
    setError(null);
    const [allRepos, p] = await Promise.all([
      listRepositories({ query: { pageSize: 200 } }),
      listRepositoryReplications({ path: { repositoryId: repo.id } }),
    ]);
    setRepos(allRepos.data?.items ?? []);
    if (p.error) {
      if (!isNotFound(p.error)) setError(p.error);
      setPlans([]);
      return;
    }
    setPlans(p.data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const submit = async (kind: 'promote' | 'replicate') => {
    setBusy(kind);
    setActionError(null);
    setNotice('');
    const body = { targetRepositoryId: targetId, coordinate: coordinate.trim(), digest: digest.trim() };
    const headers = { 'Idempotency-Key': crypto.randomUUID() };
    const { error: err } =
      kind === 'promote'
        ? await createRepositoryPromotion({ path: { repositoryId: repo.id }, body, headers })
        : await createRepositoryReplication({ path: { repositoryId: repo.id }, body, headers });
    setBusy(null);
    if (err) {
      setActionError(err);
      return;
    }
    setNotice(kind === 'promote' ? '晋升任务已提交，请在「生命周期任务」查看进度' : '复制计划已创建，下方查看进度');
    setCoordinate('');
    setDigest('');
    void load();
  };

  const showDetail = async (planId: string) => {
    const { data, error: err } = await getRepositoryReplication({ path: { repositoryId: repo.id, replicationPlanId: planId } });
    if (err) {
      setActionError(err);
      return;
    }
    setDetail(data ?? null);
  };

  const repoName = (id: string) => repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + '…';

  if (error !== null) return <ErrorBanner error={error} onRetry={load} />;

  return (
    <div className="space-y-6">
      {actionError !== null && <ErrorBanner error={actionError} />}
      {notice && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-300">
          {notice}
        </div>
      )}

      {/* 发起表单 */}
      <div className="rounded-lg border border-zinc-800 p-4">
        <div className="mb-3 text-sm font-medium text-zinc-200">发起晋升 / 复制</div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
          <Field label="目标仓库">
            <select className={inputClass} value={targetId} onChange={(e) => setTargetId(e.target.value)}>
              <option value="">选择同格式仓库…</option>
              {targets.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="坐标 coordinate">
            <input
              className={`${inputClass} font-mono text-xs`}
              placeholder="如 nginx:alpine 或 GAV"
              value={coordinate}
              onChange={(e) => setCoordinate(e.target.value)}
            />
          </Field>
          <Field label="摘要 digest">
            <input
              className={`${inputClass} font-mono text-xs`}
              placeholder="sha256:…"
              value={digest}
              onChange={(e) => setDigest(e.target.value)}
            />
          </Field>
          <div className="flex items-end gap-2">
            <button
              onClick={() => submit('promote')}
              disabled={busy !== null || !targetId || !coordinate.trim() || !digest.trim()}
              className={btnPrimary}
            >
              {busy === 'promote' ? '提交中…' : '晋升'}
            </button>
            <button
              onClick={() => submit('replicate')}
              disabled={busy !== null || !targetId || !coordinate.trim() || !digest.trim()}
              className={btnSecondary}
            >
              {busy === 'replicate' ? '提交中…' : '复制'}
            </button>
          </div>
        </div>
        <p className="mt-2 text-xs text-zinc-600">
          晋升：在目标仓库创建同一制品的可见副本（审计追踪）；复制：异步、带断点地拷贝制品字节到目标仓库。
        </p>
      </div>

      {/* 复制计划列表 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">复制计划（{plans?.length ?? 0}）</div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title="暂无复制计划" />
        ) : (
          <DataTable columns={['ID', '目标仓库', '状态', '创建时间', '完成时间', '']}>
            {plans.map((p) => (
              <tr key={p.id} className="hover:bg-zinc-800/30">
                <td className="px-4 py-2.5 font-mono text-xs text-zinc-500" title={p.id}>
                  {p.id.slice(0, 8)}…
                </td>
                <td className="px-4 py-2.5 text-xs text-zinc-300">{repoName(p.targetRepositoryId)}</td>
                <td className="px-4 py-2.5">
                  <StateBadge state={p.state} />
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(p.createdAt)}</td>
                <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(p.completedAt)}</td>
                <td className="px-4 py-2.5 text-right">
                  <button
                    onClick={() => showDetail(p.id)}
                    className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:bg-zinc-800"
                  >
                    进度
                  </button>
                </td>
              </tr>
            ))}
          </DataTable>
        )}
      </div>

      {/* 复制进度详情 */}
      <Modal open={!!detail} title="复制进度详情" onClose={() => setDetail(null)} wide>
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-zinc-400">
              <span>
                状态：<StateBadge state={detail.state} />
              </span>
              <span>目标：{repoName(detail.targetRepositoryId)}</span>
              <span>创建：{formatDate(detail.createdAt)}</span>
              {detail.lastError && <span className="text-rose-400">{detail.lastError}</span>}
            </div>
            {detail.checkpoints.length === 0 ? (
              <p className="py-4 text-center text-sm text-zinc-500">暂无检查点</p>
            ) : (
              <DataTable columns={['对象', '摘要', '大小', '进度', '状态', '重试']}>
                {detail.checkpoints.map((c, i) => (
                  <tr key={i} className="hover:bg-zinc-800/30">
                    <td className="max-w-48 truncate px-4 py-2 font-mono text-xs text-zinc-300" title={c.objectKey}>
                      {c.objectKey}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs text-zinc-500">{shortDigest(c.digest)}</td>
                    <td className="px-4 py-2 text-xs text-zinc-400">{formatBytes(c.size)}</td>
                    <td className="px-4 py-2 text-xs text-zinc-400">
                      {c.size > 0 ? `${Math.round((c.byteOffset / c.size) * 100)}%` : '—'}
                    </td>
                    <td className="px-4 py-2">
                      <StateBadge state={c.state} />
                    </td>
                    <td className="px-4 py-2 text-xs text-zinc-500">{c.attempts}</td>
                  </tr>
                ))}
              </DataTable>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}

/* ---------------- Jobs ---------------- */

function JobsTab({ repo }: { repo: Repository }) {
  const [jobs, setJobs] = useState<LifecycleJob[] | null>(null);
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listRepositoryLifecycleJobs({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    setJobs(data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 10000);
    return () => clearInterval(timer);
  }, [load]);

  if (error !== null)
    return isNotFound(error) ? <NotEnabled feature="生命周期任务" /> : <ErrorBanner error={error} onRetry={load} />;
  if (!jobs) return <Loading />;
  if (jobs.length === 0) return <EmptyState title="暂无生命周期任务" hint="保留清理、晋升、复制任务会显示在这里" />;

  return (
    <DataTable columns={['ID', '类型', '状态', '创建时间', '完成时间', '错误']}>
      {jobs.map((j) => (
        <tr key={j.id} className="hover:bg-zinc-800/30">
          <td className="px-4 py-2.5 font-mono text-xs text-zinc-500" title={j.id}>
            {j.id.slice(0, 8)}…
          </td>
          <td className="px-4 py-2.5">
            <Badge tone="blue">{j.kind}</Badge>
          </td>
          <td className="px-4 py-2.5">
            <StateBadge state={j.state} />
          </td>
          <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(j.createdAt)}</td>
          <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(j.completedAt)}</td>
          <td className="max-w-56 truncate px-4 py-2.5 text-xs text-rose-400" title={j.lastError}>
            {j.lastError ?? '—'}
          </td>
        </tr>
      ))}
    </DataTable>
  );
}

/* ---------------- Tombstones ---------------- */

function TombstonesTab({ repo }: { repo: Repository }) {
  const [items, setItems] = useState<ArtifactTombstone[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [restoring, setRestoring] = useState<string | null>(null);

  const load = useCallback(
    async (pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const { data, error: err } = await listRepositoryTombstones({
        path: { repositoryId: repo.id },
        query: { pageSize: 50, pageToken },
      });
      setLoading(false);
      if (err) {
        setError(err);
        return;
      }
      setItems((prev) => (pageToken ? [...prev, ...(data?.items ?? [])] : (data?.items ?? [])));
      setNextToken(data?.nextPageToken);
    },
    [repo.id],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const restore = async (coordinate: string) => {
    setRestoring(coordinate);
    const { error: err } = await restoreRepositoryArtifact({
      path: { repositoryId: repo.id },
      body: { coordinate },
    });
    setRestoring(null);
    if (!err) void load();
  };

  if (error !== null)
    return isNotFound(error) ? <NotEnabled feature="墓碑管理" /> : <ErrorBanner error={error} onRetry={() => load()} />;
  if (loading) return <Loading />;
  if (items.length === 0) return <EmptyState title="暂无墓碑" hint="被删除的制品会保留墓碑记录，可在此恢复" />;

  return (
    <>
      <DataTable columns={['坐标', '摘要', '删除时间', '']}>
        {items.map((t, i) => (
          <tr key={i} className="hover:bg-zinc-800/30">
            <td className="max-w-md truncate px-4 py-2.5 font-mono text-xs text-zinc-200" title={t.coordinate}>
              {t.coordinate}
            </td>
            <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">{shortDigest(t.digest)}</td>
            <td className="whitespace-nowrap px-4 py-2.5 text-xs text-zinc-500">{formatDate(t.tombstonedAt)}</td>
            <td className="px-4 py-2.5 text-right">
              <button
                onClick={() => restore(t.coordinate)}
                disabled={restoring === t.coordinate}
                className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:bg-zinc-800 disabled:opacity-50"
              >
                {restoring === t.coordinate ? '恢复中…' : '恢复'}
              </button>
            </td>
          </tr>
        ))}
      </DataTable>
      <Pagination hasMore={!!nextToken} onMore={() => load(nextToken)} />
    </>
  );
}

/* ---------------- Detail page ---------------- */

function NotEnabled({ feature }: { feature: string }) {
  return (
    <EmptyState
      title={`${feature}功能未启用`}
      hint="当前后端构建尚未挂载此管理端点（返回 404）"
    />
  );
}

function EditRepositoryDialog({ repo, onUpdated }: { repo: Repository; onUpdated: () => void }) {
  const dialog = useDisclosure();
  const [endpoint, setEndpoint] = useState(repo.endpoint ?? '');
  const [hosts, setHosts] = useState((repo.allowedHosts ?? []).join(', '));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const requiresHosts = repo.format === 'raw' || repo.format === 'conan';

  const submit = async () => {
    setSaving(true);
    setError(null);
    const allowedHosts = hosts
      .split(',')
      .map((h) => h.trim())
      .filter(Boolean);
    const { error: err } = await updateRepository({
      path: { repositoryId: repo.id },
      headers: { 'If-Match': repo.version },
      body: { endpoint: endpoint.trim(), allowedHosts },
    });
    setSaving(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    onUpdated();
  };

  return (
    <>
      <button onClick={dialog.show} className={btnSecondary}>
        编辑
      </button>
      <Modal
        open={dialog.open}
        title="编辑代理配置"
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={submit} disabled={saving} className={btnPrimary}>
              {saving ? '保存中…' : '保存'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          <Field
            label="上游地址"
            hint="https 基础地址，修改后立即生效（按请求读取）。"
          >
            <input
              className={inputClass}
              placeholder="https://upstream.example"
              value={endpoint}
              onChange={(e) => setEndpoint(e.target.value)}
            />
          </Field>
          <Field
            label="允许主机"
            hint={requiresHosts ? '逗号分隔，raw / conan 代理必填。' : '逗号分隔；oci / maven 代理可留空。'}
          >
            <input
              className={inputClass}
              placeholder="upstream.example, mirror.example"
              value={hosts}
              onChange={(e) => setHosts(e.target.value)}
            />
          </Field>
          {error ? <ErrorBanner error={error} /> : null}
        </div>
      </Modal>
    </>
  );
}

export function RepositoryDetailPage() {
  const { repositoryId = '' } = useParams();
  const [repo, setRepo] = useState<Repository | null>(null);
  const [caps, setCaps] = useState<RepositoryCapabilities | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [tab, setTab] = useState<Tab>('artifacts');

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRepository({ path: { repositoryId } });
    if (err) {
      setError(err);
      return;
    }
    setRepo(data ?? null);
    const capsRes = await getRepositoryCapabilities({ path: { repositoryId } });
    if (!capsRes.error) setCaps(capsRes.data ?? null);
  }, [repositoryId]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error !== null) {
    return (
      <div>
        <PageHeader title="仓库详情" />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  }
  if (!repo) return <Loading />;

  return (
    <div>
      <div className="mb-1 text-xs text-zinc-500">
        <Link to="/repositories" className="hover:text-cyan-300">
          仓库
        </Link>
        <span className="mx-1.5">/</span>
        <span className="text-zinc-400">{repo.name}</span>
      </div>
      <PageHeader
        title={repo.name}
        description={`ID: ${repo.id} · 版本 v${repo.version}`}
        actions={
          <div className="flex items-center gap-2">
            {repo.type === 'proxy' && <EditRepositoryDialog repo={repo} onUpdated={load} />}
            <FormatBadge format={repo.format} />
            <StateBadge state={repo.state} />
          </div>
        }
      />
      {caps && (
        <div className="mb-5 flex flex-wrap items-center gap-1.5 text-xs text-zinc-500">
          <span className="mr-1">支持的操作:</span>
          {caps.operations.map((op) => (
            <Badge key={op} tone="zinc">
              {op}
            </Badge>
          ))}
        </div>
      )}
      <div className="mb-5 flex gap-1 overflow-x-auto border-b border-zinc-800">
        {TABS.filter((t) => !t.formats || t.formats.includes(repo.format)).map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`whitespace-nowrap border-b-2 px-4 py-2 text-sm transition-colors ${
              tab === t.key
                ? 'border-cyan-400 font-medium text-cyan-300'
                : 'border-transparent text-zinc-500 hover:text-zinc-300'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <Card className="p-4">
        {tab === 'artifacts' && <ArtifactsTab repo={repo} />}
        {tab === 'publish' && repo.format === 'maven' && (
          <MavenPublishWizard repositoryId={repo.id} onPublished={() => setTab('artifacts')} />
        )}
        {tab === 'grants' && <GrantsTab repo={repo} />}
        {tab === 'retention' && <RetentionTab repo={repo} />}
        {tab === 'capacity' && <CapacityTab repo={repo} />}
        {tab === 'distribute' && <DistributeTab repo={repo} />}
        {tab === 'jobs' && <JobsTab repo={repo} />}
        {tab === 'tombstones' && <TombstonesTab repo={repo} />}
      </Card>
    </div>
  );
}
