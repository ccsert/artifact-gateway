import { useEffect, useState } from 'react';
import { listMavenCoordinates, listConanReferences } from '../client';
import { ArtifactDetailView, VersionList } from './ArtifactDetail';
import type { ArtifactMeta } from './ArtifactDetail';
import { mavenGA, mavenVersion } from '../lib/usage';
import { formatDate } from '../lib/format';

// Maven 制品详情：使用方法 + 同 group:artifact 的版本列表
export function MavenArtifactDetail({ repoId, repoName, meta }: { repoId: string; repoName: string; meta: ArtifactMeta }) {
  const [versions, setVersions] = useState<{ label: string; hint?: string }[]>([]);
  const ga = mavenGA(meta.coordinate);
  const currentVersion = mavenVersion(meta.coordinate);

  useEffect(() => {
    if (!ga) return;
    // 用 group:artifact 前缀搜同族所有版本
    listMavenCoordinates({ path: { repositoryId: repoId }, query: { q: ga, pageSize: 100 } }).then(({ data }) => {
      const vs = (data?.items ?? [])
        .map((x) => ({ label: mavenVersion(x.coordinate) ?? x.coordinate, hint: formatDate(x.createdAt) }))
        .filter((v) => v.label);
      // 去重并按版本排序（新→旧）
      const uniq = Array.from(new Map(vs.map((v) => [v.label, v])).values());
      setVersions(uniq.reverse());
    });
  }, [repoId, ga]);

  return (
    <ArtifactDetailView
      format="maven"
      repoName={repoName}
      meta={meta}
      versions={<VersionList title={`版本（${ga ?? ''}）`} items={versions} current={currentVersion ?? undefined} />}
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
