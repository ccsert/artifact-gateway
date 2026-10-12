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

type Localize = (chinese: string, english: string) => string;

/**
 * The wizard's file tables: staged files, removable before upload, and the
 * upload step's per-file status.
 */
export function stagedFileColumns(
  text: Localize,
  removeFile: (name: string) => void,
): {
  stagedColumns: ColumnsType<StagedFile>;
  uploadColumns: ColumnsType<StagedFile>;
} {
  const stagedColumns: ColumnsType<StagedFile> = [
    {
      title: text("文件名", "File name"),
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
      title: text("大小", "Size"),
      dataIndex: "size",
      key: "size",
      width: 110,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("操作", "Actions"),
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
          {text("移除", "Remove")}
        </Button>
      ),
    },
  ];
  const uploadColumns: ColumnsType<StagedFile> = [
    {
      title: text("文件名", "File name"),
      dataIndex: "name",
      key: "name",
      render: (value: string) => (
        <span className="font-mono text-xs text-fg">{value}</span>
      ),
    },
    {
      title: text("大小", "Size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-fg-secondary">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "uploaded",
      key: "uploaded",
      width: 120,
      render: (value: boolean) =>
        value ? (
          <Badge tone="success">{text("已上传", "Uploaded")}</Badge>
        ) : (
          <Badge tone="neutral">{text("待上传", "Pending")}</Badge>
        ),
    },
  ];
  return { stagedColumns, uploadColumns };
}
