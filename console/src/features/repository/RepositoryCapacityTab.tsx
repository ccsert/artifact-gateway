import { useState } from "react";
import { Button, InputNumber, Progress, Space } from "antd";
import { replaceRepositoryCapacity } from "../../client";
import type { Repository } from "../../client";
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
import { useQueryClient } from "@tanstack/react-query";
import { repositoryKeys } from "../../lib/query/keys";
import { useRepositoryCapacity } from "./repositoryQueries";

export function RepositoryCapacityTab({ repo }: { repo: Repository }) {
  return <RepositoryCapacityContent key={repo.id} repo={repo} />;
}

function RepositoryCapacityContent({ repo }: { repo: Repository }) {
  const { resolvedTheme, t } = usePreferences();
  const queryClient = useQueryClient();
  const capacityQuery = useRepositoryCapacity(repo.id);
  const capacity = capacityQuery.data ?? null;
  const error = capacityQuery.error;
  // The quota field edits a draft; without one it shows the saved quota.
  const [quotaDraft, setQuotaGiB] = useState<number | null>(null);
  const quotaGiB =
    quotaDraft ?? Math.round((capacity?.quotaBytes ?? 0) / 2 ** 30);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const load = () => capacityQuery.refetch();

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
      setNotice(t("repository.quotaUpdated"));
      setQuotaGiB(null);
      void queryClient.invalidateQueries({
        queryKey: repositoryKeys.capacity(repo.id),
      });
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  if (error !== null && !capacity)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={t("repository.capacityManagement")}
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
              ? t("repository.cacheUsage")
              : t("repository.usedSpace"),
            value: formatBytes(capacity.usedBytes),
          },
          {
            label: proxy
              ? t("repository.cachedObjects")
              : t("repository.objectCount"),
            value: formatNumber(capacity.objectCount),
          },
          {
            label: t("repository.quota"),
            value:
              capacity.quotaBytes > 0
                ? formatBytes(capacity.quotaBytes)
                : t("repository.unlimited"),
          },
        ]}
      />
      {capacity.quotaBytes > 0 && (
        <div>
          <div className="mb-1.5 flex justify-between text-xs text-fg-tertiary">
            <span>{t("repository.utilization")}</span>
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
        <Field label={t("repository.quotaGiB0ForUnlimited")}>
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
          {t("repository.updateQuota")}
        </Button>
      </div>
    </div>
  );
}
