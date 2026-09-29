import { useCallback, useEffect, useState } from "react";
import { Button, InputNumber, Progress, Space } from "antd";
import { getRepositoryCapacity, replaceRepositoryCapacity } from "../../client";
import type { Repository, RepositoryCapacity } from "../../client";
import {
  ErrorBanner,
  Loading,
  Notice,
  isNotFound,
} from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { formatBytes, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { MetricStrip } from "../../components/ui/ConsolePrimitives";

export function RepositoryCapacityTab({ repo }: { repo: Repository }) {
  const { resolvedTheme, text } = usePreferences();
  const [capacity, setCapacity] = useState<RepositoryCapacity | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [quotaGiB, setQuotaGiB] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setError(null);
    try {
      const { data, error: err } = await getRepositoryCapacity({
        path: { repositoryId: repo.id },
      });
      if (err) throw err;
      if (data) {
        setCapacity(data);
        setQuotaGiB(Math.round(data.quotaBytes / 2 ** 30));
      }
    } catch (nextError) {
      setError(nextError);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    setNotice("");
    try {
      const { error: err } = await replaceRepositoryCapacity({
        path: { repositoryId: repo.id },
        body: { quotaBytes: quotaGiB * 2 ** 30 },
      });
      if (err) {
        setSaveError(err);
        return;
      }
      setNotice(text("配额已更新", "Quota updated"));
      void load();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  if (error !== null && !capacity)
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
    <div className="ag-page-stack">
      {error !== null && <ErrorBanner error={error} onRetry={load} />}
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      <MetricStrip
        items={[
          {
            label: proxy
              ? text("缓存用量", "Cache usage")
              : text("已用空间", "Used space"),
            value: formatBytes(capacity.usedBytes),
          },
          {
            label: proxy
              ? text("缓存对象", "Cached objects")
              : text("对象数量", "Object count"),
            value: formatNumber(capacity.objectCount),
          },
          {
            label: text("配额", "Quota"),
            value:
              capacity.quotaBytes > 0
                ? formatBytes(capacity.quotaBytes)
                : text("无限制", "Unlimited"),
          },
        ]}
      />
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
            strokeColor={
              pct > 70 && pct <= 90
                ? resolvedTheme.roles.status.warning.foreground
                : undefined
            }
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
