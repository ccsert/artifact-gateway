import { useState } from "react";
import { UploadOutlined } from "@ant-design/icons";
import { Button, Input, Space, Upload } from "antd";
import { useAuth } from "../../lib/auth";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import type { Repository } from "../../client";
import { usePreferences } from "../../lib/preferences";
import {
  containsDisallowedRawPathCharacters,
  encodeRawPathSegment,
  rawResourceURL,
} from "../../lib/rawPath";

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
  const { t } = usePreferences();
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
        new Error(t("repository.invalidTargetPathEmptyDotDotDotBackslash")),
      );
      return;
    }
    let segments: string[];
    try {
      segments = rawSegments.map(encodeRawPathSegment);
    } catch {
      setError(
        new Error(t("repository.theTargetPathContainsCharactersThatCannotBe")),
      );
      return;
    }
    const canonicalPath = segments.join("/");
    if (canonicalPath.length > 4096) {
      setError(
        new Error(t("repository.theEncodedTargetPathCannotExceed4096Bytes")),
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
            t("repository.uploadFailed400TheTargetDoesNotFollow"),
          );
        }
        throw new Error(
          `${t("repository.uploadFailed")} (${res.status})${
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
        {t("repository.upload")}
      </Button>
      <Modal
        open={dialog.open}
        title={`${t("repository.uploadTo")} ${repo.name}`}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {t("repository.cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!file}
            >
              {t("repository.upload")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? (
            <ErrorBanner
              error={error}
              title={t("repository.rawFileUploadFailed")}
            />
          ) : null}
          <Field label={t("repository.file")} group>
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
                  {t("repository.chooseFile")}
                </Button>
              </Upload>
              <span
                className="max-w-72 truncate text-xs text-fg-secondary"
                title={file?.name}
              >
                {file?.name ?? t("repository.noFileSelected")}
              </span>
            </Space>
          </Field>
          <Field
            label={t("repository.targetPath")}
            hint={t("repository.relativeToTheRepositoryRootOneOptionalLeading")}
          >
            <Input
              className="font-mono"
              placeholder="releases/widget.tar"
              value={path}
              onChange={(e) => setPath(e.target.value)}
            />
          </Field>
          <p className="text-xs text-fg-tertiary">
            {t("repository.theServerValidatesSHA256AnExistingPath")}
          </p>
        </div>
      </Modal>
    </>
  );
}
