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
  const { t } = usePreferences();
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
        throw new Error(t("repository.exportResponseIsEmpty"));
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
        throw new Error(t("repository.restoreResponseIsEmpty"));
      setFile(null);
      setReceipt("");
      setNotice(
        t("repository.restoredSnapshotFromTheArchive", {
          suite: response.data.suite,
          sequence: response.data.sequence,
        }),
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
      <p>{t("repository.exportAPortableDisasterRecoveryArchiveOrRestore")}</p>
      {error !== null && <ErrorBanner error={error} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      {savedArchive && (
        <Alert
          type={savedArchive.digest ? "success" : "warning"}
          showIcon
          title={t("repository.archiveDownloadStarted")}
          description={
            savedArchive.digest
              ? t("repository.fileStoreItSeparatelyFromTheReceiptRestore", {
                  filename: savedArchive.filename,
                  digest: savedArchive.digest,
                })
              : t("repository.fileThisBrowserCannotComputeTheDigestRun", {
                  filename: savedArchive.filename,
                })
          }
        />
      )}
      <section aria-label={t("repository.exportDisasterRecoveryArchive")}>
        <h4>{t("repository.exportDisasterRecoveryArchive")}</h4>
        <ConsoleTable
          rowKey={(item) => item.snapshot.id}
          dataSource={exportable}
          scroll={{ x: 720 }}
          pagination={{}}
          columns={[
            {
              title: t("repository.snapshot"),
              render: (_, item) => <>#{item.snapshot.sequence}</>,
            },
            {
              title: t("repository.state"),
              render: (_, item) => <StateBadge state={item.snapshot.state} />,
            },
            {
              title: t("repository.published2"),
              render: (_, item) => formatDate(item.snapshot.publishedAt),
            },
            {
              title: t("repository.action"),
              render: (_, item) => (
                <Button
                  loading={exportingId === item.snapshot.id}
                  disabled={exportBusy}
                  onClick={() => void exportArchive(item.snapshot)}
                >
                  {t("repository.exportArchive")}
                </Button>
              ),
            },
          ]}
        />
      </section>
      <section aria-label={t("repository.restoreFromArchive")}>
        <h4>{t("repository.restoreFromArchive")}</h4>
        <p>{t("repository.onlyAGatewayV1ArchiveBelongingToThis")}</p>
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
              {t("repository.chooseArchiveFile")}
            </Button>
          </Upload>
          {file && (
            <>
              <code>{file.name}</code>
              <Button type="link" disabled={busy} onClick={() => setFile(null)}>
                {t("repository.remove")}
              </Button>
            </>
          )}
        </div>
        <Field
          label={t("repository.backupReceiptSha256")}
          hint={t("repository.mustBeTheSha25664LowercaseHexReceipt")}
        >
          <Input
            aria-label={t("repository.backupReceiptSha256")}
            className="font-mono"
            value={receipt}
            placeholder="sha256:"
            disabled={busy}
            onChange={(event) => setReceipt(event.target.value)}
          />
        </Field>
        <Popconfirm
          title={t("repository.restoreFromThisArchive")}
          description={t(
            "repository.restoreWritesPackagesAndSignedIndexesAConflicting",
          )}
          okText={t("repository.restoreArchive")}
          cancelText={t("repository.cancel")}
          disabled={!restoreReady}
          onConfirm={() => void restoreArchive()}
        >
          <Button type="primary" loading={busy} disabled={!restoreReady}>
            {t("repository.restoreArchive")}
          </Button>
        </Popconfirm>
      </section>
    </div>
  );
}
