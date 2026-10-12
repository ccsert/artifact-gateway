import { useMemo, useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm } from "antd";
import { deleteArtifact, listArtifacts } from "../../client";
import { ArtifactDetailView, VersionList } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { mavenGA, mavenVersion } from "../../lib/usage";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { useMavenCoordinates } from "./artifactDetailQueries";

// Maven 制品详情：使用方法 + 按发布版本、快照构建分开的版本列表。
// meta.coordinate 可以是完整 GAV（com.example:hello:1.0.0）或 GA（com.example:hello）。
export function MavenArtifactDetail({
  repoId,
  repoName,
  meta,
  onDeleted,
  canQuarantine = false,
}: {
  repoId: string;
  repoName: string;
  meta: ArtifactMeta;
  onDeleted?: () => void;
  canQuarantine?: boolean;
}) {
  const { locale, text } = usePreferences();
  // coordinate 是 GA（2 段）时直接用它，否则取 GA 部分
  const ga =
    meta.coordinate.split(":").length === 2
      ? meta.coordinate
      : mavenGA(meta.coordinate);
  // A failed read leaves only the opened coordinate, as before.
  const coordinatesQuery = useMavenCoordinates(repoId, ga);
  const coordinates = coordinatesQuery.data;
  const { versions, requested, latest } = useMemo(() => {
    if (!coordinates)
      return { versions: [], requested: undefined, latest: undefined };
    const vs = coordinates
      .map((x) => {
        const version = mavenVersion(x.coordinate) ?? x.coordinate;
        const build = x.buildNumber ?? 0;
        // SNAPSHOT 的多个构建：label 带构建号区分（release build 0 不带）
        const label = build > 0 ? `${version} #${build}` : version;
        const hint = [
          x.coordinate,
          build > 0 ? `build ${build}` : "",
          formatDate(x.createdAt, locale),
        ]
          .filter(Boolean)
          .join(" · ");
        return {
          label,
          hint,
          coordinate: x.coordinate,
          publisher: x.publisher,
          createdAt: x.createdAt,
          buildNumber: x.buildNumber,
          digest: x.digest,
        };
      })
      .filter((v) => v.label);
    const uniq = Array.from(new Map(vs.map((v) => [v.label, v])).values());
    const requestedVersion = mavenVersion(meta.coordinate);
    const requestedLabel = requestedVersion
      ? `${requestedVersion}${meta.buildNumber && meta.buildNumber > 0 ? ` #${meta.buildNumber}` : ""}`
      : "";
    const latest = [...uniq].sort((a, b) =>
      (b.createdAt ?? "").localeCompare(a.createdAt ?? ""),
    )[0];
    const requested = uniq.find((version) => version.label === requestedLabel);
    return { versions: uniq, requested, latest };
  }, [coordinates, locale, meta.buildNumber, meta.coordinate]);
  // The reader's choice for this artifact, otherwise the linked version,
  // otherwise the newest.
  const scope = `${repoId}:${meta.coordinate}:${meta.buildNumber ?? 0}`;
  const [choice, setChoice] = useState<{
    scope: string;
    label: string;
  } | null>(null);
  const selected =
    (choice?.scope === scope ? choice.label : null) ??
    requested?.label ??
    latest?.label ??
    null;
  const setSelected = (label: string) => setChoice({ scope, label });
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  // 当前选中的版本（用 label 标识，SNAPSHOT 多构建 label 唯一）；coordinate 用于使用方法
  const selectedMeta = versions.find((v) => v.label === selected);
  const effectiveMeta: ArtifactMeta = selectedMeta
    ? {
        ...meta,
        coordinate: selectedMeta.coordinate,
        publisher: selectedMeta.publisher ?? meta.publisher,
        createdAt: selectedMeta.createdAt ?? meta.createdAt,
        buildNumber: selectedMeta.buildNumber,
        digest: selectedMeta.digest ?? meta.digest,
      }
    : meta;
  const currentVersion =
    selectedMeta?.label ?? mavenVersion(meta.coordinate) ?? undefined;
  const releases = versions.filter(
    (version) => !version.label.includes("-SNAPSHOT"),
  );
  const snapshots = versions.filter((version) =>
    version.label.includes("-SNAPSHOT"),
  );

  const deleteCurrent = async () => {
    setDeleting(true);
    setDeleteError("");
    try {
      const { data, error } = await listArtifacts({
        path: { repositoryId: repoId },
      });
      if (error) throw new Error("读取 Maven artifact 列表失败");
      const artifact = (data?.items ?? []).find(
        (item) =>
          item.coordinate === effectiveMeta.coordinate &&
          item.state === "visible",
      );
      if (!artifact)
        throw new Error("当前 Maven 版本未找到可删除的 artifact id");
      const deleted = await deleteArtifact({
        path: { repositoryId: repoId, artifactId: artifact.id },
      });
      if (deleted.error) throw new Error("删除 Maven artifact 失败");
      onDeleted?.();
    } catch (error) {
      setDeleteError(
        error instanceof Error
          ? error.message
          : text("删除失败", "Delete failed"),
      );
    } finally {
      setDeleting(false);
    }
  };

  return (
    <ArtifactDetailView
      format="maven"
      repositoryId={repoId}
      repoName={repoName}
      meta={effectiveMeta}
      canQuarantine={canQuarantine}

      versions={
        <div className="space-y-5">
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2">
            <div className="min-w-0">
              <div className="text-xs font-medium text-fg">
                {text("当前版本", "Current version")}
              </div>
              <div
                className="mt-0.5 truncate font-mono text-xs text-fg-tertiary"
                title={effectiveMeta.coordinate}
              >
                {effectiveMeta.coordinate}
              </div>
              {deleteError && (
                <div className="mt-1 text-xs text-[var(--ag-status-danger)]">
                  {deleteError}
                </div>
              )}
            </div>
            <Popconfirm
              title={text(
                "删除当前 Maven 版本？",
                "Delete this Maven version?",
              )}
              description={text(
                "删除后该版本及其可恢复引用将不可见。",
                "This version and its recoverable references will no longer be visible.",
              )}
              okText={text("删除", "Delete")}
              cancelText={text("取消", "Cancel")}
              okButtonProps={{ danger: true }}
              onConfirm={() => void deleteCurrent()}
            >
              <Button
                danger
                size="small"
                icon={<DeleteOutlined />}
                loading={deleting}
              >
                {text("删除当前版本", "Delete current version")}
              </Button>
            </Popconfirm>
          </div>
          <VersionList
            title={`${text("发布版本", "Release versions")} (${ga ?? ""})`}
            items={releases}
            current={currentVersion}
            onSelect={setSelected}
          />
          <VersionList
            title={text("快照构建", "Snapshot builds")}
            items={snapshots}
            current={currentVersion}
            onSelect={setSelected}
          />
        </div>
      }
    />
  );
}
