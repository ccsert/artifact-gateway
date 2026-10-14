import { useState } from "react";
import { SendOutlined, UploadOutlined } from "@ant-design/icons";
import { Button, Input, Popconfirm, Result, Select, Steps, Upload } from "antd";
import type { UploadProps } from "antd";
import {
  createPublishSession,
  getPublishSession,
  commitPublishSession,
} from "../../client";
import type { PublishSession, DeclaredObject } from "../../client";
import { useAuth } from "../../lib/auth";
import { Card, Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { stagedFileColumns, type StagedFile } from "./mavenPublishColumns";

async function sha256Hex(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const hash = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

type Step = "declare" | "upload" | "done";

export function MavenPublishWizard({
  repositoryId,
  onPublished,
}: {
  repositoryId: string;
  onPublished?: () => void;
}) {
  const { token } = useAuth();
  const { locale, t } = usePreferences();
  const [step, setStep] = useState<Step>("declare");
  const [coordinate, setCoordinate] = useState("");
  const [pomObject, setPomObject] = useState("");
  const [staged, setStaged] = useState<StagedFile[]>([]);
  const [session, setSession] = useState<PublishSession | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(false);

  // 选择文件后计算 digest
  const addFiles = async (files: readonly File[]) => {
    const next: StagedFile[] = [];
    for (const file of files) {
      const digest = `sha256:${await sha256Hex(file)}`;
      next.push({
        name: file.name,
        digest,
        size: file.size,
        file,
        uploaded: false,
      });
    }
    setStaged((prev) => {
      const merged = [...prev];
      for (const f of next) {
        const idx = merged.findIndex((x) => x.name === f.name);
        if (idx >= 0) merged[idx] = f;
        else merged.push(f);
      }
      return merged;
    });
    // 自动推断 pomObject 和 coordinate
    const pom = next.find((f) => f.name.endsWith(".pom"));
    if (pom && !pomObject) setPomObject(pom.name);
  };

  const createSession = async () => {
    setBusy(true);
    setError(null);
    const objects: DeclaredObject[] = staged.map((f) => ({
      name: f.name,
      digest: f.digest,
      size: f.size,
    }));
    try {
      const { data, error: err } = await createPublishSession({
        path: { repositoryId },
        body: {
          format: "maven",
          coordinate: coordinate.trim(),
          pomObject,
          objects,
        },
        headers: { "Idempotency-Key": crypto.randomUUID() },
      });
      if (err) {
        setError(err);
        return;
      }
      if (data) {
        setSession(data);
        setStep("upload");
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const uploadAll = async () => {
    if (!session) return;
    setUploading(true);
    setError(null);
    for (const f of staged) {
      if (f.uploaded) continue;
      try {
        const res = await fetch(
          `/api/v2/publish-sessions/${session.id}/objects/${encodeURIComponent(f.name)}`,
          {
            method: "PUT",
            credentials: "include",
            headers: {
              ...(token ? { Authorization: `Bearer ${token}` } : {}),
              "Content-Type": "application/octet-stream",
            },
            body: f.file,
          },
        );
        if (!res.ok)
          throw new Error(
            `${f.name}: ${res.status} ${(await res.text()).slice(0, 100)}`,
          );
        setStaged((prev) =>
          prev.map((x) => (x.name === f.name ? { ...x, uploaded: true } : x)),
        );
      } catch (e) {
        setError(e);
        setUploading(false);
        return;
      }
    }
    setUploading(false);
    // 刷新会话状态
    const { data } = await getPublishSession({
      path: { sessionId: session.id },
    });
    if (data) setSession(data);
  };

  const commit = async () => {
    if (!session) return;
    setBusy(true);
    setError(null);
    try {
      const { error: err } = await commitPublishSession({
        path: { sessionId: session.id },
      });
      if (err) {
        setError(err);
        return;
      }
      setStep("done");
      onPublished?.();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const reset = () => {
    setStep("declare");
    setCoordinate("");
    setPomObject("");
    setStaged([]);
    setSession(null);
    setError(null);
  };

  const allUploaded = staged.length > 0 && staged.every((f) => f.uploaded);
  const removeFile = (name: string) => {
    setStaged((previous) => previous.filter((file) => file.name !== name));
    if (pomObject === name) setPomObject("");
  };
  const beforeUpload: UploadProps["beforeUpload"] = (file, fileList) => {
    if (file.uid === fileList[0]?.uid) void addFiles(fileList);
    return Upload.LIST_IGNORE;
  };
  const currentStep = step === "declare" ? 0 : step === "upload" ? 1 : 2;
  const { stagedColumns, uploadColumns } = stagedFileColumns(t, removeFile);

  return (
    <div className="space-y-5">
      {error !== null && <ErrorBanner error={error} />}

      <Steps
        current={currentStep}
        responsive={false}
        size="small"
        variant="outlined"
        items={[
          { title: t("repository.declareCoordinateAndObjects") },
          { title: t("repository.uploadFiles") },
          { title: t("repository.complete") },
        ]}
      />

      {step === "declare" && (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <Field
              label={t("repository.mavenCoordinate")}
              hint={t("repository.groupArtifactVersionForExampleComAcmeWidget")}
            >
              <Input
                className="font-mono text-xs"
                placeholder="com.example:my-lib:1.0.0"
                value={coordinate}
                onChange={(e) => setCoordinate(e.target.value)}
              />
            </Field>
            <Field
              label={t("repository.pOMObjectName")}
              hint={t("repository.theStagedFileUsedAsThePOM")}
            >
              <Select
                showSearch
                className="w-full font-mono text-xs"
                placeholder={t("repository.selectAPOMFileFirst")}
                value={pomObject}
                onChange={setPomObject}
                options={staged
                  .filter((file) => file.name.endsWith(".pom"))
                  .map((file) => ({ value: file.name, label: file.name }))}
              />
            </Field>
          </div>
          <Field
            label={t("repository.releaseFiles")}
            hint={t("repository.selectThePOMJARSourceArchiveAndOther")}
            group
          >
            <Upload multiple showUploadList={false} beforeUpload={beforeUpload}>
              <Button icon={<UploadOutlined />}>
                {t("repository.chooseReleaseFiles")}
              </Button>
            </Upload>
          </Field>
          {staged.length > 0 && (
            <Card>
              <ConsoleTable<StagedFile>
                rowKey="name"
                dataSource={staged}
                columns={stagedColumns}
                pagination={false}
                scroll={{ x: 640 }}
              />
            </Card>
          )}
          <Button
            type="primary"
            onClick={createSession}
            loading={busy}
            disabled={!coordinate.trim() || !pomObject || staged.length === 0}
          >
            {t("repository.createPublishSession")}
          </Button>
        </div>
      )}

      {step === "upload" && session && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-line px-4 py-3 text-xs text-fg-secondary">
            <span>
              {t("repository.session")}{" "}
              <code className="font-mono text-fg-secondary">
                {session.id.slice(0, 8)}…
              </code>
            </span>
            <StateBadge state={session.state} />
            <span>
              {t("repository.expires")}
              {formatDate(session.expiresAt, locale)}
            </span>
          </div>
          <Card>
            <ConsoleTable<StagedFile>
              rowKey="name"
              dataSource={staged}
              columns={uploadColumns}
              pagination={false}
              scroll={{ x: 520 }}
            />
          </Card>
          <div className="flex gap-2">
            <Button
              icon={<UploadOutlined />}
              onClick={uploadAll}
              loading={uploading}
              disabled={allUploaded}
            >
              {allUploaded
                ? t("repository.allFilesUploaded")
                : t("repository.uploadAllFiles")}
            </Button>
            <Button
              type="primary"
              icon={<SendOutlined />}
              onClick={commit}
              loading={busy}
              disabled={!allUploaded}
            >
              {t("repository.commitRelease")}
            </Button>
            <Popconfirm
              title={t("repository.discardThisRelease")}
              description={t(
                "repository.localSelectionsAndSessionDetailsWillBeCleared",
              )}
              okText={t("repository.discardRelease")}
              cancelText={t("repository.continueEditing")}
              okButtonProps={{ danger: true }}
              onConfirm={reset}
            >
              <Button type="text" danger className="ml-auto">
                {t("repository.discardAndStartOver")}
              </Button>
            </Popconfirm>
          </div>
        </div>
      )}

      {step === "done" && (
        <Result
          status="success"
          title={t("repository.releasePublished")}
          subTitle={<span className="font-mono text-xs">{coordinate}</span>}
          extra={
            <Button type="primary" onClick={reset}>
              {t("repository.publishAnother")}
            </Button>
          }
        />
      )}
    </div>
  );
}
