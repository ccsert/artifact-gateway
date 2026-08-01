import { useEffect, useState } from 'react';
import { deleteArtifact, listArtifacts, listMavenCoordinates, listConanReferences } from '../client';
import { ArtifactDetailView, VersionList } from './ArtifactDetail';
import type { ArtifactMeta } from './ArtifactDetail';
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
            <button
              onClick={deleteCurrent}
              disabled={deleting}
              className="rounded border border-rose-500/40 px-2.5 py-1 text-xs text-rose-300 hover:bg-rose-500/10 disabled:opacity-50"
            >
              {deleting ? '删除中…' : '删除当前版本'}
            </button>
          </div>
          <VersionList title={`发布版本（${ga ?? ''}）`} items={releases} current={currentVersion} onSelect={setSelected} />
          <VersionList title="快照构建" items={snapshots} current={currentVersion} onSelect={setSelected} />
        </div>
      }
    />
  );
}

// Conan 制品详情：使用方法 + revisions 列表
export function ConanArtifactDetail({ repoId, repoName, meta }: { repoId: string; repoName: string; meta: ArtifactMeta }) {
  const [revisions, setRevisions] = useState<{ label: string; hint?: string }[]>([]);

  useEffect(() => {
    listConanReferences({ path: { repositoryId: repoId }, query: { pageSize: 100 } }).then(({ data }) => {
      // 同 reference 的 revisions 在协议端点，这里先列出同族引用
      const rs = (data?.items ?? [])
        .filter((x) => x.reference === meta.coordinate)
        .map((x) => ({ label: x.reference, hint: x.publisher }));
      setRevisions(rs);
    });
  }, [repoId, meta.coordinate]);

  return (
    <ArtifactDetailView
      format="conan"
      repoName={repoName}
      meta={meta}
      versions={revisions.length > 0 ? <VersionList title="引用" items={revisions} current={meta.coordinate} /> : undefined}
    />
  );
}

// Raw 制品详情：使用方法 + 元信息
export function RawArtifactDetail({ repoName, meta }: { repoName: string; meta: ArtifactMeta }) {
  return <ArtifactDetailView format="raw" repoName={repoName} meta={meta} />;
}
