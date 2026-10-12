import { useState } from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { Button, Space } from "antd";
import { createRepositoryArtifactScan } from "../../client";
import type { ArtifactScanStatus as ScanStatus } from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ErrorBanner } from "../../components/ui/Feedback";
import { useQueryClient } from "@tanstack/react-query";
import {
  artifactScanStatusKey,
  useArtifactScanStatus,
} from "./artifactDetailQueries";

const activeStates = new Set(["pending", "running", "retrying"]);

type ScanStatusProps = {
  repositoryId?: string;
  format: string;
  coordinate: string;
  digest?: string;
};

export function ArtifactScanStatus(props: ScanStatusProps) {
  // A rescan error belongs to one artifact; another one starts afresh.
  return (
    <ScanStatusPanel
      key={`${props.repositoryId}:${props.coordinate}:${props.digest}`}
      {...props}
    />
  );
}

function ScanStatusPanel({
  repositoryId,
  format,
  coordinate,
  digest,
}: ScanStatusProps) {
  const { locale, text } = usePreferences();
  const queryClient = useQueryClient();
  const scan = useArtifactScanStatus(repositoryId, coordinate, digest);
  const status = scan.data ?? null;
  const loading = scan.isFetching;
  const loadError = scan.error
    ? text("读取扫描状态失败", "Failed to load scan status")
    : "";
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState("");
  const load = () => scan.refetch();

  if (!repositoryId || !digest) return null;

  const rescan = async () => {
    const key = artifactScanStatusKey(repositoryId, coordinate, digest);
    // A status read that started before the rescan must not replace it.
    await queryClient.cancelQueries({ queryKey: key });
    setScanning(true);
    setError("");
    try {
      const response = await createRepositoryArtifactScan({
        path: { repositoryId },
        headers: { "Idempotency-Key": `manual-scan:${crypto.randomUUID()}` },
        body: { coordinate, digest },
      });
      if (response.error || !response.data) {
        setError(text("重新扫描入队失败", "Failed to queue a rescan"));
        return;
      }
      queryClient.setQueryData<ScanStatus>(key, {
        coordinate,
        digest,
        state: response.data.state,
        job: response.data,
      });
    } catch {
      setError(text("重新扫描入队失败", "Failed to queue a rescan"));
    } finally {
      setScanning(false);
    }
  };

  if (!status && !loadError) return null;
  if (!status)
    return (
      <div className="col-span-full">
        <ErrorBanner error={loadError} onRetry={() => void load()} />
      </div>
    );

  const job = status.job;
  const canRescan = !activeStates.has(status.state);
  return (
    <div className="col-span-full border-b border-line pb-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="text-fg-tertiary">
          {text("扫描状态", "Scan status")} · {format.toUpperCase()}
        </span>
        <StateBadge state={status.state} />
        {job?.completedAt && (
          <span className="text-fg-tertiary">
            {formatDate(job.completedAt, locale)}
          </span>
        )}
        {job?.attempts !== undefined && job.attempts > 0 && (
          <span className="text-fg-tertiary">
            {text(
              `尝试 ${job.attempts}/${job.maxAttempts}`,
              `Attempt ${job.attempts}/${job.maxAttempts}`,
            )}
          </span>
        )}
        <Space size={6} className="ml-auto">
          {canRescan && (
            <Button
              size="small"
              type="text"
              icon={<ReloadOutlined />}
              loading={scanning || loading}
              onClick={() => void rescan()}
            >
              {text("重新扫描", "Rescan")}
            </Button>
          )}
        </Space>
      </div>
      {error && (
        <div className="mt-2">
          <ErrorBanner error={error} />
        </div>
      )}
      {loadError && (
        <div className="mt-2">
          <ErrorBanner
            error={loadError}
            tone="warning"
            onRetry={() => void load()}
          />
        </div>
      )}
      {job?.lastError && status.state === "failed" && (
        <div
          className="mt-2 truncate text-xs text-[var(--ag-status-danger)]"
          title={job.lastError}
        >
          {job.lastError}
        </div>
      )}
    </div>
  );
}
