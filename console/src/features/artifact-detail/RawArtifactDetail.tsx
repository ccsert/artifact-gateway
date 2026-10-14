import { useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm } from "antd";
import { ArtifactDetailView } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { useAuth } from "../../lib/auth";
import { usePreferences } from "../../lib/preferences";
import { rawResourceURL } from "../../lib/rawPath";

// Raw 制品详情：使用方法 + 元信息
export function RawArtifactDetail({
  repositoryId,
  repoName,
  meta,
  onDeleted,
  canQuarantine = false,
  canDelete = false,
}: {
  repositoryId?: string;
  repoName: string;
  meta: ArtifactMeta;
  onDeleted?: () => void;
  canQuarantine?: boolean;
  canDelete?: boolean;
}) {
  const { t } = usePreferences();
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
          : t("artifactDetail.failedToDeleteRawFile"),
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
      {canDelete && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2">
          <div>
            <div className="text-xs font-medium text-fg">
              {t("artifactDetail.rawFile")}
            </div>
            {deleteError && (
              <div className="mt-1 text-xs text-[var(--ag-status-danger)]">
                {deleteError}
              </div>
            )}
          </div>
          <Popconfirm
            title={t("artifactDetail.deleteThisRawFile")}
            description={t("artifactDetail.thisPathWillNoLongerBeVisible")}
            okText={t("artifactDetail.delete")}
            cancelText={t("artifactDetail.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteCurrent()}
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              loading={deleting}
            >
              {t("artifactDetail.deleteFile")}
            </Button>
          </Popconfirm>
        </div>
      )}
    </div>
  );
}
