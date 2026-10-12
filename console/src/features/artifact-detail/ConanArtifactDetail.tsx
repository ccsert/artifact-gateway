import { useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm } from "antd";
import { ErrorBanner } from "../../components/ui/Feedback";
import { deleteConanPackageRevision } from "../../client";
import { ArtifactDetailView, VersionList } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { useQueryClient } from "@tanstack/react-query";
import {
  conanPackageRevisionsKey,
  useConanPackageRevisions,
  useConanRecipeRevisions,
} from "./artifactDetailQueries";

// Maven 制品详情：使用方法 + 按发布版本、快照构建分开的版本列表。
// meta.coordinate 可以是完整 GAV（com.example:hello:1.0.0）或 GA（com.example:hello）。
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
  const reference = meta.coordinate.trim().replace(/\/$/, "").split("#", 1)[0];
  const queryClient = useQueryClient();
  const recipesQuery = useConanRecipeRevisions(repoId, reference, managed);
  const recipeRevisions = recipesQuery.data ?? [];
  const [chosenRecipe, setSelectedRecipe] = useState("");
  // The chosen revision, otherwise the newest visible one.
  const selectedRecipe = chosenRecipe || recipeRevisions[0]?.revision || "";
  const packagesQuery = useConanPackageRevisions(
    repoId,
    reference,
    selectedRecipe,
    managed,
  );
  const packageRevisions = packagesQuery.data ?? [];
  // A package key is chosen within one recipe revision.
  const [packageChoice, setPackageChoice] = useState({ recipe: "", key: "" });
  const selectedPackageKey =
    packageChoice.recipe === selectedRecipe ? packageChoice.key : "";
  const setSelectedPackageKey = (
    next: string | ((current: string) => string),
  ) =>
    setPackageChoice((current) => {
      const before = current.recipe === selectedRecipe ? current.key : "";
      return {
        recipe: selectedRecipe,
        key: typeof next === "function" ? next(before) : next,
      };
    });
  const [deleting, setDeleting] = useState("");
  const [actionError, setError] = useState("");

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

  const error =
    actionError ||
    (recipesQuery.error
      ? requestErrorMessage(
          text(
            "读取 Conan recipe revisions 失败",
            "Failed to load Conan recipe revisions",
          ),
          recipesQuery.error,
        )
      : packagesQuery.error
        ? requestErrorMessage(
            text(
              "读取 Conan package revisions 失败",
              "Failed to load Conan package revisions",
            ),
            packagesQuery.error,
          )
        : "");

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
      queryClient.setQueryData<typeof packageRevisions>(
        conanPackageRevisionsKey(repoId, reference, selectedRecipe),
        (items) =>
          (items ?? []).filter(
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
            {error && <ErrorBanner error={error} />}
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
              <div className="overflow-hidden rounded-lg border border-line">
                <div className="border-b border-line px-3 py-2 text-sm font-medium text-fg">
                  {text("Package revisions", "Package revisions")}
                </div>
                {packageRevisions.map((item) => {
                  const packageKey = `${item.recipeRevision}:${item.packageId}:${item.revision}`;
                  const deletingItem = deleting === `package:${packageKey}`;
                  const selected = selectedPackageKey === packageKey;
                  return (
                    <div
                      key={packageKey}
                      className={`flex items-center justify-between gap-3 border-b border-line px-3 py-2 last:border-0 ${selected ? "bg-[var(--ag-navigation-selected-start)]" : ""}`}
                    >
                      <div className="min-w-0">
                        <div className="font-mono text-xs text-fg">
                          {item.packageId}#{item.revision}
                        </div>
                        <div className="mt-0.5 text-xs text-fg-tertiary">
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
