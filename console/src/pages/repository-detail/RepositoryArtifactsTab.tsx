import { useCallback, useEffect, useState } from "react";
import { Button, Checkbox, Input, Select, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  clearProxyNegativeCache,
  getProxyHealth,
  invalidateProxyCache,
  listConanReferences,
  listMavenCoordinates,
  listOciImages,
  listProxyCacheEntries,
  refreshProxyCache,
  searchRepositoryArtifacts,
} from "../../client";
import type { ProxyCacheAsset, Repository } from "../../client";
import { Badge } from "../../components/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/Feedback";
import { Pagination } from "../../components/Layout";
import { OciImageDetail } from "../../components/OciImageDetail";
import {
  ConanArtifactDetail,
  MavenArtifactDetail,
  RawArtifactDetail,
} from "../../components/ArtifactRowDetail";
import { NpmPackageDetail } from "../../components/NpmPackageDetail";
import { PyPIProjectDetail } from "../../components/PyPIProjectDetail";
import { GoModuleDetail } from "../../components/GoModuleDetail";
import { RawUploadDialog } from "../../components/RawUploadDialog";
import { useAuth } from "../../lib/auth";
import {
  formatBytes,
  formatDate,
  formatNumber,
  shortDigest,
} from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { mavenGA, mavenUsage, mavenVersion } from "../../lib/usage";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import {
  CopyButton,
  RepositorySnippetBlock as SnippetBlock,
} from "./RepositoryUsageGuides";

// 统一的制品行，按格式从不同端点归一化而来
interface ArtifactRow {
  key: string;
  coordinate: string;
  digest?: string;
  createdAt?: string;
  state?: string;
  size?: number;
  contentType?: string;
  publisher?: string;
  buildNumber?: number;
  // maven 聚合：同 group:artifact 的版本数与最新版本
  versionCount?: number;
  latestVersion?: string;
  fileCount?: number;
  primaryFiles?: string[];
  files?: ProxyMavenFile[];
}

async function fetchMavenArtifactPage(
  repositoryId: string,
  query: string,
  pageToken?: string,
): Promise<{
  items: ArtifactRow[];
  nextPageToken?: string;
  error: unknown;
}> {
  const response = await searchRepositoryArtifacts({
    path: { repositoryId },
    query: { q: query, pageSize: 50, pageToken },
  });
  return {
    items: (response.data?.items ?? []).map((item) => ({
      key: `${item.coordinate}-${item.buildNumber ?? 0}`,
      coordinate: item.coordinate,
      digest: item.digest,
      createdAt: item.createdAt,
      size: item.size,
      contentType: item.contentType,
      publisher: item.publisher,
      buildNumber: item.buildNumber,
    })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
}

type ProxyMavenFile = ProxyCacheAsset;

const PROXY_MAVEN_PAGE_SIZE = 50;
type ProxyMavenAssetFilter = "primary" | "all" | "jar" | "pom";

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

function ProxyMavenUsage({
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
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
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
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            {text("健康", "Health")}
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.reachable ? "text-emerald-300" : "text-rose-300"}`}
          >
            {health
              ? health.reachable
                ? `${text("可达", "Reachable")}${health.status ? ` · ${health.status}` : ""}`
                : health.error || text("不可达", "Unreachable")
              : text("检查中…", "Checking…")}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            Circuit
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.circuitOpen ? "text-rose-300" : "text-emerald-300"}`}
          >
            {health?.circuitOpen ? "open" : "closed"}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
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
        <div className="text-xs text-rose-300">{healthError}</div>
      )}
      <details className="group">
        <summary className="cursor-pointer text-sm font-medium text-zinc-100 hover:text-cyan-300">
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
          <div className="mt-2 text-xs text-rose-300">{warmError}</div>
        )}
        {warmResult && (
          <div
            className={`mt-2 text-xs ${warmResult.status < 400 ? "text-emerald-300" : "text-rose-300"}`}
          >
            HTTP {warmResult.status} · {formatBytes(warmResult.bytes)}
          </div>
        )}
        {refreshError && (
          <div className="mt-2 text-xs text-rose-300">{refreshError}</div>
        )}
        {refreshResult && (
          <div
            className={`mt-2 text-xs ${refreshResult.refreshed ? "text-emerald-300" : "text-rose-300"}`}
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
        <div className="mt-1 text-[11px] text-zinc-600">
          {text(
            "版本、组件和全部会按对应 Maven 缓存前缀失效；只删除缓存索引，字节对象由 Orphan Collector 延迟回收。",
            "Version, component, and repository invalidations use their Maven cache prefixes. Only cache indexes are removed; byte objects are reclaimed later by the orphan collector.",
          )}
        </div>
        {invalidateError && (
          <div className="mt-2 text-xs text-rose-300">{invalidateError}</div>
        )}
        {invalidateResult !== null && (
          <div className="mt-2 text-xs text-emerald-300">
            {text(
              `已失效 ${invalidateResult} 个缓存条目。`,
              `${invalidateResult} cache entries invalidated.`,
            )}
          </div>
        )}
        {negativeError && (
          <div className="mt-2 text-xs text-rose-300">{negativeError}</div>
        )}
        {negativeResult !== null && (
          <div className="mt-2 text-xs text-emerald-300">
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

function ProxyMavenCacheDetail({
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
              {file.sidecar && <Badge tone="zinc">checksum</Badge>}
            </div>
            <div className="mt-1 flex flex-wrap gap-3 text-[11px] text-zinc-500">
              <span>{formatBytes(file.size)}</span>
              {file.contentType && <span>{file.contentType}</span>}
              {file.digest && (
                <span className="font-mono">{shortDigest(file.digest)}</span>
              )}
            </div>
          </div>
          <CopyButton text={url} />
        </div>
        <code className="mt-1 block break-all font-mono text-[11px] leading-5 text-cyan-300">
          {url}
        </code>
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            {text("主文件大小", "Primary size")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {formatBytes(meta.size)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            {text("文件数", "Files")}
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {meta.fileCount ?? meta.files?.length ?? 0}
          </div>
        </div>
        {meta.publisher && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
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
            <div className="pl-8 text-cyan-300">└─ {parsed.version}</div>
            <div className="pl-12 text-zinc-500">
              {text("主文件：", "Primary files: ")}
              {meta.primaryFiles?.join(", ") || "—"}
            </div>
          </div>
          <details className="group">
            <summary className="cursor-pointer text-sm font-medium text-zinc-200 hover:text-cyan-300">
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
        <div className="border-b border-zinc-800 px-3 py-2 text-[10px] uppercase tracking-wider text-zinc-500">
          {text("文件明细", "File details")}
        </div>
        <div className="divide-y divide-zinc-800/70">
          {primary.map(renderFile)}
          {sidecars.length > 0 && (
            <details className="group">
              <summary className="cursor-pointer px-3 py-2 text-xs text-zinc-500 hover:text-zinc-300">
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

export function RepositoryArtifactsTab({
  repo,
  canWrite,
  artifactTarget = "",
  buildTarget,
  referenceTarget,
  versionTarget,
  onVersionChange,
}: {
  repo: Repository;
  canWrite: boolean;
  artifactTarget?: string;
  buildTarget?: number;
  referenceTarget?: string;
  versionTarget?: string;
  onVersionChange?: (coordinate: string, version: string) => void;
}) {
  const { token } = useAuth();
  const { text } = usePreferences();
  const [q, setQ] = useState(artifactTarget);
  const [rows, setRows] = useState<ArtifactRow[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [proxyPage, setProxyPage] = useState(1);
  const [proxyTotal, setProxyTotal] = useState(0);
  const [proxyAssetFilter, setProxyAssetFilter] =
    useState<ProxyMavenAssetFilter>("primary");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [expandedImage, setExpandedImage] = useState<string | null>(null);

  const format = repo.format;
  const proxyMaven = format === "maven" && repo.type === "proxy";
  const proxyNpm = format === "npm" && repo.type === "proxy";
  const proxyPyPI = format === "pypi" && repo.type === "proxy";
  const proxyGo = format === "go" && repo.type === "proxy";
  const canUploadRaw = format === "raw" && repo.type !== "proxy";

  const load = useCallback(
    async (query: string, pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const page = { pageSize: 50, pageToken };
      let items: ArtifactRow[] = [];
      let next: string | undefined;
      let err: unknown = null;

      if (format === "oci") {
        const r = await listOciImages({
          path: { repositoryId: repo.id },
          query: { q: query || undefined, ...page },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((x) => ({
          key: x.name,
          coordinate: x.name,
        }));
        next = r.data?.nextPageToken;
      } else if (format === "maven") {
        if (proxyMaven) {
          try {
            const { data: pageData, error: requestError } =
              await listProxyCacheEntries({
                path: { repositoryId: repo.id },
                query: {
                  groupBy: "version",
                  assetFilter: proxyAssetFilter,
                  q: query || undefined,
                  pageSize: PROXY_MAVEN_PAGE_SIZE,
                  pageToken,
                },
              });
            if (requestError || !pageData)
              throw new Error(
                text("读取 Proxy 缓存失败", "Failed to load proxy cache"),
              );
            setProxyTotal(pageData.totalEstimate);
            items = pageData.items.map((item) => {
              const primary =
                item.assets?.filter((asset) => !asset.sidecar) ?? [];
              return {
                key: item.key,
                coordinate: item.coordinate,
                latestVersion: item.version,
                size: item.size,
                contentType: item.extensions?.join(", ") || item.contentType,
                publisher: item.member,
                fileCount: item.assetCount,
                primaryFiles: primary.map((asset) => asset.name),
                files: item.assets,
              } satisfies ArtifactRow;
            });
            next = pageData.nextPageToken;
            setProxyPage((current) => (pageToken ? current + 1 : 1));
          } catch (e) {
            err = e;
          }
        } else if (query) {
          let result = await fetchMavenArtifactPage(repo.id, query, pageToken);
          err = result.error;
          items = result.items;
          next = result.nextPageToken;
          if (!pageToken && artifactTarget && buildTarget && !err) {
            let target = items.find(
              (item) =>
                item.coordinate === artifactTarget &&
                item.buildNumber === buildTarget,
            );
            while (!target && next) {
              result = await fetchMavenArtifactPage(repo.id, query, next);
              if (result.error) {
                err = result.error;
                break;
              }
              target = result.items.find(
                (item) =>
                  item.coordinate === artifactTarget &&
                  item.buildNumber === buildTarget,
              );
              next = result.nextPageToken;
            }
            if (target) {
              items = [target];
              next = undefined;
            }
          }
        } else {
          const r = await listMavenCoordinates({
            path: { repositoryId: repo.id },
            query: page,
          });
          err = r.error;
          // 按 group:artifact 聚合：每个制品一行，显示版本数与最新版本
          const byGA = new Map<
            string,
            {
              versions: {
                coordinate: string;
                digest?: string;
                createdAt?: string;
                publisher?: string;
              }[];
            }
          >();
          for (const x of r.data?.items ?? []) {
            const ga = mavenGA(x.coordinate) ?? x.coordinate;
            if (!byGA.has(ga)) byGA.set(ga, { versions: [] });
            byGA.get(ga)!.versions.push(x);
          }
          items = Array.from(byGA.entries()).map(([ga, { versions }]) => {
            const sorted = [...versions].sort((a, b) =>
              (b.createdAt ?? "").localeCompare(a.createdAt ?? ""),
            );
            const latest = sorted[0];
            return {
              key: ga,
              coordinate: ga,
              digest: latest?.digest,
              createdAt: latest?.createdAt,
              publisher: latest?.publisher,
              versionCount: versions.length,
              latestVersion:
                mavenVersion(latest?.coordinate ?? "") ?? undefined,
            };
          });
          next = r.data?.nextPageToken;
        }
      } else if (format === "npm" || format === "pypi" || format === "go") {
        const r = await searchRepositoryArtifacts({
          path: { repositoryId: repo.id },
          query: { q: query || undefined, ...page },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((item) => ({
          key: item.coordinate,
          coordinate: item.coordinate,
          digest: item.digest,
          createdAt: item.createdAt,
          size: item.size,
          publisher: item.publisher,
          versionCount: item.versionCount,
          latestVersion: item.version,
        }));
        next = r.data?.nextPageToken;
      } else if (format === "conan") {
        const r = await listConanReferences({
          path: { repositoryId: repo.id },
          query: page,
        });
        err = r.error;
        items = (r.data?.items ?? [])
          .filter((x) => !query || x.reference.includes(query))
          .map((x) => ({
            key: x.reference,
            coordinate: x.reference,
            publisher: x.publisher,
          }));
        next = r.data?.nextPageToken;
      } else {
        // raw：通用搜索端点
        const r = await searchRepositoryArtifacts({
          path: { repositoryId: repo.id },
          query: { q: query || undefined, ...page },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((x, i) => ({
          key: `${x.coordinate}-${i}`,
          coordinate: x.coordinate,
          digest: x.digest,
          createdAt: x.createdAt,
          size: x.size,
          contentType: x.contentType,
        }));
        next = r.data?.nextPageToken;
      }

      setLoading(false);
      if (err) {
        setError(err);
        return;
      }
      setRows((prev) => (pageToken ? [...prev, ...items] : items));
      setNextToken(next);
      if (!pageToken && artifactTarget) {
        const target = items.find(
          (item) =>
            item.coordinate === artifactTarget &&
            (!buildTarget || item.buildNumber === buildTarget),
        );
        setExpandedImage(target?.key ?? null);
      }
    },
    [
      repo.id,
      format,
      proxyMaven,
      proxyAssetFilter,
      artifactTarget,
      buildTarget,
      text,
    ],
  );

  useEffect(() => {
    setQ(artifactTarget);
    void load(artifactTarget);
  }, [load, artifactTarget]);

  const searchPlaceholder: Record<string, string> = {
    oci: text("按镜像名前缀过滤…", "Filter by image name prefix…"),
    maven: text("搜索 GAV 坐标…", "Search GAV coordinates…"),
    conan: text("按引用名过滤…", "Filter by reference…"),
    npm: text("按包名前缀过滤…", "Filter by package name prefix…"),
    pypi: text("按项目名前缀过滤…", "Filter by project name prefix…"),
    go: text("按模块路径前缀过滤…", "Filter by module path prefix…"),
    raw: text("搜索路径…", "Search paths…"),
  };

  const columns: ColumnsType<ArtifactRow> =
    format === "oci" || format === "conan"
      ? [
          {
            title: text("名称", "Name"),
            dataIndex: "coordinate",
            key: "coordinate",
            ellipsis: true,
            render: (value: string, record) => (
              <span
                className="font-mono text-xs text-zinc-200"
                title={record.coordinate}
              >
                {value}
              </span>
            ),
          },
        ]
      : proxyMaven
        ? [
            {
              title: text("Maven 坐标", "Maven coordinate"),
              dataIndex: "coordinate",
              key: "coordinate",
              ellipsis: true,
              render: (value: string, record) => (
                <span
                  className="font-mono text-xs text-zinc-200"
                  title={record.coordinate}
                >
                  {value}
                </span>
              ),
            },
            {
              title: text("文件", "Files"),
              key: "fileCount",
              width: 120,
              render: (_, record) => (
                <Badge tone="zinc">
                  {text(
                    `${record.fileCount ?? record.files?.length ?? 0} 个文件`,
                    `${record.fileCount ?? record.files?.length ?? 0} files`,
                  )}
                </Badge>
              ),
            },
            {
              title: text("主文件大小", "Primary size"),
              key: "size",
              width: 140,
              render: (_, record) => (
                <span className="text-xs text-zinc-400">
                  {formatBytes(record.size)}
                </span>
              ),
            },
            {
              title: text("类型", "Type"),
              dataIndex: "contentType",
              key: "contentType",
              width: 180,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span className="text-xs text-zinc-500">{value ?? "—"}</span>
              ),
            },
          ]
        : format === "maven" ||
            format === "npm" ||
            format === "pypi" ||
            format === "go"
          ? [
              {
                title:
                  format === "npm"
                    ? text("包", "Package")
                    : format === "pypi"
                      ? text("项目", "Project")
                      : format === "go"
                        ? text("模块", "Module")
                        : text("制品", "Artifact"),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string, record) => (
                  <span
                    className="font-mono text-xs text-zinc-200"
                    title={record.coordinate}
                  >
                    {value}
                  </span>
                ),
              },
              {
                title: text("版本", "Versions"),
                key: "versionCount",
                width: 120,
                render: (_, record) => (
                  <Badge tone="zinc">
                    {record.versionCount !== undefined
                      ? text(
                          `${record.versionCount} 个版本`,
                          `${record.versionCount} versions`,
                        )
                      : text("展开查看", "Open to inspect")}
                  </Badge>
                ),
              },
              {
                title: text("最新版本", "Latest version"),
                dataIndex: "latestVersion",
                key: "latestVersion",
                width: 160,
                render: (value: string | undefined) => (
                  <span className="font-mono text-xs text-cyan-300">
                    {value ?? "—"}
                  </span>
                ),
              },
              {
                title: text("更新时间", "Updated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-zinc-500">
                    {formatDate(value)}
                  </span>
                ),
              },
            ]
          : [
              {
                title: text("坐标", "Coordinate"),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string, record) => (
                  <span
                    className="font-mono text-xs text-zinc-200"
                    title={record.coordinate}
                  >
                    {value}
                  </span>
                ),
              },
              {
                title: text("摘要", "Digest"),
                dataIndex: "digest",
                key: "digest",
                width: 180,
                ellipsis: true,
                render: (value: string | undefined) => (
                  <span
                    className="font-mono text-xs text-zinc-500"
                    title={value}
                  >
                    {shortDigest(value)}
                  </span>
                ),
              },
              {
                title: text("大小", "Size"),
                dataIndex: "size",
                key: "size",
                width: 120,
                render: (value: number | undefined) => (
                  <span className="text-xs text-zinc-400">
                    {formatBytes(value)}
                  </span>
                ),
              },
              {
                title: text("最后更新时间", "Last updated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-zinc-500">
                    {formatDate(value)}
                  </span>
                ),
              },
            ];

  const expandedRowRender = (r: ArtifactRow) => {
    if (format === "oci") {
      return (
        <OciImageDetail
          repositoryId={repo.id}
          repository={repo.name}
          image={r.coordinate}
          initialReference={referenceTarget}
          onDeleted={() => void load(q)}
        />
      );
    }
    if (format === "maven" && !proxyMaven) {
      return (
        <MavenArtifactDetail
          repoId={repo.id}
          repoName={repo.name}
          onDeleted={() => void load(q)}
          meta={{
            coordinate: r.coordinate,
            digest: r.digest,
            size: r.size,
            createdAt: r.createdAt,
            publisher: r.publisher,
            buildNumber: r.buildNumber,
          }}
        />
      );
    }
    if (proxyMaven) {
      return <ProxyMavenCacheDetail repoName={repo.name} meta={r} />;
    }
    if (format === "npm") {
      return (
        <NpmPackageDetail
          repoName={repo.name}
          packageName={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "pypi") {
      return (
        <PyPIProjectDetail
          repoName={repo.name}
          project={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "go") {
      return (
        <GoModuleDetail
          repoName={repo.name}
          modulePath={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "conan") {
      return (
        <ConanArtifactDetail
          repoId={repo.id}
          repoName={repo.name}
          managed={repo.type !== "proxy"}
          canDelete={repo.type !== "proxy" && canWrite}
          onDeleted={() => void load(q)}
          meta={{ coordinate: r.coordinate, publisher: r.publisher }}
        />
      );
    }
    return (
      <RawArtifactDetail
        repoName={repo.name}
        onDeleted={() => void load(q)}
        meta={{
          coordinate: r.coordinate,
          digest: r.digest,
          size: r.size,
          createdAt: r.createdAt,
        }}
      />
    );
  };

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Input.Search
          allowClear
          className="w-80"
          placeholder={searchPlaceholder[format] ?? text("搜索…", "Search…")}
          value={q}
          onChange={(e) => {
            const value = e.target.value;
            setQ(value);
            if (!value) {
              setExpandedImage(null);
              void load("");
            }
          }}
          onSearch={(value) => {
            setQ(value);
            setExpandedImage(null);
            void load(value);
          }}
          enterButton={text("搜索", "Search")}
        />
        {canUploadRaw && (
          <RawUploadDialog repo={repo} onUploaded={() => load(q)} />
        )}
        {proxyMaven && (
          <>
            <Select
              className="w-28"
              value={proxyAssetFilter}
              options={[
                { value: "primary", label: text("主资产", "Primary assets") },
                { value: "all", label: text("全部文件", "All files") },
                { value: "jar", label: text("仅 JAR", "JAR only") },
                { value: "pom", label: text("仅 POM", "POM only") },
              ]}
              onChange={(value: ProxyMavenAssetFilter) => {
                setProxyAssetFilter(value);
                setExpandedImage(null);
              }}
            />
            <span className="text-xs text-zinc-500">
              {text(
                `${formatNumber(proxyTotal)} 个 Maven 版本，当前显示 ${formatNumber(rows.length)} 个`,
                `${formatNumber(proxyTotal)} Maven versions, showing ${formatNumber(rows.length)}`,
              )}
            </span>
          </>
        )}
        {q && (
          <Button
            type="text"
            size="small"
            onClick={() => {
              setQ("");
              setExpandedImage(null);
              void load("");
            }}
          >
            {text("返回完整列表", "Return to full list")}
          </Button>
        )}
      </div>
      {error !== null ? (
        isNotFound(error) ? (
          <RepositoryFeatureUnavailable
            feature={text("制品浏览", "Artifact browser")}
          />
        ) : (
          <ErrorBanner error={error} onRetry={() => load(q)} />
        )
      ) : loading ? (
        <Loading />
      ) : rows.length === 0 ? (
        <>
          {proxyMaven && (
            <ProxyMavenUsage
              repoId={repo.id}
              repoName={repo.name}
              token={token}
              onWarmed={() => void load(q)}
            />
          )}
          <EmptyState
            title={
              q
                ? text("没有匹配的制品", "No matching artifacts")
                : text("暂无制品", "No artifacts")
            }
            hint={
              q
                ? text("换个关键词试试", "Try a different search term")
                : proxyMaven
                  ? text(
                      "通过 Maven 客户端拉取依赖后会显示代理缓存",
                      "The proxy cache appears after a Maven client retrieves dependencies.",
                    )
                  : proxyNpm
                    ? text(
                        "通过 npm install 拉取包后会显示上游元数据与缓存状态",
                        "Upstream metadata and cache status appear after npm install retrieves a package.",
                      )
                    : proxyPyPI
                      ? text(
                          "通过 pip install 拉取项目后会显示上游文件与缓存状态",
                          "Upstream files and cache status appear after pip install retrieves a project.",
                        )
                      : proxyGo
                        ? text(
                            "通过 go mod download 拉取模块后会显示上游版本与缓存资产",
                            "Upstream versions and cached assets appear after go mod download retrieves a module.",
                          )
                        : text(
                            `通过 ${format} 客户端推送制品后会显示在这里`,
                            "Push artifacts with the matching client to display them here.",
                          )
            }
          />
        </>
      ) : (
        <>
          {proxyMaven && (
            <ProxyMavenUsage
              repoId={repo.id}
              repoName={repo.name}
              token={token}
              onWarmed={() => void load(q)}
            />
          )}
          <Table<ArtifactRow>
            className="ag-console-table"
            rowKey="key"
            size="middle"
            dataSource={rows}
            columns={columns}
            pagination={false}
            scroll={{
              x: format === "oci" || format === "conan" ? 520 : 980,
              y: "calc(100vh - 350px)",
            }}
            expandable={{
              expandedRowKeys: expandedImage ? [expandedImage] : [],
              expandedRowRender,
              expandRowByClick: true,
              onExpand: (expanded, record) =>
                setExpandedImage(expanded ? record.key : null),
            }}
          />
          <Pagination hasMore={!!nextToken} onMore={() => load(q, nextToken)} />
          {proxyMaven && proxyTotal > 0 && (
            <div className="border-t border-zinc-800/60 px-4 py-2 text-center text-xs text-zinc-500">
              {text(
                `第 ${formatNumber(proxyPage)} 页，每页 ${formatNumber(PROXY_MAVEN_PAGE_SIZE)} 个 Maven 版本`,
                `Page ${formatNumber(proxyPage)} with ${formatNumber(PROXY_MAVEN_PAGE_SIZE)} Maven versions per page`,
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
