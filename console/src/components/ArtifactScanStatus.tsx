import { useCallback, useEffect, useRef, useState } from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Space } from "antd";
import {
  createRepositoryArtifactScan,
  getRepositoryArtifactScanStatus,
} from "../client";
import type { ArtifactScanStatus as ScanStatus } from "../client";
import { StateBadge } from "./Badge";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";

const activeStates = new Set(["pending", "running", "retrying"]);

export function ArtifactScanStatus({
  repositoryId,
  format,
  coordinate,
  digest,
}: {
  repositoryId?: string;
  format: string;
  coordinate: string;
  digest?: string;
}) {
  const { locale, text } = usePreferences();
  const [status, setStatus] = useState<ScanStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const requestSequence = useRef(0);

  const load = useCallback(async () => {
    if (!repositoryId || !coordinate || !digest) return;
    const requestID = ++requestSequence.current;
    setLoading(true);
    try {
      const response = await getRepositoryArtifactScanStatus({
        path: { repositoryId },
        query: { coordinate, digest },
      });
      if (requestID !== requestSequence.current) return;
      if (response.error || !response.data) {
        setLoadError(text("读取扫描状态失败", "Failed to load scan status"));
        return;
      }
      setLoadError("");
      setStatus(response.data);
    } catch {
      if (requestID === requestSequence.current) {
        setLoadError(text("读取扫描状态失败", "Failed to load scan status"));
      }
    } finally {
      if (requestID === requestSequence.current) setLoading(false);
    }
  }, [coordinate, digest, repositoryId, text]);

  useEffect(() => {
    requestSequence.current++;
    setError("");
    setLoadError("");
    setStatus(null);
    void load();
    return () => {
      // This monotonic generation ref invalidates any response that resolves
      // after an identity change or unmount; it is not a rendered-node ref.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      requestSequence.current++;
    };
  }, [load]);

  useEffect(() => {
    if (!status || !activeStates.has(status.state)) return;
    const timer = window.setInterval(() => void load(), 2500);
    return () => window.clearInterval(timer);
  }, [load, status]);

  if (!repositoryId || !digest) return null;

  const rescan = async () => {
    requestSequence.current++;
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
      setLoadError("");
      setStatus({
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
      <Alert
        className="col-span-full"
        type="warning"
        showIcon
        title={loadError}
        action={
          <Button size="small" loading={loading} onClick={() => void load()}>
            {text("重试", "Retry")}
          </Button>
        }
      />
    );

  const job = status.job;
  const canRescan = !activeStates.has(status.state);
  return (
    <div className="col-span-full border-b border-zinc-800/80 pb-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="text-zinc-500">
          {text("扫描状态", "Scan status")} · {format.toUpperCase()}
        </span>
        <StateBadge state={status.state} />
        {job?.completedAt && (
          <span className="text-zinc-500">
            {formatDate(job.completedAt, locale)}
          </span>
        )}
        {job?.attempts !== undefined && job.attempts > 0 && (
          <span className="text-zinc-500">
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
        <Alert className="mt-2" type="warning" showIcon title={error} />
      )}
      {loadError && (
        <Alert
          className="mt-2"
          type="warning"
          showIcon
          title={loadError}
          action={
            <Button size="small" loading={loading} onClick={() => void load()}>
              {text("重试", "Retry")}
            </Button>
          }
        />
      )}
      {job?.lastError && status.state === "failed" && (
        <div
          className="mt-2 truncate text-xs text-rose-400"
          title={job.lastError}
        >
          {job.lastError}
        </div>
      )}
    </div>
  );
}
