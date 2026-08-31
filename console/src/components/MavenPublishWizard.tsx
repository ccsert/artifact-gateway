import { useState } from "react";
import {
  DeleteOutlined,
  SendOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import {
  Button,
  Input,
  Popconfirm,
  Result,
  Select,
  Steps,
  Table,
  Upload,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import type { UploadProps } from "antd";
import {
  createPublishSession,
  getPublishSession,
  commitPublishSession,
} from "../client";
import type { PublishSession, DeclaredObject } from "../client";
import { useAuth } from "../lib/auth";
import { Card, Field } from "./Layout";
import { ErrorBanner } from "./Feedback";
import { StateBadge, Badge } from "./Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usePreferences } from "../lib/preferences";

async function sha256Hex(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const hash = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

interface StagedFile {
  name: string;
  digest: string;
  size: number;
  file: File;
  uploaded: boolean;
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
  const { locale, text } = usePreferences();
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
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setSession(data);
      setStep("upload");
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
    const { error: err } = await commitPublishSession({
      path: { sessionId: session.id },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    setStep("done");
    onPublished?.();
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
  const stagedColumns: ColumnsType<StagedFile> = [
    {
      title: text("文件名", "File name"),
      dataIndex: "name",
      key: "name",
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-200">{value}</span>
      ),
    },
    {
      title: "sha256",
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
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
        <span className="text-xs text-zinc-400">{formatBytes(value)}</span>
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
        <span className="font-mono text-xs text-zinc-200">{value}</span>
      ),
    },
    {
      title: text("大小", "Size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-zinc-400">{formatBytes(value)}</span>
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

  return (
    <div className="space-y-5">
      {error !== null && <ErrorBanner error={error} />}

      <Steps
        current={currentStep}
        responsive={false}
        size="small"
        variant="outlined"
        items={[
          { title: text("声明坐标与对象", "Declare coordinate and objects") },
          { title: text("上传文件", "Upload files") },
          { title: text("完成", "Complete") },
        ]}
      />

      {step === "declare" && (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <Field
              label={text("Maven 坐标", "Maven coordinate")}
              hint={text(
                "group:artifact:version，如 com.acme:widget:1.0.0",
                "group:artifact:version, for example com.acme:widget:1.0.0",
              )}
            >
              <Input
                className="font-mono text-xs"
                placeholder="com.example:my-lib:1.0.0"
                value={coordinate}
                onChange={(e) => setCoordinate(e.target.value)}
              />
            </Field>
            <Field
              label={text("POM 对象名", "POM object name")}
              hint={text(
                "staged 文件中作为 POM 的那个",
                "The staged file used as the POM",
              )}
            >
              <Select
                showSearch
                className="w-full font-mono text-xs"
                placeholder={text(
                  "请先选择 POM 文件",
                  "Select a POM file first",
                )}
                value={pomObject}
                onChange={setPomObject}
                options={staged
                  .filter((file) => file.name.endsWith(".pom"))
                  .map((file) => ({ value: file.name, label: file.name }))}
              />
            </Field>
          </div>
          <Field
            label={text("发布文件", "Release files")}
            hint={text(
              "选择 POM、JAR、源码包等；浏览器会自动计算 sha256",
              "Select the POM, JAR, source archive, and other files; SHA-256 is calculated in the browser",
            )}
            group
          >
            <Upload multiple showUploadList={false} beforeUpload={beforeUpload}>
              <Button icon={<UploadOutlined />}>
                {text("选择发布文件", "Choose release files")}
              </Button>
            </Upload>
          </Field>
          {staged.length > 0 && (
            <Card>
              <Table<StagedFile>
                className="ag-console-table"
                rowKey="name"
                size="middle"
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
            {text("创建发布会话", "Create publish session")}
          </Button>
        </div>
      )}

      {step === "upload" && session && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-zinc-800 px-4 py-3 text-xs text-zinc-400">
            <span>
              {text("会话", "Session")}{" "}
              <code className="font-mono text-zinc-300">
                {session.id.slice(0, 8)}…
              </code>
            </span>
            <StateBadge state={session.state} />
            <span>
              {text("过期时间：", "Expires: ")}
              {formatDate(session.expiresAt, locale)}
            </span>
          </div>
          <Card>
            <Table<StagedFile>
              className="ag-console-table"
              rowKey="name"
              size="middle"
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
                ? text("全部已上传", "All files uploaded")
                : text("上传全部文件", "Upload all files")}
            </Button>
            <Button
              type="primary"
              icon={<SendOutlined />}
              onClick={commit}
              loading={busy}
              disabled={!allUploaded}
            >
              {text("提交发布", "Commit release")}
            </Button>
            <Popconfirm
              title={text("确认放弃当前发布？", "Discard this release?")}
              description={text(
                "本地选择和会话信息将被清空，已上传的临时对象由服务端过期回收。",
                "Local selections and session details will be cleared. Uploaded temporary objects expire on the server.",
              )}
              okText={text("放弃发布", "Discard release")}
              cancelText={text("继续编辑", "Continue editing")}
              okButtonProps={{ danger: true }}
              onConfirm={reset}
            >
              <Button type="text" danger className="ml-auto">
                {text("放弃并重新开始", "Discard and start over")}
              </Button>
            </Popconfirm>
          </div>
        </div>
      )}

      {step === "done" && (
        <Result
          status="success"
          title={text("发布成功", "Release published")}
          subTitle={<span className="font-mono text-xs">{coordinate}</span>}
          extra={
            <Button type="primary" onClick={reset}>
              {text("再发布一个", "Publish another")}
            </Button>
          }
        />
      )}
    </div>
  );
}
