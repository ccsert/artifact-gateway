import { useCallback, useEffect, useState } from "react";
import { Button, Checkbox, Input, Select } from "antd";
import {
  clearProxyNegativeCache,
  getProxyHealth,
  invalidateProxyCache,
  refreshProxyCache,
} from "../../client";
import { Badge } from "../../components/Badge";
import { formatBytes, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { mavenUsage } from "../../lib/usage";
import {
  CopyButton,
  RepositorySnippetBlock as SnippetBlock,
} from "./RepositoryUsageGuides";
import type { ArtifactRow, ProxyMavenFile } from "./artifactRow";

export const PROXY_MAVEN_PAGE_SIZE = 50;

export type ProxyMavenAssetFilter = "primary" | "all" | "jar" | "pom";

function mavenWarmPath(input: string): string | null {
  const value = input.trim();
  if (!value) return null;
  if (value.includes("/")) return value.replace(/^\/+/, "");
  const parts = value.split(":");
  if (parts.length < 3) return null;
  const [groupId, artifactId, version, extension = "jar", classifier] = parts;
  const suffix = classifier ? `-${classifier}` : "";
  return `${groupId.replaceAll(".", "/")}/${artifactId}/${version}/${artifactId}-${version}${suffix}.${extension}`;
}

type ProxyHealth = {
  endpoint: string;
  reachable: boolean;
  status?: number;
  error?: string;
  proxyAllowed: boolean;
  circuitOpen: boolean;
  cacheEnabled: boolean;
  checkedAt: string;
};

export function ProxyMavenUsage({
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
  const base = window.location.origin;
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
  const [health, setHealth] = useState<ProxyHealth | null>(null);
  const [healthError, setHealthError] = useState("");
  const [invalidateInput, setInvalidateInput] = useState("");
  const [invalidateScope, setInvalidateScope] = useState<
    "path" | "version" | "component" | "repository"
  >("path");
  const [invalidatePrefix, setInvalidatePrefix] = useState(false);
  const [invalidating, setInvalidating] = useState(false);
  const [invalidateResult, setInvalidateResult] = useState<number | null>(null);
  const [invalidateError, setInvalidateError] = useState("");
  const [clearingNegative, setClearingNegative] = useState(false);
  const [negativeResult, setNegativeResult] = useState<number | null>(null);
  const [negativeError, setNegativeError] = useState("");
  const settings = `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <servers>
    <server>
      <id>${repoName}</id>
      <username>resolver</username>
      <password>\${env.GATEWAY_RESOLVER_TOKEN}</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>${repoName}</id>
      <mirrorOf>*</mirrorOf>
      <url>${base}/maven/${repoName}</url>
    </mirror>
  </mirrors>
</settings>`;
  const docker = `docker run --rm \\
  -e GATEWAY_RESOLVER_TOKEN=<resolver-token> \\
  -v "$PWD/settings.xml:/root/.m2/settings.xml:ro" \\
  -v "$PWD:/workspace" -w /workspace \\
  maven:3.9-eclipse-temurin-21 mvn dependency:go-offline`;
  const direct = `curl -u resolver:<resolver-token> \\
  ${base}/maven/${repoName}/org/springframework/boot/spring-boot/3.4.4/spring-boot-3.4.4.pom`;

  const loadHealth = useCallback(async () => {
    setHealthError("");
    try {
      const { data, error } = await getProxyHealth({
        path: { repositoryId: repoId },
      });
      if (error || !data)
        throw new Error(
          text("读取上游状态失败", "Failed to load upstream status"),
        );
      setHealth(data);
    } catch (error) {
      setHealthError(
        error instanceof Error
          ? error.message
          : text("读取上游状态失败", "Failed to load upstream status"),
      );
    }
  }, [repoId, text]);

  useEffect(() => {
    void loadHealth();
  }, [loadHealth]);

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
      void loadHealth();
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

  const invalidate = async () => {
    const value = invalidateInput.trim();
    if (invalidateScope !== "repository" && !value) {
      setInvalidateError(
        text("请输入失效目标。", "Enter an invalidation target."),
      );
      return;
    }
    setInvalidating(true);
    setInvalidateError("");
    setInvalidateResult(null);
    try {
      const body = {
        path: value,
        scope: invalidateScope,
        prefix: invalidateScope === "path" && invalidatePrefix,
      };
      const { data: result, error } = await invalidateProxyCache({
        path: { repositoryId: repoId },
        body,
      });
      if (error || !result)
        throw new Error(text("失效缓存失败", "Failed to invalidate cache"));
      setInvalidateResult(result.invalidated);
      onWarmed();
    } catch (error) {
      setInvalidateError(
        error instanceof Error
          ? error.message
          : text("失效缓存失败", "Failed to invalidate cache"),
      );
    } finally {
      setInvalidating(false);
    }
  };

  const clearNegative = async () => {
    const path = invalidateInput.trim() ? mavenWarmPath(invalidateInput) : null;
    if (invalidateInput.trim() && !path) {
      setNegativeError(
        text(
          "请输入 Maven GAV 或缓存路径。",
          "Enter a Maven GAV or cache path.",
        ),
      );
      return;
    }
    setClearingNegative(true);
    setNegativeError("");
    setNegativeResult(null);
    try {
      const { data: result, error } = await clearProxyNegativeCache({
        path: { repositoryId: repoId },
        body: { ...(path ? { path } : {}), prefix: invalidatePrefix },
      });
      if (error || !result)
        throw new Error(
          text("清理负缓存失败", "Failed to clear negative cache"),
        );
      setNegativeResult(result.cleared);
      onWarmed();
    } catch (error) {
      setNegativeError(
        error instanceof Error
          ? error.message
          : text("清理负缓存失败", "Failed to clear negative cache"),
      );
    } finally {
      setClearingNegative(false);
    }
  };

  return (
    <div className="mb-5 space-y-3 rounded-lg border border-zinc-800 bg-zinc-900/40 p-4">
      <div className="grid gap-3 lg:grid-cols-4">
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("上游", "Upstream")}
          </div>
          <div
            className="mt-1 truncate font-mono text-xs text-zinc-200"
            title={health?.endpoint}
          >
            {health?.endpoint ?? text("检查中…", "Checking…")}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("健康", "Health")}
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.reachable ? "text-[var(--ag-status-success)]" : "text-[var(--ag-status-danger)]"}`}
          >
            {health
              ? health.reachable
                ? `${text("可达", "Reachable")}${health.status ? ` · ${health.status}` : ""}`
                : health.error || text("不可达", "Unreachable")
              : text("检查中…", "Checking…")}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            Circuit
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.circuitOpen ? "text-[var(--ag-status-danger)]" : "text-[var(--ag-status-success)]"}`}
          >
            {health?.circuitOpen ? "open" : "closed"}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("缓存", "Cache")}
          </div>
          <div className="mt-1 text-xs font-semibold text-zinc-100">
            {health?.cacheEnabled
              ? text("已启用", "Enabled")
              : text("已停用", "Disabled")}
          </div>
        </div>
      </div>
      {healthError && (
        <div className="text-xs text-[var(--ag-status-danger)]">
          {healthError}
        </div>
      )}
      <details className="group">
        <summary className="cursor-pointer text-sm font-medium text-[var(--ag-content-strong)] hover:text-[var(--ag-link-hover)]">
          {text("使用方法", "Usage")}{" "}
          <span className="text-xs text-zinc-600 group-open:hidden">
            {text("（展开）", "(expand)")}
          </span>
        </summary>
        <div className="mt-2 space-y-2">
          <div className="text-xs text-zinc-500">
            {text(
              "Maven 代理仓库需要 Basic 认证：用户名任意且非空，密码使用 resolver token。",
              "The Maven proxy requires Basic authentication: use any non-empty username and the resolver token as the password.",
            )}
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            <SnippetBlock label="settings.xml" code={settings} />
            <SnippetBlock label="Docker Maven" code={docker} />
            <SnippetBlock
              label={text("直接下载", "Direct download")}
              code={direct}
            />
          </div>
        </div>
      </details>
      <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-3">
        <div className="mb-2 text-sm font-medium text-zinc-200">
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
          <Button onClick={() => void loadHealth()}>
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
      <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-3">
        <div className="mb-2 text-sm font-medium text-zinc-200">
          {text("失效缓存", "Invalidate cache")}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            className="w-28"
            value={invalidateScope}
            options={[
              { value: "path", label: text("路径", "Path") },
              { value: "version", label: text("版本", "Version") },
              { value: "component", label: text("组件", "Component") },
              { value: "repository", label: text("全部", "Repository") },
            ]}
            onChange={(value: typeof invalidateScope) =>
              setInvalidateScope(value)
            }
          />
          <Input
            className="min-w-80 flex-1 font-mono"
            placeholder={
              invalidateScope === "component"
                ? "org.springframework.boot:spring-boot"
                : invalidateScope === "version"
                  ? "org.springframework.boot:spring-boot:3.4.4"
                  : "org/springframework/boot/spring-boot/3.4.4"
            }
            value={invalidateInput}
            onChange={(e) => setInvalidateInput(e.target.value)}
            disabled={invalidateScope === "repository"}
          />
          {invalidateScope === "path" && (
            <Checkbox
              checked={invalidatePrefix}
              onChange={(e) => setInvalidatePrefix(e.target.checked)}
            >
              {text("按前缀", "By prefix")}
            </Checkbox>
          )}
          <Button
            onClick={invalidate}
            loading={invalidating}
            disabled={
              invalidateScope !== "repository" && !invalidateInput.trim()
            }
          >
            {text("失效", "Invalidate")}
          </Button>
          <Button onClick={clearNegative} loading={clearingNegative}>
            {text("清理负缓存", "Clear negative cache")}
          </Button>
        </div>
        <div className="mt-1 text-xs text-zinc-600">
          {text(
            "版本、组件和全部会按对应 Maven 缓存前缀失效；只删除缓存索引，字节对象由 Orphan Collector 延迟回收。",
            "Version, component, and repository invalidations use their Maven cache prefixes. Only cache indexes are removed; byte objects are reclaimed later by the orphan collector.",
          )}
        </div>
        {invalidateError && (
          <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
            {invalidateError}
          </div>
        )}
        {invalidateResult !== null && (
          <div className="mt-2 text-xs text-[var(--ag-status-success)]">
            {text(
              `已失效 ${invalidateResult} 个缓存条目。`,
              `${invalidateResult} cache entries invalidated.`,
            )}
          </div>
        )}
        {negativeError && (
          <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
            {negativeError}
          </div>
        )}
        {negativeResult !== null && (
          <div className="mt-2 text-xs text-[var(--ag-status-success)]">
            {text(
              `已清理 ${negativeResult} 个负-cache 条目。`,
              `${negativeResult} negative-cache entries cleared.`,
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function parseMavenCachePath(path: string): {
  groupId: string;
  artifactId: string;
  version: string;
  fileName: string;
  coordinate: string;
} | null {
  const parts = path.split("/").filter(Boolean);
  if (parts.length < 4) return null;
  const fileName = parts.at(-1)!;
  const version = parts.at(-2)!;
  const artifactId = parts.at(-3)!;
  const groupParts = parts.slice(0, -3);
  if (
    groupParts.length === 0 ||
    !fileName.startsWith(`${artifactId}-${version}`)
  )
    return null;
  const groupId = groupParts.join(".");
  return {
    groupId,
    artifactId,
    version,
    fileName,
    coordinate: `${groupId}:${artifactId}:${version}`,
  };
}

export function ProxyMavenCacheDetail({
  repoName,
  meta,
}: {
  repoName: string;
  meta: ArtifactRow;
}) {
  const { text } = usePreferences();
  const parsed = meta.files?.[0]
    ? parseMavenCachePath(meta.files[0].path)
    : null;
  const primary = (meta.files ?? []).filter((file) => !file.sidecar);
  const sidecars = (meta.files ?? []).filter((file) => file.sidecar);
  const renderFile = (file: ProxyMavenFile) => {
    const url = `${window.location.origin}/maven/${repoName}/${file.path}`;
    return (
      <div key={file.path} className="px-3 py-2">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <code
                className="truncate font-mono text-xs text-zinc-200"
                title={file.name}
              >
                {file.name}
              </code>
              {file.sidecar && <Badge tone="neutral">checksum</Badge>}
            </div>
            <div className="mt-1 flex flex-wrap gap-3 text-xs text-zinc-500">
              <span>{formatBytes(file.size)}</span>
              {file.contentType && <span>{file.contentType}</span>}
              {file.digest && (
                <span className="font-mono">{shortDigest(file.digest)}</span>
              )}
            </div>
          </div>
          <CopyButton text={url} />
        </div>
        <code className="mt-1 block break-all font-mono text-xs leading-5 text-[var(--ag-content-secondary)]">
          {url}
        </code>
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("主文件大小", "Primary size")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {formatBytes(meta.size)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {text("文件数", "Files")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {meta.fileCount ?? meta.files?.length ?? 0}
          </div>
        </div>
        {meta.publisher && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {text("成员", "Member")}
            </div>
            <div
              className="mt-0.5 truncate font-mono text-xs text-zinc-100"
              title={meta.publisher}
            >
              {meta.publisher}
            </div>
          </div>
        )}
      </div>

      {parsed && (
        <>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-3 font-mono text-xs leading-6 text-zinc-300">
            <div className="text-zinc-500">{parsed.groupId}</div>
            <div className="pl-4 text-zinc-400">└─ {parsed.artifactId}</div>
            <div className="pl-8 text-[var(--ag-content-secondary)]">
              └─ {parsed.version}
            </div>
            <div className="pl-12 text-zinc-500">
              {text("主文件：", "Primary files: ")}
              {meta.primaryFiles?.join(", ") || "—"}
            </div>
          </div>
          <details className="group">
            <summary className="cursor-pointer text-sm font-medium text-[var(--ag-content-primary)] hover:text-[var(--ag-link-hover)]">
              {text("Maven 坐标用法", "Maven coordinate usage")}{" "}
              <span className="text-xs text-zinc-600 group-open:hidden">
                {text("（展开）", "(expand)")}
              </span>
            </summary>
            <div className="mt-2 grid gap-2 lg:grid-cols-3">
              {mavenUsage(repoName, parsed.coordinate).map((snippet) => (
                <SnippetBlock
                  key={snippet.label}
                  label={snippet.label}
                  code={snippet.code}
                />
              ))}
            </div>
          </details>
        </>
      )}

      <div className="rounded-lg border border-zinc-800 bg-zinc-950/60">
        <div className="border-b border-zinc-800 px-3 py-2 text-xs uppercase tracking-wider text-zinc-500">
          {text("文件明细", "File details")}
        </div>
        <div className="divide-y divide-zinc-800/70">
          {primary.map(renderFile)}
          {sidecars.length > 0 && (
            <details className="group">
              <summary className="cursor-pointer px-3 py-2 text-xs text-[var(--ag-content-tertiary)] hover:text-[var(--ag-content-secondary)]">
                {text(
                  `校验 / 签名文件（${sidecars.length}）`,
                  `Checksums / signatures (${sidecars.length})`,
                )}
              </summary>
              <div className="divide-y divide-zinc-800/70 border-t border-zinc-800/70">
                {sidecars.map(renderFile)}
              </div>
            </details>
          )}
        </div>
      </div>
    </div>
  );
}
