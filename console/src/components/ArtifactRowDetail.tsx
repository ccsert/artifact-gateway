import { useEffect, useState } from 'react';
import { DeleteOutlined } from '@ant-design/icons';
import { Alert, Button, Popconfirm } from 'antd';
import { deleteArtifact, deleteConanPackageRevision, listArtifacts, listConanPackageIds, listConanPackageRevisions, listConanRecipeRevisions, listMavenCoordinates } from '../client';
import { ArtifactDetailView, VersionList } from './ArtifactDetail';
import type { ArtifactMeta } from './ArtifactDetail';
import { useAuth } from '../lib/auth';
import { mavenGA, mavenVersion } from '../lib/usage';
import { formatDate } from '../lib/format';

// Maven 制品详情：使用方法 + 按发布版本、快照构建分开的版本列表。
// meta.coordinate 可以是完整 GAV（com.example:hello:1.0.0）或 GA（com.example:hello）。
export function MavenArtifactDetail({ repoId, repoName, meta, onDeleted }: { repoId: string; repoName: string; meta: ArtifactMeta; onDeleted?: () => void }) {
  const [versions, setVersions] = useState<{ label: string; hint?: string; coordinate: string; publisher?: string; createdAt?: string }[]>([]);
  // coordinate 是 GA（2 段）时直接用它，否则取 GA 部分
  const ga = meta.coordinate.split(':').length === 2 ? meta.coordinate : mavenGA(meta.coordinate);
  const [selected, setSelected] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  useEffect(() => {
    if (!ga) return;
    // 用 group:artifact 前缀搜同族所有版本
    listMavenCoordinates({ path: { repositoryId: repoId }, query: { q: ga, pageSize: 100 } }).then(({ data }) => {
      const vs = (data?.items ?? [])
        .map((x) => {
          const version = mavenVersion(x.coordinate) ?? x.coordinate;
          const build = x.buildNumber ?? 0;
          // SNAPSHOT 的多个构建：label 带构建号区分（release build 0 不带）
          const label = build > 0 ? `${version} #${build}` : version;
          const hint = [x.coordinate, build > 0 ? `build ${build}` : '', formatDate(x.createdAt)].filter(Boolean).join(' · ');
          return {
            label,
            hint,
            coordinate: x.coordinate,
            publisher: x.publisher,
            createdAt: x.createdAt,
          };
        })
        .filter((v) => v.label);
      const uniq = Array.from(new Map(vs.map((v) => [v.label, v])).values());
      setVersions(uniq);
      // 默认选最新（createdAt 最大）
      const latest = [...uniq].sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))[0];
      setSelected((prev) => prev ?? latest?.label ?? null);
    });
  }, [repoId, ga]);

  // 当前选中的版本（用 label 标识，SNAPSHOT 多构建 label 唯一）；coordinate 用于使用方法
  const selectedMeta = versions.find((v) => v.label === selected);
  const effectiveMeta: ArtifactMeta = selectedMeta
    ? { ...meta, coordinate: selectedMeta.coordinate, publisher: selectedMeta.publisher ?? meta.publisher, createdAt: selectedMeta.createdAt ?? meta.createdAt }
    : meta;
  const currentVersion = selectedMeta?.label ?? mavenVersion(meta.coordinate) ?? undefined;
  const releases = versions.filter((version) => !version.label.includes('-SNAPSHOT'));
  const snapshots = versions.filter((version) => version.label.includes('-SNAPSHOT'));

  const deleteCurrent = async () => {
    setDeleting(true);
    setDeleteError('');
    try {
      const { data, error } = await listArtifacts({ path: { repositoryId: repoId } });
      if (error) throw new Error('读取 Maven artifact 列表失败');
      const artifact = (data?.items ?? []).find((item) => item.coordinate === effectiveMeta.coordinate && item.state === 'visible');
      if (!artifact) throw new Error('当前 Maven 版本未找到可删除的 artifact id');
      const deleted = await deleteArtifact({ path: { repositoryId: repoId, artifactId: artifact.id } });
      if (deleted.error) throw new Error('删除 Maven artifact 失败');
      onDeleted?.();
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : '删除失败');
    } finally {
      setDeleting(false);
    }
  };

  return (
    <ArtifactDetailView
      format="maven"
      repoName={repoName}
      meta={effectiveMeta}

      versions={
        <div className="space-y-5">
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-800 px-3 py-2">
            <div className="min-w-0">
              <div className="text-xs font-medium text-zinc-200">当前版本</div>
              <div className="mt-0.5 truncate font-mono text-xs text-zinc-500" title={effectiveMeta.coordinate}>{effectiveMeta.coordinate}</div>
              {deleteError && <div className="mt-1 text-xs text-rose-300">{deleteError}</div>}
            </div>
            <Popconfirm
              title="删除当前 Maven 版本？"
              description="删除后该版本及其可恢复引用将不可见。"
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={() => void deleteCurrent()}
            >
              <Button
                danger
                size="small"
                icon={<DeleteOutlined />}
                loading={deleting}
              >
                删除当前版本
              </Button>
            </Popconfirm>
          </div>
          <VersionList title={`发布版本（${ga ?? ''}）`} items={releases} current={currentVersion} onSelect={setSelected} />
          <VersionList title="快照构建" items={snapshots} current={currentVersion} onSelect={setSelected} />
        </div>
      }
    />
  );
}

// Conan 制品详情：使用方法 + revisions 列表
export function ConanArtifactDetail({ repoId, repoName, meta, managed, canDelete, onDeleted }: { repoId: string; repoName: string; meta: ArtifactMeta; managed: boolean; canDelete: boolean; onDeleted?: () => void }) {
  const [recipeRevisions, setRecipeRevisions] = useState<{ revision: string; digest: string; createdAt: string }[]>([]);
  const [selectedRecipe, setSelectedRecipe] = useState('');
  const [packageRevisions, setPackageRevisions] = useState<{ recipeRevision: string; packageId: string; revision: string; digest: string; createdAt: string }[]>([]);
  const [deleting, setDeleting] = useState('');
  const [error, setError] = useState('');

  const reference = meta.coordinate.trim().replace(/\/$/, '').split('#', 1)[0];

  const requestErrorMessage = (label: string, requestError: unknown) => {
    if (!requestError) return label;
    const detail = typeof requestError === 'object' && requestError !== null
      ? ('detail' in requestError ? String(requestError.detail) : 'message' in requestError ? String(requestError.message) : '')
      : String(requestError);
    return detail ? `${label}: ${detail}` : label;
  };

  const loadRecipeRevisions = async () => {
    const { data, error: requestError } = await listConanRecipeRevisions({ path: { repositoryId: repoId }, query: { reference } });
    if (requestError || !data) {
      setError(requestErrorMessage('读取 Conan recipe revisions 失败', requestError));
      return;
    }
    const items = data.items.filter((item) => item.state === 'visible');
    setRecipeRevisions(items);
    setSelectedRecipe((current) => current || items[0]?.revision || '');
  };

  useEffect(() => {
    if (!managed) return;
    void loadRecipeRevisions();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoId, reference, managed]);

  useEffect(() => {
    if (!managed || !selectedRecipe) {
      setPackageRevisions([]);
      return;
    }
    let cancelled = false;
    setPackageRevisions([]);
    setError('');
    void (async () => {
      const { data: ids, error: idsError } = await listConanPackageIds({ path: { repositoryId: repoId }, query: { reference, recipeRevision: selectedRecipe } });
      if (cancelled) return;
      if (idsError || !ids) {
        setError(requestErrorMessage('读取 Conan package IDs 失败', idsError));
        return;
      }
      const pages = await Promise.all(ids.items.map((packageId) => listConanPackageRevisions({ path: { repositoryId: repoId }, query: { reference, recipeRevision: selectedRecipe, packageId } })));
      if (cancelled) return;
      if (pages.some((page) => page.error)) {
        setError(requestErrorMessage('读取 Conan package revisions 失败', pages.find((page) => page.error)?.error));
        return;
      }
      setPackageRevisions(pages.flatMap((page) => page.data?.items ?? []).filter((item) => item.state === 'visible'));
    })();
    return () => { cancelled = true; };
  }, [repoId, reference, managed, selectedRecipe]);

  const deletePackage = async (item: { recipeRevision: string; packageId: string; revision: string }) => {
    setDeleting(`package:${item.recipeRevision}:${item.packageId}:${item.revision}`);
    setError('');
    const { error: requestError } = await deleteConanPackageRevision({ path: { repositoryId: repoId, revision: item.revision }, query: { reference, recipeRevision: item.recipeRevision, packageId: item.packageId } });
    if (requestError) setError(requestErrorMessage('删除 Conan package revision 失败', requestError));
    else {
      setPackageRevisions((items) => items.filter((candidate) => candidate.recipeRevision !== item.recipeRevision || candidate.packageId !== item.packageId || candidate.revision !== item.revision));
      onDeleted?.();
    }
    setDeleting('');
  };

  return (
    <ArtifactDetailView
      format="conan"
      repoName={repoName}
      meta={meta}
      versions={managed ? (
        <div className="space-y-4">
          {error && <Alert type="error" showIcon title={error} />}
          <VersionList title="Recipe revisions" items={recipeRevisions.map((item) => ({ label: item.revision, hint: `${item.digest.slice(0, 18)} · ${formatDate(item.createdAt)}` }))} current={selectedRecipe} onSelect={setSelectedRecipe} />
          {packageRevisions.length > 0 && (
            <div className="overflow-hidden rounded-lg border border-zinc-800">
              <div className="border-b border-zinc-800 px-3 py-2 text-sm font-medium text-zinc-200">Package revisions</div>
              {packageRevisions.map((item) => {
                const deletingItem = deleting === `package:${item.recipeRevision}:${item.packageId}:${item.revision}`;
                return (
                  <div key={`${item.recipeRevision}:${item.packageId}:${item.revision}`} className="flex items-center justify-between gap-3 border-b border-zinc-800/60 px-3 py-2 last:border-0">
                    <div className="min-w-0">
                      <div className="font-mono text-xs text-zinc-200">{item.packageId}#{item.revision}</div>
                      <div className="mt-0.5 text-[11px] text-zinc-500">{item.digest.slice(0, 18)} · {formatDate(item.createdAt)}</div>
                    </div>
                    {canDelete && (
                      <Popconfirm
                        title="删除 package revision？"
                        description="删除后该二进制包 revision 将不可见。"
                        okText="删除"
                        cancelText="取消"
                        okButtonProps={{ danger: true }}
                        onConfirm={() => void deletePackage(item)}
                      >
                        <Button danger size="small" icon={<DeleteOutlined />} loading={deletingItem}>
                          删除
                        </Button>
                      </Popconfirm>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      ) : undefined}
    />
  );
}

// Raw 制品详情：使用方法 + 元信息
export function RawArtifactDetail({ repoName, meta, onDeleted }: { repoName: string; meta: ArtifactMeta; onDeleted?: () => void }) {
  const { token } = useAuth();
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const deleteCurrent = async () => {
    setDeleting(true);
    setDeleteError('');
    try {
      const response = await fetch(`/raw/${encodeURIComponent(repoName)}/${meta.coordinate.split('/').map(encodeURIComponent).join('/')}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!response.ok && response.status !== 204) throw new Error(`${response.status}: ${(await response.text()).slice(0, 120)}`);
      onDeleted?.();
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : '删除 Raw 文件失败');
    } finally {
      setDeleting(false);
    }
  };
  return (
    <div className="space-y-4">
      <ArtifactDetailView format="raw" repoName={repoName} meta={meta} />
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-800 px-3 py-2">
        <div>
          <div className="text-xs font-medium text-zinc-200">Raw 文件</div>
          <div className="mt-0.5 font-mono text-xs text-zinc-500">{meta.coordinate}</div>
          {deleteError && <div className="mt-1 text-xs text-rose-300">{deleteError}</div>}
        </div>
        <Popconfirm
          title="删除 Raw 文件？"
          description="删除后该路径将不可见。"
          okText="删除"
          cancelText="取消"
          okButtonProps={{ danger: true }}
          onConfirm={() => void deleteCurrent()}
        >
          <Button danger size="small" icon={<DeleteOutlined />} loading={deleting}>
            删除文件
          </Button>
        </Popconfirm>
      </div>
    </div>
  );
}
