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
  const { text } = usePreferences();
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
        text(
          "请输入 Maven GAV（groupId:artifactId:version[:extension[:classifier]]）或仓库路径。",
          "Enter a Maven GAV (groupId:artifactId:version[:extension[:classifier]]) or a repository path.",
        ),
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
          : text("预热请求失败", "Cache warm-up request failed"),
      );
    } finally {
      setWarming(false);
    }
  };

  const refresh = async () => {
    const value = warmInput.trim();
    if (!value) {
      setRefreshError(
        text(
          "请输入 Maven GAV 或缓存路径。",
          "Enter a Maven GAV or cache path.",
        ),
      );
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
        throw new Error(text("刷新缓存失败", "Failed to refresh cache"));
      setRefreshResult(result);
      onWarmed();
      void refreshHealth();
    } catch (error) {
      setRefreshError(
        error instanceof Error
          ? error.message
          : text("刷新缓存失败", "Failed to refresh cache"),
      );
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-3 py-3">
      <div className="mb-2 text-sm font-medium text-fg">
        {text("预热缓存", "Warm cache")}
      </div>
      <div className="flex flex-wrap gap-2">
        <Input
          className="min-w-80 flex-1 font-mono"
          placeholder="org.springframework.boot:spring-boot:3.4.4:pom"
          value={warmInput}
          onChange={(e) => setWarmInput(e.target.value)}
        />
        <Button onClick={warm} loading={warming} disabled={!warmInput.trim()}>
          {text("预热", "Warm")}
        </Button>
        <Button
          onClick={refresh}
          loading={refreshing}
          disabled={!warmInput.trim()}
        >
          {text("强制刷新", "Force refresh")}
        </Button>
        <Button onClick={() => void refreshHealth()}>
          {text("检查上游", "Check upstream")}
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
          {text("刷新", "Refresh")} HTTP {refreshResult.status}
          {refreshResult.size !== undefined
            ? ` · ${formatBytes(refreshResult.size)}`
            : ""}
        </div>
      )}
    </div>
  );
}
