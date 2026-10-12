import { useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm } from "antd";
import { ArtifactDetailView } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { useAuth } from "../../lib/auth";
import { usePreferences } from "../../lib/preferences";
import { rawResourceURL } from "../../lib/rawPath";

// Maven 制品详情：使用方法 + 按发布版本、快照构建分开的版本列表。
// meta.coordinate 可以是完整 GAV（com.example:hello:1.0.0）或 GA（com.example:hello）。
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
      {canDelete && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2">
          <div>
            <div className="text-xs font-medium text-fg">
              {text("Raw 文件", "Raw file")}
            </div>
            {deleteError && (
              <div className="mt-1 text-xs text-[var(--ag-status-danger)]">
                {deleteError}
              </div>
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
      )}
    </div>
  );
}
