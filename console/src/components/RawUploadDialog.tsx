import { useState } from "react";
import { UploadOutlined } from "@ant-design/icons";
import { Button, Input, Space, Upload } from "antd";
import { useAuth } from "../lib/auth";
import { Modal, useDisclosure } from "./Modal";
import { Field } from "./Layout";
import { ErrorBanner } from "./Feedback";
import type { Repository } from "../client";
import { usePreferences } from "../lib/preferences";

// Uploads a single object to a Raw Hosted repository via the native
// /raw/<repository>/<path> PUT route. The server computes the sha256 digest,
// so no Digest header is required; a provided one would be validated.
export function RawUploadDialog({
  repo,
  onUploaded,
}: {
  repo: Repository;
  onUploaded: () => void;
}) {
  const { token } = useAuth();
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [file, setFile] = useState<File | null>(null);
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const pickFile = (f: File | null) => {
    setFile(f);
    setPath((current) => current || f?.name || "");
  };

  const submit = async () => {
    if (!file) return;
    const segments = (path.trim() || file.name)
      .replace(/^\/+/, "")
      .split("/")
      .map((s) => encodeURIComponent(s));
    if (segments.length === 0 || segments.some((s) => s === "")) {
      setError(
        new Error(
          text(
            "目标路径不能为空或包含空段",
            "The target path cannot be empty or contain empty segments",
          ),
        ),
      );
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(
        `/raw/${encodeURIComponent(repo.name)}/${segments.join("/")}`,
        {
          method: "PUT",
          credentials: "include",
          headers: {
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
            "Content-Type": file.type || "application/octet-stream",
          },
          body: file,
        },
      );
      if (!res.ok) {
        const detail = await res.text().catch(() => "");
        throw new Error(
          `${text("上传失败", "Upload failed")} (${res.status})${
            detail ? `: ${detail.slice(0, 200)}` : ""
          }`,
        );
      }
      dialog.hide();
      setFile(null);
      setPath("");
      onUploaded();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button
        icon={<UploadOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {text("上传", "Upload")}
      </Button>
      <Modal
        open={dialog.open}
        title={`${text("上传到", "Upload to")} ${repo.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!file}
            >
              {text("上传", "Upload")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label={text("文件", "File")} group>
            <Space>
              <Upload
                maxCount={1}
                showUploadList={false}
                beforeUpload={(selectedFile) => {
                  pickFile(selectedFile);
                  return Upload.LIST_IGNORE;
                }}
              >
                <Button icon={<UploadOutlined />}>
                  {text("选择文件", "Choose file")}
                </Button>
              </Upload>
              <span
                className="max-w-72 truncate text-xs text-zinc-400"
                title={file?.name}
              >
                {file?.name ?? text("尚未选择文件", "No file selected")}
              </span>
            </Space>
          </Field>
          <Field
            label={text("目标路径", "Target path")}
            hint={text(
              "相对于仓库根；留空则用文件名。不要以 / 开头。",
              "Relative to the repository root. Leave empty to use the file name. Do not start with /.",
            )}
          >
            <Input
              className="font-mono"
              placeholder="releases/widget.tar"
              value={path}
              onChange={(e) => setPath(e.target.value)}
            />
          </Field>
          <p className="text-xs text-zinc-500">
            {text(
              "服务端会校验 sha256；同名路径会被覆盖。",
              "The server validates SHA-256. An existing path with the same name will be overwritten.",
            )}
          </p>
        </div>
      </Modal>
    </>
  );
}
