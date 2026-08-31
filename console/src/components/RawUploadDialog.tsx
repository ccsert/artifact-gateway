import { useState } from "react";
import { UploadOutlined } from "@ant-design/icons";
import { Button, Input, Space, Upload } from "antd";
import { useAuth } from "../lib/auth";
import { Modal, useDisclosure } from "./Modal";
import { Field } from "./Layout";
import { ErrorBanner } from "./Feedback";
import type { Repository } from "../client";
import { usePreferences } from "../lib/preferences";
import {
  containsDisallowedRawPathCharacters,
  encodeRawPathSegment,
  rawResourceURL,
} from "../lib/rawPath";

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
    const enteredPath = path === "" ? file.name : path;
    const targetPath = enteredPath.startsWith("/")
      ? enteredPath.slice(1)
      : enteredPath;
    const rawSegments = targetPath.split("/");
    if (
      rawSegments.length === 0 ||
      rawSegments.some(
        (segment) =>
          segment === "" ||
          segment === "." ||
          segment === ".." ||
          segment.includes("\\") ||
          containsDisallowedRawPathCharacters(segment),
      )
    ) {
      setError(
        new Error(
          text(
            "目标路径无效：不能使用空路径段、. 或 .. 路径段、反斜杠、控制字符或双向格式化字符。支持中文、空格和括号。",
            "Invalid target path: empty, dot, dot-dot, backslash, control, and bidirectional formatting characters are not allowed. Unicode, spaces, and parentheses are supported.",
          ),
        ),
      );
      return;
    }
    let segments: string[];
    try {
      segments = rawSegments.map(encodeRawPathSegment);
    } catch {
      setError(
        new Error(
          text(
            "目标路径包含无法编码的字符，请修改文件名或目标路径后重试。",
            "The target path contains characters that cannot be encoded. Change the file name or target path and try again.",
          ),
        ),
      );
      return;
    }
    const canonicalPath = segments.join("/");
    if (canonicalPath.length > 4096) {
      setError(
        new Error(
          text(
            "目标路径编码后不能超过 4096 字节，请缩短目录或文件名。",
            "The encoded target path cannot exceed 4096 bytes. Shorten the directories or file name.",
          ),
        ),
      );
      return;
    }
    if (path !== "" && path !== targetPath) setPath(targetPath);
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(rawResourceURL(repo.name, canonicalPath), {
        method: "PUT",
        credentials: "include",
        headers: {
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
          "Content-Type": file.type || "application/octet-stream",
        },
        body: file,
      });
      if (!res.ok) {
        const detail = (await res.text().catch(() => "")).trim();
        if (res.status === 400 && detail === "invalid raw path") {
          throw new Error(
            text(
              "上传失败（400）：目标路径不符合 Raw 路径规则。支持中文、空格和括号；请删除空路径段、.、..、反斜杠或空字符后重试。",
              "Upload failed (400): the target does not follow the Raw path rules. Unicode, spaces, and parentheses are supported; remove empty, dot, dot-dot, backslash, or NUL segments and try again.",
            ),
          );
        }
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
          {error ? (
            <ErrorBanner
              error={error}
              title={text("Raw 文件上传失败", "Raw file upload failed")}
            />
          ) : null}
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
              "相对于仓库根；可以用一个前导 / 明确表示仓库根，上传时会自动转换。留空则用文件名。支持中文、空格和括号；编码后最多 4096 字节，不能使用空路径段、.、..、反斜杠、控制字符或双向格式化字符。",
              "Relative to the repository root. One optional leading / may explicitly represent the repository root and is removed on upload. Leave empty to use the file name. Unicode, spaces, and parentheses are supported up to 4096 encoded bytes; empty, dot, dot-dot, backslash, control, and bidirectional formatting characters are not.",
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
