import { useEffect, useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Alert, Button, Popconfirm } from "antd";
import {
  deleteArtifact,
  deleteConanPackageRevision,
  listArtifacts,
  listConanPackageIds,
  listConanPackageRevisions,
  listConanRecipeRevisions,
  listMavenCoordinates,
} from "../client";
import { ArtifactDetailView, VersionList } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { useAuth } from "../lib/auth";
import { mavenGA, mavenVersion } from "../lib/usage";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { rawResourceURL } from "../lib/rawPath";

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
  const [versions, setVersions] = useState<
    {
      label: string;
      hint?: string;
      coordinate: string;
      publisher?: string;
      createdAt?: string;
      buildNumber?: number;
      digest?: string;
    }[]
  >([]);
  // coordinate 是 GA（2 段）时直接用它，否则取 GA 部分
  const ga =
    meta.coordinate.split(":").length === 2
      ? meta.coordinate
      : mavenGA(meta.coordinate);
  const [selected, setSelected] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  useEffect(() => {
    if (!ga) return;
    let cancelled = false;
    void (async () => {
      const coordinates: {
        coordinate: string;
        buildNumber?: number;
        publisher?: string;
        createdAt?: string;
        digest?: string;
      }[] = [];
      let pageToken: string | undefined;
      do {
        const { data, error } = await listMavenCoordinates({
          path: { repositoryId: repoId },
          query: { q: ga, pageSize: 200, pageToken },
        });
        if (cancelled || error || !data) return;
        coordinates.push(...data.items);
        pageToken = data.nextPageToken;
      } while (pageToken);

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
      const requested = uniq.find(
        (version) => version.label === requestedLabel,
      );
      if (cancelled) return;
      setVersions(uniq);
      setSelected((prev) => requested?.label ?? prev ?? latest?.label ?? null);
    })();
    return () => {
      cancelled = true;
    };
  }, [locale, repoId, ga, meta.buildNumber, meta.coordinate]);

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
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-800 px-3 py-2">
            <div className="min-w-0">
              <div className="text-xs font-medium text-zinc-200">
                {text("当前版本", "Current version")}
              </div>
              <div
                className="mt-0.5 truncate font-mono text-xs text-zinc-500"
                title={effectiveMeta.coordinate}
              >
                {effectiveMeta.coordinate}
              </div>
              {deleteError && (
                <div className="mt-1 text-xs text-rose-300">{deleteError}</div>
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

// Conan 制品详情：使用方法 + revisions 列表
export function ConanArtifactDetail({
  repoId,
  repoName,
  meta,
  managed,
  canDelete,
  onDeleted,
  canQuarantine = false,
}: {
  repoId: string;
  repoName: string;
  meta: ArtifactMeta;
  managed: boolean;
  canDelete: boolean;
  onDeleted?: () => void;
  canQuarantine?: boolean;
}) {
  const { locale, text } = usePreferences();
  const [recipeRevisions, setRecipeRevisions] = useState<
    { revision: string; digest: string; createdAt: string }[]
  >([]);
  const [selectedRecipe, setSelectedRecipe] = useState("");
  const [packageRevisions, setPackageRevisions] = useState<
    {
      recipeRevision: string;
      packageId: string;
      revision: string;
      digest: string;
      createdAt: string;
    }[]
  >([]);
  const [selectedPackageKey, setSelectedPackageKey] = useState("");
  const [deleting, setDeleting] = useState("");
  const [error, setError] = useState("");

  const reference = meta.coordinate.trim().replace(/\/$/, "").split("#", 1)[0];

  const requestErrorMessage = (label: string, requestError: unknown) => {
    if (!requestError) return label;
    const detail =
      typeof requestError === "object" && requestError !== null
        ? "detail" in requestError
          ? String(requestError.detail)
          : "message" in requestError
            ? String(requestError.message)
            : ""
        : String(requestError);
    return detail ? `${label}: ${detail}` : label;
  };

  const loadRecipeRevisions = async () => {
    const { data, error: requestError } = await listConanRecipeRevisions({
      path: { repositoryId: repoId },
      query: { reference },
    });
    if (requestError || !data) {
      setError(
        requestErrorMessage(
          text(
            "读取 Conan recipe revisions 失败",
            "Failed to load Conan recipe revisions",
          ),
          requestError,
        ),
      );
      return;
    }
    const items = data.items.filter((item) => item.state === "visible");
    setRecipeRevisions(items);
    setSelectedRecipe((current) => current || items[0]?.revision || "");
  };

  useEffect(() => {
    if (!managed) return;
    void loadRecipeRevisions();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoId, reference, managed]);

  useEffect(() => {
    if (!managed || !selectedRecipe) {
      setPackageRevisions([]);
      setSelectedPackageKey("");
      return;
    }
    let cancelled = false;
    setPackageRevisions([]);
    setSelectedPackageKey("");
    setError("");
    void (async () => {
      const { data: ids, error: idsError } = await listConanPackageIds({
        path: { repositoryId: repoId },
        query: { reference, recipeRevision: selectedRecipe },
      });
      if (cancelled) return;
      if (idsError || !ids) {
        setError(
          requestErrorMessage(
            text(
              "读取 Conan package IDs 失败",
              "Failed to load Conan package IDs",
            ),
            idsError,
          ),
        );
        return;
      }
      const pages = await Promise.all(
        ids.items.map((packageId) =>
          listConanPackageRevisions({
            path: { repositoryId: repoId },
            query: { reference, recipeRevision: selectedRecipe, packageId },
          }),
        ),
      );
      if (cancelled) return;
      if (pages.some((page) => page.error)) {
        setError(
          requestErrorMessage(
            text(
              "读取 Conan package revisions 失败",
              "Failed to load Conan package revisions",
            ),
            pages.find((page) => page.error)?.error,
          ),
        );
        return;
      }
      const visible = pages
        .flatMap((page) => page.data?.items ?? [])
        .filter((item) => item.state === "visible");
      setPackageRevisions(visible);
    })();
    return () => {
      cancelled = true;
    };
  }, [repoId, reference, managed, selectedRecipe, text]);

  const intelligenceDigest = recipeRevisions.find(
    (item) => item.revision === selectedRecipe,
  )?.digest;
  const selectedPackage = packageRevisions.find(
    (item) =>
      `${item.recipeRevision}:${item.packageId}:${item.revision}` ===
      selectedPackageKey,
  );
  const detailMeta: ArtifactMeta = selectedPackage
    ? {
        ...meta,
        coordinate: `${reference}#${selectedPackage.recipeRevision}/${selectedPackage.packageId}#${selectedPackage.revision}`,
        digest: selectedPackage.digest,
        createdAt: selectedPackage.createdAt,
      }
    : selectedRecipe && intelligenceDigest
      ? {
          ...meta,
          coordinate: `${reference}#${selectedRecipe}`,
          digest: intelligenceDigest,
          createdAt: recipeRevisions.find(
            (item) => item.revision === selectedRecipe,
          )?.createdAt,
        }
      : meta;

  const deletePackage = async (item: {
    recipeRevision: string;
    packageId: string;
    revision: string;
  }) => {
    setDeleting(
      `package:${item.recipeRevision}:${item.packageId}:${item.revision}`,
    );
    setError("");
    const { error: requestError } = await deleteConanPackageRevision({
      path: { repositoryId: repoId, revision: item.revision },
      query: {
        reference,
        recipeRevision: item.recipeRevision,
        packageId: item.packageId,
      },
    });
    if (requestError)
      setError(
        requestErrorMessage(
          text(
            "删除 Conan package revision 失败",
            "Failed to delete Conan package revision",
          ),
          requestError,
        ),
      );
    else {
      setPackageRevisions((items) =>
        items.filter(
          (candidate) =>
            candidate.recipeRevision !== item.recipeRevision ||
            candidate.packageId !== item.packageId ||
            candidate.revision !== item.revision,
        ),
      );
      setSelectedPackageKey((current) =>
        current === `${item.recipeRevision}:${item.packageId}:${item.revision}`
          ? ""
          : current,
      );
      onDeleted?.();
    }
    setDeleting("");
  };

  return (
    <ArtifactDetailView
      format="conan"
      repositoryId={repoId}
      repoName={repoName}
      meta={detailMeta}
      // Conan promotion and replication publish a recipe revision as one
      // unit. A package revision is not an independently enforceable
      // distribution anchor, so do not offer a misleading transition there.
      canQuarantine={canQuarantine && !selectedPackage}
      showQuarantine={!selectedPackage}
      versions={
        managed ? (
          <div className="space-y-4">
            {error && <Alert type="error" showIcon title={error} />}
            <VersionList
              title={text("Recipe revisions", "Recipe revisions")}
              items={recipeRevisions.map((item) => ({
                label: item.revision,
                hint: `${item.digest.slice(0, 18)} · ${formatDate(item.createdAt, locale)}`,
              }))}
              current={selectedRecipe}
              onSelect={(revision) => {
                setSelectedRecipe(revision);
                setSelectedPackageKey("");
              }}
            />
            {packageRevisions.length > 0 && (
              <div className="overflow-hidden rounded-lg border border-zinc-800">
                <div className="border-b border-zinc-800 px-3 py-2 text-sm font-medium text-zinc-200">
                  {text("Package revisions", "Package revisions")}
                </div>
                {packageRevisions.map((item) => {
                  const packageKey = `${item.recipeRevision}:${item.packageId}:${item.revision}`;
                  const deletingItem = deleting === `package:${packageKey}`;
                  const selected = selectedPackageKey === packageKey;
                  return (
                    <div
                      key={packageKey}
                      className={`flex items-center justify-between gap-3 border-b border-zinc-800/60 px-3 py-2 last:border-0 ${selected ? "bg-cyan-950/20" : ""}`}
                    >
                      <div className="min-w-0">
                        <div className="font-mono text-xs text-zinc-200">
                          {item.packageId}#{item.revision}
                        </div>
                        <div className="mt-0.5 text-xs text-zinc-500">
                          {item.digest.slice(0, 18)} ·{" "}
                          {formatDate(item.createdAt, locale)}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        <Button
                          size="small"
                          type={selected ? "primary" : "text"}
                          aria-pressed={selected}
                          onClick={() =>
                            setSelectedPackageKey(selected ? "" : packageKey)
                          }
                        >
                          {selected
                            ? text("查看 Recipe", "View recipe")
                            : text("查看详情", "Inspect")}
                        </Button>
                        {canDelete && (
                          <Popconfirm
                            title={text(
                              "删除 package revision？",
                              "Delete this package revision?",
                            )}
                            description={text(
                              "删除后该二进制包 revision 将不可见。",
                              "This binary package revision will no longer be visible.",
                            )}
                            okText={text("删除", "Delete")}
                            cancelText={text("取消", "Cancel")}
                            okButtonProps={{ danger: true }}
                            onConfirm={() => void deletePackage(item)}
                          >
                            <Button
                              danger
                              size="small"
                              icon={<DeleteOutlined />}
                              loading={deletingItem}
                            >
                              {text("删除", "Delete")}
                            </Button>
                          </Popconfirm>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        ) : undefined
      }
    />
  );
}

// Raw 制品详情：使用方法 + 元信息
export function RawArtifactDetail({
  repositoryId,
  repoName,
  meta,
  onDeleted,
  canQuarantine = false,
}: {
  repositoryId?: string;
  repoName: string;
  meta: ArtifactMeta;
  onDeleted?: () => void;
  canQuarantine?: boolean;
}) {
  const { text } = usePreferences();
  const { token } = useAuth();
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const deleteCurrent = async () => {
    setDeleting(true);
    setDeleteError("");
    try {
      const response = await fetch(rawResourceURL(repoName, meta.coordinate), {
        method: "DELETE",
        credentials: "include",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      });
      if (!response.ok && response.status !== 204)
        throw new Error(
          `${response.status}: ${(await response.text()).slice(0, 120)}`,
        );
      onDeleted?.();
    } catch (error) {
      setDeleteError(
        error instanceof Error
          ? error.message
          : text("删除 Raw 文件失败", "Failed to delete Raw file"),
      );
    } finally {
      setDeleting(false);
    }
  };
  return (
    <div className="space-y-4">
      <ArtifactDetailView
        repositoryId={repositoryId}
        format="raw"
        repoName={repoName}
        meta={meta}
        canQuarantine={canQuarantine}
      />
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-800 px-3 py-2">
        <div>
          <div className="text-xs font-medium text-zinc-200">
            {text("Raw 文件", "Raw file")}
          </div>
          {deleteError && (
            <div className="mt-1 text-xs text-rose-300">{deleteError}</div>
          )}
        </div>
        <Popconfirm
          title={text("删除 Raw 文件？", "Delete this Raw file?")}
          description={text(
            "删除后该路径将不可见。",
            "This path will no longer be visible.",
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
            {text("删除文件", "Delete file")}
          </Button>
        </Popconfirm>
      </div>
    </div>
  );
}
