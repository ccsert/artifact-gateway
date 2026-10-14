import type { Translate } from "../../lib/preferences";
import { DeleteOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Badge } from "../../components/ui/Badge";
import { formatBytes, shortDigest } from "../../lib/format";

export interface StagedFile {
  name: string;
  digest: string;
  size: number;
  file: File;
  uploaded: boolean;
}

/**
 * The wizard's file tables: staged files, removable before upload, and the
 * upload step's per-file status.
 */
export function stagedFileColumns(
  t: Translate,
  removeFile: (name: string) => void,
): {
  stagedColumns: ColumnsType<StagedFile>;
  uploadColumns: ColumnsType<StagedFile>;
} {
  const stagedColumns: ColumnsType<StagedFile> = [
    {
      title: t("repository.fileName"),
      dataIndex: "name",
      key: "name",
      render: (value: string) => (
        <span className="font-mono text-xs text-fg">{value}</span>
      ),
    },
    {
      title: "sha256",
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-fg-tertiary" title={value}>
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: t("repository.size"),
      dataIndex: "size",
      key: "size",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: t("repository.actions"),
      key: "actions",
      width: 90,
      render: (_, file) => (
        <Button
          type="text"
          size="small"
          danger
          icon={<DeleteOutlined />}
          onClick={() => removeFile(file.name)}
        >
          {t("repository.remove")}
        </Button>
      ),
    },
  ];
  const uploadColumns: ColumnsType<StagedFile> = [
    {
      title: t("repository.fileName"),
      dataIndex: "name",
      key: "name",
      render: (value: string) => (
        <span className="font-mono text-xs text-fg">{value}</span>
      ),
    },
    {
      title: t("repository.size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: t("repository.status"),
      dataIndex: "uploaded",
      key: "uploaded",
      width: 120,
      render: (value: boolean) =>
        value ? (
          <Badge tone="success">{t("repository.uploaded")}</Badge>
        ) : (
          <Badge tone="neutral">{t("repository.pending")}</Badge>
        ),
    },
  ];
  return { stagedColumns, uploadColumns };
}
