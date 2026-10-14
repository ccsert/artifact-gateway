import { useState } from "react";
import { Button, Input } from "antd";
import { refreshProxyCache } from "../../client";
import { formatBytes } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

import { mavenWarmPath } from "./proxyMavenPaths";
import { useInvalidateProxyHealth } from "./repositoryQueries";

/** Warm one artifact into the cache, or refresh cached metadata. */
export function ProxyMavenWarmCard({
  repoId,
  repoName,
  token,
  onWarmed,
}: {
  repoId: string;
  repoName: string;
  token: string;
  onWarmed: () => void;
}) {
  const { t } = usePreferences();
  const refreshHealth = useInvalidateProxyHealth(repoId);
  const [warmInput, setWarmInput] = useState(
    "org.springframework.boot:spring-boot:3.4.4:pom",
  );
  const [warming, setWarming] = useState(false);
  const [warmResult, setWarmResult] = useState<{
    status: number;
    bytes: number;
  } | null>(null);
  const [warmError, setWarmError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [refreshResult, setRefreshResult] = useState<{
    status: number;
    size?: number;
    refreshed: boolean;
  } | null>(null);
  const [refreshError, setRefreshError] = useState("");

  const warm = async () => {
    const path = mavenWarmPath(warmInput);
    if (!path) {
      setWarmError(
        t("repository.enterAMavenGAVGroupIdArtifactIdVersionExtension"),
      );
      return;
    }
    setWarming(true);
    setWarmError("");
    setWarmResult(null);
    try {
      const response = await fetch(`/maven/${repoName}/${path}`, {
        credentials: "include",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      });
      const body = await response.arrayBuffer();
      setWarmResult({ status: response.status, bytes: body.byteLength });
      if (response.ok) onWarmed();
    } catch (error) {
      setWarmError(
        error instanceof Error
          ? error.message
          : t("repository.cacheWarmUpRequestFailed"),
      );
    } finally {
      setWarming(false);
    }
  };

  const refresh = async () => {
    const value = warmInput.trim();
    if (!value) {
      setRefreshError(t("repository.enterAMavenGAVOrCachePath"));
      return;
    }
    setRefreshing(true);
    setRefreshError("");
    setRefreshResult(null);
    try {
      const body = value.includes("/")
        ? { path: value.replace(/^\/+/, "") }
        : { gav: value };
      const { data: result, error } = await refreshProxyCache({
        path: { repositoryId: repoId },
        body,
      });
      if (error || !result)
        throw new Error(t("repository.failedToRefreshCache"));
      setRefreshResult(result);
      onWarmed();
      void refreshHealth();
    } catch (error) {
      setRefreshError(
        error instanceof Error
          ? error.message
          : t("repository.failedToRefreshCache"),
      );
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-3 py-3">
      <div className="mb-2 text-sm font-medium text-fg">
        {t("repository.warmCache")}
      </div>
      <div className="flex flex-wrap gap-2">
        <Input
          className="min-w-80 flex-1 font-mono"
          placeholder="org.springframework.boot:spring-boot:3.4.4:pom"
          value={warmInput}
          onChange={(e) => setWarmInput(e.target.value)}
        />
        <Button onClick={warm} loading={warming} disabled={!warmInput.trim()}>
          {t("repository.warm")}
        </Button>
        <Button
          onClick={refresh}
          loading={refreshing}
          disabled={!warmInput.trim()}
        >
          {t("repository.forceRefresh")}
        </Button>
        <Button onClick={() => void refreshHealth()}>
          {t("repository.checkUpstream")}
        </Button>
      </div>
      {warmError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {warmError}
        </div>
      )}
      {warmResult && (
        <div
          className={`mt-2 text-xs ${warmResult.status < 400 ? "text-[var(--ag-status-success)]" : "text-[var(--ag-status-danger)]"}`}
        >
          HTTP {warmResult.status} · {formatBytes(warmResult.bytes)}
        </div>
      )}
      {refreshError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {refreshError}
        </div>
      )}
      {refreshResult && (
        <div
          className={`mt-2 text-xs ${refreshResult.refreshed ? "text-[var(--ag-status-success)]" : "text-[var(--ag-status-danger)]"}`}
        >
          {t("repository.refresh")} HTTP {refreshResult.status}
          {refreshResult.size !== undefined
            ? ` · ${formatBytes(refreshResult.size)}`
            : ""}
        </div>
      )}
    </div>
  );
}
