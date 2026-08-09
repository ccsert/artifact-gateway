import { useCallback, useEffect, useState } from "react";
import { Alert, Button, InputNumber, Progress, Space } from "antd";
import { getRepositoryCapacity, replaceRepositoryCapacity } from "../../client";
import type { Repository, RepositoryCapacity } from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/Feedback";
import { Field } from "../../components/Layout";
import { formatBytes, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";

export function RepositoryCapacityTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  type CapacityDetail = RepositoryCapacity & {
    primaryBytes?: number;
    sidecarBytes?: number;
    negativeCount?: number;
    expiredObjectCount?: number;
    reclaimableBytes?: number;
  };
  const [capacity, setCapacity] = useState<CapacityDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [quotaGiB, setQuotaGiB] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRepositoryCapacity({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setCapacity(data);
      setQuotaGiB(Math.round(data.quotaBytes / 2 ** 30));
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    setNotice("");
    const { error: err } = await replaceRepositoryCapacity({
      path: { repositoryId: repo.id },
      body: { quotaBytes: quotaGiB * 2 ** 30 },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice(text("配额已更新", "Quota updated"));
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("容量管理", "Capacity management")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!capacity) return <Loading />;

  const pct =
    capacity.quotaBytes > 0
      ? Math.min(100, (capacity.usedBytes / capacity.quotaBytes) * 100)
      : 0;
  const proxy = repo.type === "proxy";

  return (
    <div className="space-y-6">
      <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3 text-sm text-zinc-400">
        {proxy
          ? text(
              "Proxy 仓库的容量来自 read-through cache：已缓存的上游响应会计入缓存用量；它不是 Hosted 发布制品。",
              "A proxy repository's capacity comes from its read-through cache. Cached upstream responses count toward cache usage; they are not hosted published artifacts.",
            )
          : text(
              "Hosted 仓库的容量来自已发布或可恢复的制品/资产引用，并受发布配额约束。",
              "A hosted repository's capacity comes from published or recoverable artifact/asset references and is constrained by its publishing quota.",
            )}
      </div>
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {proxy
              ? text("缓存用量", "Cache usage")
              : text("已用空间", "Used space")}
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {formatBytes(capacity.usedBytes)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {proxy
              ? text("缓存对象", "Cached objects")
              : text("对象数量", "Object count")}
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {formatNumber(capacity.objectCount)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("配额", "Quota")}
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {capacity.quotaBytes > 0
              ? formatBytes(capacity.quotaBytes)
              : text("无限制", "Unlimited")}
          </div>
        </div>
      </div>
      {proxy && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {text("主资产缓存", "Primary asset cache")}
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.primaryBytes)}
            </div>
          </div>
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {text("校验/签名缓存", "Checksum/signature cache")}
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.sidecarBytes)}
            </div>
          </div>
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {text("可回收缓存", "Reclaimable cache")}
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.reclaimableBytes)}
            </div>
            <div className="mt-1 text-xs text-zinc-500">
              {text(
                `过期 ${formatNumber(capacity.expiredObjectCount)} 项 · negative ${formatNumber(capacity.negativeCount)} 项`,
                `Expired ${formatNumber(capacity.expiredObjectCount)} · negative ${formatNumber(capacity.negativeCount)}`,
              )}
            </div>
          </div>
        </div>
      )}
      {capacity.quotaBytes > 0 && (
        <div>
          <div className="mb-1.5 flex justify-between text-xs text-zinc-500">
            <span>{text("使用率", "Utilization")}</span>
            <span>{pct.toFixed(1)}%</span>
          </div>
          <Progress
            percent={pct}
            showInfo={false}
            status={pct > 90 ? "exception" : "normal"}
            strokeColor={pct > 70 && pct <= 90 ? "#f59e0b" : undefined}
          />
        </div>
      )}
      <div className="flex max-w-lg items-end gap-2">
        <Field
          label={text(
            "配额 (GiB，0 表示无限制)",
            "Quota (GiB, 0 for unlimited)",
          )}
        >
          <Space.Compact block>
            <InputNumber
              min={0}
              precision={0}
              className="w-full"
              value={quotaGiB}
              onChange={(value) => setQuotaGiB(value ?? 0)}
            />
            <Space.Addon>GiB</Space.Addon>
          </Space.Compact>
        </Field>
        <Button type="primary" onClick={save} loading={saving}>
          {text("更新配额", "Update quota")}
        </Button>
      </div>
    </div>
  );
}
