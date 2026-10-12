import { useState } from "react";
import { Alert, Button, Input, Popconfirm, Upload } from "antd";
import { UploadOutlined } from "@ant-design/icons";
import {
  exportAptRepositorySnapshot,
  restoreAptRepositorySnapshot,
} from "../../client";
import type {
  AptRepositorySnapshot,
  AptSnapshotHistory,
  Repository,
} from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { downloadBlob, sha256Receipt } from "../../lib/download";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { APT_ARCHIVE_RECEIPT, archiveExportable } from "./aptModel";

/** Export a signed snapshot as an archive, or restore one against its receipt. */
export function APTArchiveOperations({
  repo,
  snapshots,
  disabled,
  onRestored,
}: {
  repo: Repository;
  snapshots: AptSnapshotHistory[];
  disabled: boolean;
  onRestored: () => Promise<void>;
}) {
  const { text } = usePreferences();
  const [exportingId, setExportingId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [receipt, setReceipt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [savedArchive, setSavedArchive] = useState<{
    filename: string;
    digest: string | null;
  } | null>(null);

  const exportable = snapshots.filter(archiveExportable);
  const exportBusy = exportingId !== "" || disabled;
  const receiptValid = APT_ARCHIVE_RECEIPT.test(receipt.trim());
  const restoreReady = Boolean(file) && receiptValid && !busy && !disabled;

  const exportArchive = async (snapshot: AptRepositorySnapshot) => {
    setError(null);
    setNotice("");
    setSavedArchive(null);
    setExportingId(snapshot.id);
    try {
      const response = await exportAptRepositorySnapshot({
        path: { repositoryId: repo.id, snapshotId: snapshot.id },
      });
      if (response.error) throw response.error;
      if (!response.data)
        throw new Error(text("导出响应为空", "Export response is empty"));
      const blob = response.data as Blob;
      const filename = `apt-${snapshot.suite}-${snapshot.sequence}.tar`;
      downloadBlob(filename, blob);
      setSavedArchive({ filename, digest: await sha256Receipt(blob) });
    } catch (err) {
      setError(err);
    } finally {
      setExportingId("");
    }
  };

  const restoreArchive = async () => {
    if (!file || !receiptValid) return;
    setError(null);
    setNotice("");
    setBusy(true);
    try {
      const response = await restoreAptRepositorySnapshot({
        path: { repositoryId: repo.id },
        headers: { "X-Artifact-Archive-Digest": receipt.trim() },
        body: file,
      });
      if (response.error) throw response.error;
      if (!response.data)
        throw new Error(text("恢复响应为空", "Restore response is empty"));
      setFile(null);
      setReceipt("");
      setNotice(
        text(
          `已从归档恢复 ${response.data.suite} 快照 #${response.data.sequence}`,
          `Restored ${response.data.suite} snapshot #${response.data.sequence} from the archive`,
        ),
      );
      await onRestored();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ag-page-stack">
      <p>
        {text(
          "导出可携带的灾备归档，或在灾难后按独立保存的回执恢复同一套件的历史快照。归档只含公开签名与软件包字节，不含签名私钥。",
          "Export a portable disaster-recovery archive, or restore a historical snapshot of this suite after a disaster using the receipt stored independently. An archive carries public signatures and package bytes only, never private-key material.",
        )}
      </p>
      {error !== null && <ErrorBanner error={error} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      {savedArchive && (
        <Alert
          type={savedArchive.digest ? "success" : "warning"}
          showIcon
          title={text("归档已开始下载", "Archive download started")}
          description={
            savedArchive.digest
              ? text(
                  `文件 ${savedArchive.filename}。请与回执 ${savedArchive.digest} 分开独立保存；恢复时必须原样提交该回执。`,
                  `File ${savedArchive.filename}. Store it separately from the receipt ${savedArchive.digest}; restore requires that exact receipt.`,
                )
              : text(
                  `文件 ${savedArchive.filename}。当前浏览器无法计算摘要，请对归档运行 gateway apt-snapshot verify，并独立保存它输出的 sha256 回执。`,
                  `File ${savedArchive.filename}. This browser cannot compute the digest; run gateway apt-snapshot verify on the archive and store the sha256 receipt it prints.`,
                )
          }
        />
      )}
      <section
        aria-label={text("导出灾备归档", "Export disaster-recovery archive")}
      >
        <h4>{text("导出灾备归档", "Export disaster-recovery archive")}</h4>
        <ConsoleTable
          rowKey={(item) => item.snapshot.id}
          dataSource={exportable}
          scroll={{ x: 720 }}
          pagination={{}}
          columns={[
            {
              title: text("快照", "Snapshot"),
              render: (_, item) => <>#{item.snapshot.sequence}</>,
            },
            {
              title: text("状态", "State"),
              render: (_, item) => <StateBadge state={item.snapshot.state} />,
            },
            {
              title: text("发布时间", "Published"),
              render: (_, item) => formatDate(item.snapshot.publishedAt),
            },
            {
              title: text("操作", "Action"),
              render: (_, item) => (
                <Button
                  loading={exportingId === item.snapshot.id}
                  disabled={exportBusy}
                  onClick={() => void exportArchive(item.snapshot)}
                >
                  {text("导出归档", "Export archive")}
                </Button>
              ),
            },
          ]}
        />
      </section>
      <section aria-label={text("从归档恢复", "Restore from archive")}>
        <h4>{text("从归档恢复", "Restore from archive")}</h4>
        <p>
          {text(
            "仅接受属于本仓库的 Gateway v1 归档。恢复会先校验备份回执、两个 OpenPGP 签名与规范索引；同一归档重复恢复是幂等的，且不会重新签名或提升已退役快照。",
            "Only a Gateway v1 archive belonging to this repository is accepted. Restore first verifies the backup receipt, both OpenPGP signatures, and canonical indexes; replaying the same archive is idempotent and never re-signs or promotes a retired snapshot.",
          )}
        </p>
        <div className="ag-apt-toolbar">
          <Upload
            accept=".tar"
            maxCount={1}
            showUploadList={false}
            disabled={busy}
            beforeUpload={(candidate) => {
              setFile(candidate);
              setError(null);
              setNotice("");
              return false;
            }}
          >
            <Button icon={<UploadOutlined aria-hidden />} disabled={busy}>
              {text("选择归档文件", "Choose archive file")}
            </Button>
          </Upload>
          {file && (
            <>
              <code>{file.name}</code>
              <Button type="link" disabled={busy} onClick={() => setFile(null)}>
                {text("移除", "Remove")}
              </Button>
            </>
          )}
        </div>
        <Field
          label={text("备份回执 (sha256)", "Backup receipt (sha256)")}
          hint={text(
            "必须是备份时独立保存的 sha256:<64 位小写十六进制> 回执。请勿根据本次上传的文件现算摘要，服务端会校验两者一致。",
            "Must be the sha256:<64 lowercase hex> receipt stored independently at backup time. Do not derive it from this upload; the server verifies they match.",
          )}
        >
          <Input
            aria-label={text("备份回执 (sha256)", "Backup receipt (sha256)")}
            className="font-mono"
            value={receipt}
            placeholder="sha256:"
            disabled={busy}
            onChange={(event) => setReceipt(event.target.value)}
          />
        </Field>
        <Popconfirm
          title={text("从此归档恢复？", "Restore from this archive?")}
          description={text(
            "恢复会写入软件包与已签名索引。目标套件已有冲突快照或坐标时会被拒绝。",
            "Restore writes packages and signed indexes. A conflicting snapshot or coordinate in the target suite is rejected.",
          )}
          okText={text("恢复归档", "Restore archive")}
          cancelText={text("取消", "Cancel")}
          disabled={!restoreReady}
          onConfirm={() => void restoreArchive()}
        >
          <Button type="primary" loading={busy} disabled={!restoreReady}>
            {text("恢复归档", "Restore archive")}
          </Button>
        </Popconfirm>
      </section>
    </div>
  );
}
