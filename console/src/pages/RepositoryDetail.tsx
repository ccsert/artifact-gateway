import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Input,
  InputNumber,
  Popconfirm,
  Popover,
  Progress,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tooltip,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  InfoCircleOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  getRepository,
  updateRepository,
  searchRepositoryArtifacts,
  listOciImages,
  listMavenCoordinates,
  listConanReferences,
  listGrants,
  replaceGrants,
  listUsers,
  listApiKeys,
  getRetentionPolicy,
  replaceRetentionPolicy,
  dryRunRepositoryRetention,
  executeRepositoryRetention,
  getRepositoryCapacity,
  replaceRepositoryCapacity,
  listRepositoryLifecycleJobs,
  listRepositoryTombstones,
  restoreRepositoryArtifact,
  getRepositoryCapabilities,
  listRepositories,
  createRepositoryPromotion,
  listRepositoryReplications,
  createRepositoryReplication,
  getRepositoryReplication,
  deleteRepositoryReplication,
  getRepositoryEffectiveAccess,
  listProxyCacheEntries,
  invalidateProxyCache,
  clearProxyNegativeCache,
  refreshProxyCache,
  getProxyHealth,
  testEgressProxy,
} from "../client";
import type {
  Repository,
  Grant,
  RetentionPolicy,
  RepositoryCapacity,
  LifecycleJob,
  ArtifactTombstone,
  RetentionDryRun,
  RepositoryCapabilities,
  RepositoryEffectiveAccess,
  ReplicationPlan,
  ReplicationPlanDetail,
  ProxyCacheAsset,
  User,
  ApiKey,
} from "../client";
import type { EgressProxyTestResult, EgressProxyWritable } from "../client";
import {
  PageHeader,
  Card,
  CardHeader,
  Pagination,
  Field,
} from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../components/Feedback";
import { FormatBadge, StateBadge, Badge } from "../components/Badge";
import { IdentitySummary } from "../components/IdentitySummary";
import { AccessDecisionSummary } from "../components/AccessDecisionSummary";
import { Modal, useDisclosure } from "../components/Modal";
import { OciImageDetail } from "../components/OciImageDetail";
import { MavenPublishWizard } from "../components/MavenPublishWizard";
import {
  MavenArtifactDetail,
  ConanArtifactDetail,
  RawArtifactDetail,
} from "../components/ArtifactRowDetail";
import { RawUploadDialog } from "../components/RawUploadDialog";
import { NpmPackageDetail } from "../components/NpmPackageDetail";
import { useAuth } from "../lib/auth";
import { usePreferences } from "../lib/preferences";
import { downloadCsv } from "../lib/csv";
import { mavenGA, mavenUsage, mavenVersion } from "../lib/usage";
import {
  formatBytes,
  formatDate,
  formatNumber,
  shortDigest,
} from "../lib/format";

type Tab =
  | "artifacts"
  | "publish"
  | "grants"
  | "retention"
  | "capacity"
  | "distribute"
  | "jobs"
  | "tombstones"
  | "settings";

type Localize = (chinese: string, english: string) => string;

const TABS: {
  key: Tab;
  label: string;
  labelEn: string;
  formats?: string[];
  hostedOnly?: boolean;
}[] = [
  { key: "artifacts", label: "制品", labelEn: "Artifacts" },
  {
    key: "publish",
    label: "发布",
    labelEn: "Publish",
    formats: ["maven", "npm"],
  },
  { key: "grants", label: "访问授权", labelEn: "Access grants" },
  {
    key: "retention",
    label: "保留策略",
    labelEn: "Retention",
    formats: ["maven", "oci", "conan", "raw"],
    hostedOnly: true,
  },
  { key: "capacity", label: "容量", labelEn: "Capacity" },
  {
    key: "distribute",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    formats: ["maven", "oci", "conan", "raw"],
  },
  {
    key: "jobs",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    formats: ["maven", "oci", "conan", "raw"],
  },
  {
    key: "tombstones",
    label: "墓碑",
    labelEn: "Tombstones",
    formats: ["maven", "oci", "conan", "raw"],
  },
  { key: "settings", label: "设置", labelEn: "Settings" },
];

function repositoryTabFromQuery(value: string | null): Tab {
  return TABS.find((tab) => tab.key === value)?.key ?? "artifacts";
}

/* ---------------- Artifacts ---------------- */

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

function CopyButton({ text }: { text: string }) {
  const { text: localize } = usePreferences();
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="text"
      size="small"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          /* ignore */
        }
      }}
      className="shrink-0"
    >
      {copied ? localize("已复制", "Copied") : localize("复制", "Copy")}
    </Button>
  );
}

function SnippetBlock({ label, code }: { label: string; code: string }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <div className="mb-1 flex items-center justify-between gap-3">
        <span className="text-[10px] uppercase tracking-wider text-zinc-500">
          {label}
        </span>
        <CopyButton text={code} />
      </div>
      <code className="block whitespace-pre-wrap break-all font-mono text-xs leading-5 text-cyan-300">
        {code}
      </code>
    </div>
  );
}

function NpmPublishGuide({ repoName }: { repoName: string }) {
  const { text } = usePreferences();
  const registry = `${window.location.origin}/npm/${repoName}/`;
  const authPath = `//${window.location.host}/npm/${repoName}/:_authToken=\${ARTIFACT_GATEWAY_TOKEN}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-zinc-100">
          {text("注册 npm 仓库", "Configure npm registry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-zinc-500">
          {text(
            "认证令牌使用 Gateway API Key 或 resolver token。",
            "Use a Gateway API key or resolver token for authentication.",
          )}
        </p>
      </div>
      <div className="space-y-3">
        <SnippetBlock
          label=".npmrc"
          code={`registry=${registry}\n${authPath}`}
        />
        <SnippetBlock
          label={text("发布", "Publish")}
          code={`npm publish --registry ${registry}`}
        />
      </div>
    </div>
  );
}

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

function ArtifactsTab({
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
      } else if (format === "npm") {
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
        : format === "maven" || format === "npm"
          ? [
              {
                title:
                  format === "npm"
                    ? text("包", "Package")
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
                    {text(
                      `${record.versionCount ?? 1} 个版本`,
                      `${record.versionCount ?? 1} versions`,
                    )}
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
          <NotEnabled feature={text("制品浏览", "Artifact browser")} />
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

/* ---------------- Grants ---------------- */

type GrantLevel = "read" | "write" | "admin";
const CUSTOM_PRINCIPAL = "__custom__";

interface PrincipalOption {
  value: string;
  label: string;
  detail: string;
  disabled?: boolean;
}

type PrincipalKind = "user" | "api-key" | "custom";

function principalKind(principal: string): PrincipalKind {
  if (principal.startsWith("user:")) return "user";
  if (principal.startsWith("api-key:")) return "api-key";
  return "custom";
}

function principalEditorKind(principal: string): PrincipalKind | "" {
  if (principal === CUSTOM_PRINCIPAL) return "custom";
  return principal ? principalKind(principal) : "";
}

function resourcePrefixHint(
  format: Repository["format"],
  text: Localize,
): string {
  switch (format) {
    case "maven":
      return text(
        "例如 org/example（Maven group 前缀）",
        "For example: org/example (Maven group prefix)",
      );
    case "oci":
      return text(
        "例如 team/backend（镜像名称前缀）",
        "For example: team/backend (image name prefix)",
      );
    case "conan":
      return text(
        "例如 pkg/1.0/user/stable（reference 前缀）",
        "For example: pkg/1.0/user/stable (reference prefix)",
      );
    case "raw":
      return text(
        "例如 releases/2026（路径前缀）",
        "For example: releases/2026 (path prefix)",
      );
    case "npm":
      return text(
        "例如 @scope/package（npm 包名前缀）",
        "For example: @scope/package (npm package prefix)",
      );
  }
}

function grantLevelLabel(level: GrantLevel, text: Localize): string {
  if (level === "admin") return text("管理员", "Administrator");
  if (level === "write") return text("写入", "Write");
  return text("读取", "Read");
}

function grantCapabilitiesLabel(level: GrantLevel, text: Localize): string {
  if (level === "admin")
    return text("读取 + 写入 + 管理", "Read + write + admin");
  if (level === "write") return text("读取 + 写入", "Read + write");
  return text("读取", "Read");
}

function grantLevel(scopes: Grant["scopes"]): GrantLevel {
  if (scopes.includes("repositories:admin")) return "admin";
  if (scopes.includes("repositories:write")) return "write";
  return "read";
}

function scopesForLevel(level: GrantLevel): Grant["scopes"] {
  return [`repositories:${level}`] as Grant["scopes"];
}

function principalOptions(
  users: User[],
  apiKeys: ApiKey[],
  text: Localize,
): PrincipalOption[] {
  return [
    ...users.map((user) => ({
      value: `user:${user.name}`,
      label: `${text("用户", "User")} · ${user.name}`,
      detail: `${text("全局角色", "Global role")} ${user.role}${user.state === "disabled" ? ` · ${text("已停用", "Disabled")}` : ""}`,
      disabled: user.state === "disabled",
    })),
    ...apiKeys.map((key) => ({
      value: `api-key:${key.id}`,
      label: `API Key · ${key.name}`,
      detail: `${text("全局角色", "Global role")} ${key.roles.join(", ")}${key.revokedAt ? ` · ${text("已撤销", "Revoked")}` : ""}`,
      disabled: Boolean(key.revokedAt),
    })),
  ];
}

function GrantsTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [principalChoices, setPrincipalChoices] = useState<PrincipalOption[]>(
    [],
  );
  const [principalChoicesError, setPrincipalChoicesError] =
    useState<unknown>(null);
  const [version, setVersion] = useState("");
  const editor = useDisclosure();
  const [draft, setDraft] = useState<Grant[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const {
      data,
      error: err,
      response,
    } = await listGrants({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    setGrants(data ?? []);
    const etag = response?.headers.get("ETag");
    setVersion(etag ? etag.replaceAll('"', "") : repo.version);
  }, [repo.id, repo.version]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [usersResult, apiKeysResult] = await Promise.all([
        listUsers(),
        listApiKeys(),
      ]);
      if (cancelled) return;
      if (usersResult.error || apiKeysResult.error) {
        setPrincipalChoicesError(
          new Error(
            text(
              "无法加载用户或 API Key 列表，可继续使用自定义身份。",
              "Could not load users or API keys. You can still enter a custom identity.",
            ),
          ),
        );
      }
      setPrincipalChoices(
        principalOptions(
          usersResult.data?.items ?? [],
          apiKeysResult.data?.items ?? [],
          text,
        ),
      );
    })();
    return () => {
      cancelled = true;
    };
  }, [text]);

  const openEditor = () => {
    setDraft(
      grants ? grants.map((g) => ({ ...g, scopes: [...g.scopes] })) : [],
    );
    setSaveError(null);
    editor.show();
  };

  const save = async () => {
    if (
      draft.some(
        (grant) =>
          !grant.principal.trim() || grant.principal === CUSTOM_PRINCIPAL,
      )
    ) {
      setSaveError(
        new Error(
          text(
            "请为每条授权规则选择或填写授权主体；不需要的空行请先移除。",
            "Select or enter a principal for every grant. Remove unused blank rows first.",
          ),
        ),
      );
      return;
    }
    const normalized = draft.map((grant) => ({
      ...grant,
      principal:
        grant.principal === CUSTOM_PRINCIPAL ? "" : grant.principal.trim(),
      scopes: scopesForLevel(grantLevel(grant.scopes)),
      resourcePrefix: grant.resourcePrefix?.trim() || undefined,
    }));
    const duplicate = new Set<string>();
    for (const grant of normalized) {
      const key = `${grant.principal}\x00${grant.resourcePrefix ?? ""}`;
      if (duplicate.has(key)) {
        setSaveError(
          new Error(
            text(
              "存在重复的授权主体与资源范围，请合并或删除重复规则。",
              "Duplicate principal and resource scope. Merge or remove the duplicate grant.",
            ),
          ),
        );
        return;
      }
      duplicate.add(key);
    }
    setSaving(true);
    setSaveError(null);
    const { error: err } = await replaceGrants({
      path: { repositoryId: repo.id },
      body: normalized,
      headers: { "If-Match": version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    editor.hide();
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature={text("访问授权", "Access grants")} />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!grants) return <Loading />;

  const grantColumns: ColumnsType<Grant> = [
    {
      title: text("授权主体", "Principal"),
      dataIndex: "principal",
      key: "principal",
      width: 320,
      render: (value: string) => (
        <div>
          <div className="font-mono text-xs text-zinc-200">
            {principalChoices.find((choice) => choice.value === value)?.label ??
              value}
          </div>
          <div className="mt-0.5 font-mono text-[10px] text-zinc-600">
            {value}
          </div>
        </div>
      ),
    },
    {
      title: text("权限级别", "Permission"),
      key: "level",
      width: 150,
      render: (_, grant) => (
        <Badge
          tone={
            grantLevel(grant.scopes) === "admin"
              ? "red"
              : grantLevel(grant.scopes) === "write"
                ? "amber"
                : "green"
          }
        >
          {grantLevelLabel(grantLevel(grant.scopes), text)}
        </Badge>
      ),
    },
    {
      title: text("资源范围", "Resource scope"),
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 260,
      render: (value?: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {value || text("整个仓库", "Entire repository")}
        </span>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <Button type="primary" icon={<EditOutlined />} onClick={openEditor}>
          {text("编辑授权", "Edit grants")}
        </Button>
      </div>
      {grants.length === 0 ? (
        <EmptyState
          title={text("暂无授权规则", "No access grants")}
          hint={text(
            "在编辑授权中选择用户、API Key，或填写 OIDC subject / 自定义 actor。",
            "Choose a user or API key in Edit grants, or enter an OIDC subject/custom actor.",
          )}
        />
      ) : (
        <Table<Grant>
          className="ag-console-table"
          rowKey={(grant) => `${grant.principal}-${grant.resourcePrefix ?? ""}`}
          size="middle"
          dataSource={grants}
          columns={grantColumns}
          pagination={false}
          scroll={{ x: 760, y: 380 }}
        />
      )}
      <Modal
        open={editor.open}
        title={text("编辑访问授权", "Edit access grants")}
        onClose={editor.hide}
        wide
        footer={
          <Space>
            <Button onClick={editor.hide}>{text("取消", "Cancel")}</Button>
            <Button type="primary" onClick={save} loading={saving}>
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          <Alert
            type="info"
            showIcon
            title={text(
              "仓库规则只会追加权限，不能撤销用户或 API Key 已有的全局角色。",
              "Repository rules add permissions; they cannot revoke an existing global user or API key role.",
            )}
          />
          {principalChoicesError !== null && (
            <Alert
              type="warning"
              showIcon
              title={text(
                "用户和 API Key 列表暂时不可用；仍可选择“OIDC / 自定义 actor”并填写主体标识。",
                "Users and API keys are temporarily unavailable. You can still choose OIDC/custom actor and enter its identifier.",
              )}
            />
          )}
          <div>
            <div className="grid grid-cols-[minmax(340px,1.45fr)_185px_minmax(260px,1.15fr)_190px_40px] items-center gap-3 px-2 pb-2 text-[11px] font-medium text-zinc-500">
              <span>{text("主体", "Principal")}</span>
              <span>{text("权限级别", "Permission")}</span>
              <span>{text("资源范围", "Resource scope")}</span>
              <span>{text("本规则授予", "Granted by this rule")}</span>
              <span />
            </div>
            <div className="border-b border-zinc-800/70">
              {draft.map((g, i) => {
                const kind = principalEditorKind(g.principal);
                const selectedChoice = principalChoices.find(
                  (choice) => choice.value === g.principal,
                );
                const level = grantLevel(g.scopes);
                return (
                  <div
                    key={i}
                    className="grid grid-cols-[minmax(340px,1.45fr)_185px_minmax(260px,1.15fr)_190px_40px] items-start gap-3 border-t border-zinc-800/70 px-2 py-3"
                  >
                    <div className="min-w-0">
                      <Select
                        className="w-full"
                        showSearch={{ optionFilterProp: "label" }}
                        value={
                          kind === "custom"
                            ? CUSTOM_PRINCIPAL
                            : g.principal || undefined
                        }
                        placeholder={text(
                          "选择用户、API Key 或外部身份",
                          "Select a user, API key, or external identity",
                        )}
                        options={[
                          {
                            label: text("用户", "Users"),
                            options: principalChoices
                              .filter((choice) =>
                                choice.value.startsWith("user:"),
                              )
                              .map((choice) => ({
                                value: choice.value,
                                label: `${choice.label} · ${choice.detail}`,
                                disabled: choice.disabled,
                              })),
                          },
                          {
                            label: "API Keys",
                            options: principalChoices
                              .filter((choice) =>
                                choice.value.startsWith("api-key:"),
                              )
                              .map((choice) => ({
                                value: choice.value,
                                label: `${choice.label} · ${choice.detail}`,
                                disabled: choice.disabled,
                              })),
                          },
                          {
                            label: text("外部身份", "External identities"),
                            options: [
                              {
                                value: CUSTOM_PRINCIPAL,
                                label: text(
                                  "OIDC / 自定义 actor",
                                  "OIDC / custom actor",
                                ),
                              },
                            ],
                          },
                        ]}
                        onChange={(value) =>
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i
                                ? {
                                    ...x,
                                    principal:
                                      value === CUSTOM_PRINCIPAL
                                        ? principalEditorKind(x.principal) ===
                                          "custom"
                                          ? x.principal
                                          : CUSTOM_PRINCIPAL
                                        : value,
                                  }
                                : x,
                            ),
                          )
                        }
                      />
                      {kind === "custom" && (
                        <Input
                          className="mt-2 font-mono"
                          placeholder={text(
                            "完整 actor，例如 oidc:gitlab:team/release",
                            "Complete actor, for example oidc:gitlab:team/release",
                          )}
                          value={
                            g.principal === CUSTOM_PRINCIPAL ? "" : g.principal
                          }
                          onChange={(event) =>
                            setDraft((d) =>
                              d.map((x, j) =>
                                j === i
                                  ? { ...x, principal: event.target.value }
                                  : x,
                              ),
                            )
                          }
                        />
                      )}
                      <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                        {kind === "custom"
                          ? text(
                              "必须与认证完成后产生的 actor 完全一致",
                              "Must exactly match the authenticated actor",
                            )
                          : selectedChoice?.detail}
                      </div>
                    </div>
                    <div className="min-w-0">
                      <Select
                        className="w-full"
                        value={level}
                        options={[
                          {
                            value: "read",
                            label: text(
                              "读取 · 浏览 / 拉取",
                              "Read · browse / pull",
                            ),
                          },
                          {
                            value: "write",
                            label: text(
                              "写入 · 发布 / 编辑",
                              "Write · publish / edit",
                            ),
                          },
                          {
                            value: "admin",
                            label: text(
                              "管理 · 授权 / 删除",
                              "Admin · grant / delete",
                            ),
                          },
                        ]}
                        onChange={(value: GrantLevel) =>
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i
                                ? { ...x, scopes: scopesForLevel(value) }
                                : x,
                            ),
                          )
                        }
                      />
                    </div>
                    <div className="min-w-0">
                      <Input
                        className="font-mono"
                        placeholder={text(
                          "留空表示整个仓库",
                          "Leave blank for the entire repository",
                        )}
                        value={g.resourcePrefix ?? ""}
                        onChange={(event) =>
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i
                                ? { ...x, resourcePrefix: event.target.value }
                                : x,
                            ),
                          )
                        }
                      />
                      <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                        {resourcePrefixHint(repo.format, text)}
                      </div>
                    </div>
                    <div className="flex min-h-10 items-center">
                      <Badge
                        tone={
                          level === "admin"
                            ? "red"
                            : level === "write"
                              ? "blue"
                              : "green"
                        }
                      >
                        {grantCapabilitiesLabel(level, text)}
                      </Badge>
                    </div>
                    <Tooltip title={text("移除规则", "Remove rule")}>
                      <Button
                        type="text"
                        danger
                        aria-label={text("移除规则", "Remove rule")}
                        icon={<DeleteOutlined />}
                        onClick={() =>
                          setDraft((d) => d.filter((_, j) => j !== i))
                        }
                      />
                    </Tooltip>
                  </div>
                );
              })}
              {draft.length === 0 && (
                <div className="border-t border-zinc-800/70 px-3 py-8 text-center text-xs text-zinc-600">
                  {text("尚未添加授权规则", "No access grants added")}
                </div>
              )}
            </div>
          </div>
          <Button
            block
            type="dashed"
            icon={<PlusOutlined />}
            onClick={() =>
              setDraft((d) => [
                ...d,
                { principal: "", scopes: ["repositories:read"] },
              ])
            }
          >
            {text("添加授权规则", "Add access grant")}
          </Button>
        </div>
      </Modal>
    </div>
  );
}

/* ---------------- Retention ---------------- */

const RETENTION_DRY_RUN_PAGE_SIZE = 100;

function retentionFormatCopy(format: Repository["format"], text: Localize) {
  switch (format) {
    case "oci":
      return {
        ageLabel: text("镜像版本保留天数", "Image version retention days"),
        ageHint: text(
          "Manifest 创建超过此天数后，才会进入清理候选。",
          "A manifest becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个镜像最少保留版本",
          "Minimum versions per image",
        ),
        minimumHint: text(
          "按镜像名称分组，始终保护最新的这些 manifest。",
          "Group by image name and always protect these newest manifests.",
        ),
        maximumLabel: text(
          "每个镜像最多保留版本",
          "Maximum versions per image",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧 manifest 会进入候选。",
          "Use 0 for no limit. Older manifests beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配镜像", "Only clean matching images"),
        matchHint: text(
          "可匹配镜像名、name@digest 或 name:tag；留空表示全部。",
          "Matches image name, name@digest, or name:tag. Leave empty for all images.",
        ),
        protectLabel: text("保护镜像版本", "Protect image versions"),
        protectHint: text(
          "可用镜像名保护全部版本，或用 digest、tag 精确保护。",
          "Use an image name to protect all versions, or a digest/tag for an exact version.",
        ),
        matchPlaceholder: text(
          "如 ^team/backend(@|:)",
          "e.g. ^team/backend(@|:)",
        ),
        protectPlaceholder: text(
          "如 ^team/backend:stable$",
          "e.g. ^team/backend:stable$",
        ),
        candidateName: text("镜像版本", "image versions"),
      };
    case "conan":
      return {
        ageLabel: text(
          "Recipe revision 保留天数",
          "Recipe revision retention days",
        ),
        ageHint: text(
          "Recipe revision 创建超过此天数后，才会进入清理候选。",
          "A recipe revision becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 reference 最少保留版本",
          "Minimum versions per reference",
        ),
        minimumHint: text(
          "按完整 Conan reference 分组，保护最新的 recipe revisions。",
          "Group by full Conan reference and protect the newest recipe revisions.",
        ),
        maximumLabel: text(
          "每个 reference 最多保留版本",
          "Maximum versions per reference",
        ),
        maximumHint: text(
          "0 表示不限制；清理 recipe revision 时会同时隐藏其二进制包。",
          "Use 0 for no limit. Cleaning a recipe revision also hides its binary packages.",
        ),
        matchLabel: text(
          "只清理匹配 reference",
          "Only clean matching references",
        ),
        matchHint: text(
          "可匹配完整 reference 或 reference#recipe-revision。",
          "Matches a full reference or reference#recipe-revision.",
        ),
        protectLabel: text("保护 Conan 版本", "Protect Conan versions"),
        protectHint: text(
          "匹配 reference 可保护全部 revisions，精确坐标只保护一个版本。",
          "A matching reference protects all revisions; an exact coordinate protects one version.",
        ),
        matchPlaceholder: text("如 ^openssl/3\\.", "e.g. ^openssl/3\\."),
        protectPlaceholder: text(
          "如 @release/stable(#|$)",
          "e.g. @release/stable(#|$)",
        ),
        candidateName: "Recipe revision",
      };
    case "raw":
      return {
        ageLabel: text("资产未更新保留天数", "Asset inactivity retention days"),
        ageHint: text(
          "路径资产超过此天数未更新后，才会进入清理候选。",
          "A path asset becomes eligible after it has not been updated for this many days.",
        ),
        minimumLabel: "",
        minimumHint: "",
        maximumLabel: "",
        maximumHint: "",
        matchLabel: text("只清理匹配路径", "Only clean matching paths"),
        matchHint: text(
          "可选 RE2 路径正则；留空表示匹配仓库内全部资产。",
          "Optional RE2 path regex. Leave empty to match every repository asset.",
        ),
        protectLabel: text("保护路径", "Protect paths"),
        protectHint: text(
          "匹配任一正则的路径永不进入清理候选。",
          "Paths matching any regex never become eligible.",
        ),
        matchPlaceholder: text(
          "如 ^releases/nightly/",
          "e.g. ^releases/nightly/",
        ),
        protectPlaceholder: text(
          "如 ^releases/stable/",
          "e.g. ^releases/stable/",
        ),
        candidateName: text("路径资产", "path assets"),
      };
    default:
      return {
        ageLabel: text("发布版本保留天数", "Release version retention days"),
        ageHint: text(
          "发布版本创建超过此天数后，才会进入清理候选。",
          "A release version becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个模块最少保留版本",
          "Minimum versions per module",
        ),
        minimumHint: text(
          "按 groupId:artifactId 分组，始终保护最新的这些版本。",
          "Group by groupId:artifactId and always protect the newest versions.",
        ),
        maximumLabel: text(
          "每个模块最多保留版本",
          "Maximum versions per module",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧版本会进入候选。",
          "Use 0 for no limit. Older versions beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配坐标", "Only clean matching coordinates"),
        matchHint: text(
          "可选 RE2 正则；留空表示匹配全部 Maven 坐标。",
          "Optional RE2 regex. Leave empty to match all Maven coordinates.",
        ),
        protectLabel: text("保护 Maven 坐标", "Protect Maven coordinates"),
        protectHint: text(
          "匹配任一正则的坐标永不进入清理候选。",
          "Coordinates matching any regex never become eligible.",
        ),
        matchPlaceholder: text("如 ^com\\.example:", "e.g. ^com\\.example:"),
        protectPlaceholder: text(
          "如 ^com\\.example:platform:",
          "e.g. ^com\\.example:platform:",
        ),
        candidateName: text("制品版本", "artifact versions"),
      };
  }
}

function retentionCandidateTypeLabel(
  versionType: RetentionDryRun["candidates"][number]["versionType"],
  format: Repository["format"],
  text: Localize,
) {
  if (versionType === "snapshot") return text("快照构建", "Snapshot build");
  if (versionType === "release") return text("发布版本", "Release version");
  if (versionType === "asset") return text("路径资产", "Path asset");
  return format === "oci"
    ? text("Manifest 版本", "Manifest version")
    : "Recipe revision";
}

function RetentionTab({ repo }: { repo: Repository }) {
  const { token } = useAuth();
  const { text } = usePreferences();
  const [policy, setPolicy] = useState<RetentionPolicy | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [enabled, setEnabled] = useState(false);
  const [keepDays, setKeepDays] = useState(0);
  const [snapshotKeepDays, setSnapshotKeepDays] = useState(0);
  const [minimumVersions, setMinimumVersions] = useState(0);
  const [maximumVersions, setMaximumVersions] = useState(0);
  const [coordinatePatterns, setCoordinatePatterns] = useState<string[]>([]);
  const [protectedPatterns, setProtectedPatterns] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [dryRun, setDryRun] = useState<RetentionDryRun | null>(null);
  const [dryRunning, setDryRunning] = useState(false);
  const [dryRunLoadingMore, setDryRunLoadingMore] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [notice, setNotice] = useState("");
  const isMaven = repo.format === "maven";
  const isRaw = repo.format === "raw";
  const copy = retentionFormatCopy(repo.format, text);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRetentionPolicy({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setPolicy(data);
      setEnabled(data.enabled ?? false);
      setKeepDays(data.keepDays);
      setSnapshotKeepDays(data.snapshotKeepDays ?? data.keepDays);
      setMinimumVersions(data.minimumVersions);
      setMaximumVersions(data.maximumVersions ?? 0);
      setCoordinatePatterns(data.coordinatePatterns ?? []);
      setProtectedPatterns(data.protectedPatterns ?? []);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    if (!policy) return;
    if (!isRaw && maximumVersions > 0 && maximumVersions < minimumVersions) {
      setSaveError(
        new Error(
          text(
            "最多保留版本数必须为 0，或不小于最少保留版本数",
            "Maximum versions must be 0 or greater than or equal to minimum versions.",
          ),
        ),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    setNotice("");
    const { error: err } = await replaceRetentionPolicy({
      path: { repositoryId: repo.id },
      body: {
        ...policy,
        enabled,
        keepDays,
        snapshotKeepDays: isMaven
          ? snapshotKeepDays
          : (policy.snapshotKeepDays ?? policy.keepDays),
        minimumVersions: isRaw ? policy.minimumVersions : minimumVersions,
        maximumVersions: isRaw
          ? (policy.maximumVersions ?? 0)
          : maximumVersions,
        coordinatePatterns,
        protectedPatterns,
      },
      headers: { "If-Match": policy.version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice(text("策略已保存", "Policy saved"));
    setDryRun(null);
    void load();
  };

  const runDryRun = async () => {
    setDryRunning(true);
    setDryRun(null);
    setSaveError(null);
    const { data, error: err } = await dryRunRepositoryRetention({
      path: { repositoryId: repo.id },
      query: { pageSize: RETENTION_DRY_RUN_PAGE_SIZE },
    });
    setDryRunning(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setDryRun(data ?? null);
  };

  const loadMoreDryRun = async () => {
    if (!dryRun?.nextPageToken || dryRunLoadingMore) return;
    setDryRunLoadingMore(true);
    const { data, error: err } = await dryRunRepositoryRetention({
      path: { repositoryId: repo.id },
      query: {
        pageSize: RETENTION_DRY_RUN_PAGE_SIZE,
        pageToken: dryRun.nextPageToken,
      },
    });
    setDryRunLoadingMore(false);
    if (err) {
      const code = (err as { code?: string } | undefined)?.code;
      if (code === "invalid_page_token") {
        setDryRun(null);
        setSaveError(
          new Error(
            text(
              "试运行结果已过期或策略已变化，请重新试运行",
              "The dry-run result expired or the policy changed. Run it again.",
            ),
          ),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    if (!data) return;
    setDryRun((current) =>
      current
        ? {
            ...data,
            candidates: [...current.candidates, ...data.candidates],
          }
        : data,
    );
  };

  const execute = async () => {
    if (!dryRun) return;
    setExecuting(true);
    const { error: err } = await executeRepositoryRetention({
      path: { repositoryId: repo.id },
      headers: {
        "Idempotency-Key": crypto.randomUUID(),
        "If-Match": dryRun.policyVersion,
      },
    });
    setExecuting(false);
    if (err) {
      const code = (err as { code?: string } | undefined)?.code;
      if (code === "version_conflict") {
        setDryRun(null);
        setSaveError(
          new Error(
            text(
              "保留策略已变化，当前预览不再有效，请重新试运行",
              "The retention policy changed, so this preview is no longer valid. Run it again.",
            ),
          ),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    setNotice(
      text(
        "保留执行任务已提交，请在「生命周期任务」标签页查看进度",
        "The retention task was submitted. Track it on the Lifecycle jobs tab.",
      ),
    );
    setDryRun(null);
  };

  const exportDryRun = async () => {
    setExporting(true);
    setSaveError(null);
    try {
      const response = await fetch(
        `/api/v2/repositories/${repo.id}/retention:dry-run?output=csv`,
        {
          method: "POST",
          credentials: "include",
          headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        },
      );
      if (!response.ok) {
        const problem = (await response.json().catch(() => null)) as {
          message?: string;
        } | null;
        throw new Error(
          problem?.message ??
            text("导出试运行结果失败", "Failed to export dry-run results"),
        );
      }
      downloadCsv(`${repo.name}-retention.csv`, await response.text());
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setExporting(false);
    }
  };

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature={text("保留策略", "Retention policy")} />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!policy) return <Loading />;

  if (
    repo.type !== "hosted" ||
    !["maven", "oci", "conan", "raw"].includes(repo.format)
  ) {
    return (
      <NotEnabled
        feature={text("Hosted 仓库保留策略", "Hosted repository retention")}
      />
    );
  }

  const dryRunColumns: ColumnsType<RetentionDryRun["candidates"][number]> = [
    {
      title: text("清理单位", "Cleanup unit"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-zinc-200"
          title={value}
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
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("类型", "Type"),
      key: "versionType",
      width: 140,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {retentionCandidateTypeLabel(
            candidate.versionType,
            candidate.format,
            text,
          )}
        </span>
      ),
    },
    {
      title: text("原因", "Reason"),
      key: "reasons",
      width: 280,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {candidate.reasons
            .map((reason) =>
              reason === "maximum_versions"
                ? text("超过版本上限", "Exceeded version limit")
                : candidate.versionType === "asset"
                  ? text(
                      `已 ${candidate.ageDays} 天未更新`,
                      `Not updated for ${candidate.ageDays} days`,
                    )
                  : text(
                      `已保留 ${candidate.ageDays} 天`,
                      `Retained for ${candidate.ageDays} days`,
                    ),
            )
            .join("、")}
        </span>
      ),
    },
    {
      title: isRaw
        ? text("最后更新时间", "Last updated")
        : text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      {isRaw && (
        <Alert
          type="info"
          showIcon
          title={text("Raw 按路径资产清理", "Raw cleanup by path asset")}
          description={text(
            "Raw 没有版本分组，因此不应用最少或最多版本数；期限按资产最后更新时间计算。",
            "Raw assets are not version-grouped, so minimum and maximum versions do not apply. Age is calculated from the last update.",
          )}
        />
      )}
      <div className="flex items-center justify-between border-b border-zinc-800 pb-4">
        <div>
          <div className="text-sm font-medium text-zinc-200">
            {text("自动清理", "Automatic cleanup")}
          </div>
          <div className="mt-1 text-xs text-zinc-500">
            {text(
              "关闭时不会创建定时或手动清理任务，已有墓碑不受影响。",
              "When disabled, no scheduled or manual cleanup task is created. Existing tombstones are unaffected.",
            )}
          </div>
        </div>
        <Switch
          checked={enabled}
          checkedChildren={text("已启用", "Enabled")}
          unCheckedChildren={text("已停用", "Disabled")}
          onChange={setEnabled}
        />
      </div>
      <div
        className={`grid max-w-5xl gap-4 ${!isMaven && !isRaw ? "grid-cols-3" : "grid-cols-2"}`}
      >
        <Field label={copy.ageLabel} hint={copy.ageHint}>
          <Space.Compact block>
            <InputNumber
              min={1}
              max={36500}
              precision={0}
              className="w-full"
              value={keepDays}
              onChange={(value) => setKeepDays(value ?? 0)}
            />
            <Space.Addon>{text("天", "days")}</Space.Addon>
          </Space.Compact>
        </Field>
        {isMaven && (
          <Field
            label={text("快照版本保留天数", "Snapshot retention days")}
            hint={text(
              "Maven SNAPSHOT 可使用独立于发布版本的保留期限。",
              "Maven SNAPSHOT versions can use a retention period separate from releases.",
            )}
          >
            <Space.Compact block>
              <InputNumber
                min={1}
                max={36500}
                precision={0}
                className="w-full"
                value={snapshotKeepDays}
                onChange={(value) => setSnapshotKeepDays(value ?? 0)}
              />
              <Space.Addon>{text("天", "days")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        {!isRaw && (
          <Field label={copy.minimumLabel} hint={copy.minimumHint}>
            <Space.Compact block>
              <InputNumber
                min={1}
                max={100000}
                precision={0}
                className="w-full"
                value={minimumVersions}
                onChange={(value) => setMinimumVersions(value ?? 0)}
              />
              <Space.Addon>{text("个", "items")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        {!isRaw && (
          <Field label={copy.maximumLabel} hint={copy.maximumHint}>
            <Space.Compact block>
              <InputNumber
                min={0}
                max={100000}
                precision={0}
                className="w-full"
                value={maximumVersions}
                onChange={(value) => setMaximumVersions(value ?? 0)}
              />
              <Space.Addon>{text("个", "items")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
      </div>
      <div className="grid max-w-5xl grid-cols-2 gap-4 border-t border-zinc-800 pt-4">
        <Field label={copy.matchLabel} hint={copy.matchHint}>
          <Select
            mode="tags"
            className="w-full font-mono text-xs"
            value={coordinatePatterns}
            onChange={setCoordinatePatterns}
            tokenSeparators={[",", " "]}
            maxTagCount="responsive"
            placeholder={copy.matchPlaceholder}
          />
        </Field>
        <Field label={copy.protectLabel} hint={copy.protectHint}>
          <Select
            mode="tags"
            className="w-full font-mono text-xs"
            value={protectedPatterns}
            onChange={setProtectedPatterns}
            tokenSeparators={[",", " "]}
            maxTagCount="responsive"
            placeholder={copy.protectPlaceholder}
          />
        </Field>
      </div>
      <Space>
        <Button type="primary" onClick={save} loading={saving}>
          {text("保存策略", "Save policy")}
        </Button>
        <Button onClick={runDryRun} loading={dryRunning} disabled={!enabled}>
          {text("试运行", "Dry run")}
        </Button>
        {dryRun && dryRun.candidates.length > 0 && (
          <Popconfirm
            title={text("确认执行保留清理？", "Run retention cleanup?")}
            description={text(
              `将清理全部 ${dryRun.totalCandidates} 个候选${copy.candidateName}；执行前会再次校验策略版本。`,
              `This will clean all ${dryRun.totalCandidates} candidate ${copy.candidateName}. The policy version is checked again before execution.`,
            )}
            okText={text("执行清理", "Run cleanup")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true, loading: executing }}
            onConfirm={execute}
          >
            <Button danger loading={executing} disabled={!enabled}>
              {text(
                `执行清理（${dryRun.totalCandidates} 个）`,
                `Run cleanup (${dryRun.totalCandidates})`,
              )}
            </Button>
          </Popconfirm>
        )}
      </Space>
      {dryRun && (
        <Card>
          <CardHeader
            title={text(
              `试运行结果：已加载 ${dryRun.candidates.length} / 共 ${dryRun.totalCandidates} 个候选${copy.candidateName}（策略版本 ${dryRun.policyVersion}）`,
              `Dry-run results: loaded ${dryRun.candidates.length} of ${dryRun.totalCandidates} candidate ${copy.candidateName} (policy version ${dryRun.policyVersion})`,
            )}
            extra={
              dryRun.totalCandidates > 0 ? (
                <Tooltip
                  title={text(
                    "导出完整候选集，不受当前分页影响",
                    "Export the complete candidate set, independent of the current page",
                  )}
                >
                  <Button
                    size="small"
                    icon={<DownloadOutlined />}
                    loading={exporting}
                    onClick={() => void exportDryRun()}
                  >
                    {text("导出 CSV", "Export CSV")}
                  </Button>
                </Tooltip>
              ) : undefined
            }
          />
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2 border-b border-zinc-800/80 px-4 py-3 text-xs text-zinc-400">
            <span>
              {text("按期限", "By age")}{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.age}
              </strong>
            </span>
            <span>
              {text("超过版本上限", "Exceeded version limit")}{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.maximumVersions}
              </strong>
            </span>
            <span>
              {text("类型：", "Types: ")}
              {[
                [
                  text("发布", "Release"),
                  dryRun.summary.versionTypeCounts.release,
                ],
                [
                  text("快照", "Snapshot"),
                  dryRun.summary.versionTypeCounts.snapshot,
                ],
                [
                  text("版本", "Version"),
                  dryRun.summary.versionTypeCounts.version,
                ],
                [text("资产", "Asset"), dryRun.summary.versionTypeCounts.asset],
              ]
                .filter(([, count]) => Number(count) > 0)
                .map(([label, count]) => `${label} ${count}`)
                .join(", ") || text("无", "None")}
            </span>
            <span>
              {text("最早候选", "Oldest candidate")}{" "}
              {formatDate(dryRun.summary.oldestCandidateAt)}
            </span>
          </div>
          {dryRun.candidates.length === 0 ? (
            <EmptyState
              title={text(
                `没有需要清理的${copy.candidateName}`,
                `No ${copy.candidateName} to clean`,
              )}
            />
          ) : (
            <Table<RetentionDryRun["candidates"][number]>
              className="ag-console-table"
              rowKey={(candidate) =>
                `${candidate.format}:${candidate.coordinate}:${candidate.digest}`
              }
              size="middle"
              dataSource={dryRun.candidates}
              columns={dryRunColumns}
              pagination={false}
              scroll={{ x: 1080 }}
            />
          )}
          {dryRun.nextPageToken && (
            <div className="flex justify-center border-t border-zinc-800 px-4 py-3">
              <Button onClick={loadMoreDryRun} loading={dryRunLoadingMore}>
                {text("加载更多候选", "Load more candidates")}
              </Button>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}

/* ---------------- Capacity ---------------- */

function CapacityTab({ repo }: { repo: Repository }) {
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
      <NotEnabled feature={text("容量管理", "Capacity management")} />
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

/* ---------------- Distribute (Promotion / Replication) ---------------- */

function DistributeTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [repos, setRepos] = useState<Repository[]>([]);
  const [targetId, setTargetId] = useState("");
  const [coordinate, setCoordinate] = useState("");
  const [digest, setDigest] = useState("");
  const [plans, setPlans] = useState<ReplicationPlan[] | null>(null);
  const [detail, setDetail] = useState<ReplicationPlanDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<"promote" | "replicate" | null>(null);

  const targets = repos.filter(
    (r) => r.id !== repo.id && r.format === repo.format && r.state === "active",
  );

  const load = useCallback(async () => {
    setError(null);
    const [allRepos, p] = await Promise.all([
      listRepositories({ query: { pageSize: 200 } }),
      listRepositoryReplications({ path: { repositoryId: repo.id } }),
    ]);
    setRepos(allRepos.data?.items ?? []);
    if (p.error) {
      if (!isNotFound(p.error)) setError(p.error);
      setPlans([]);
      return;
    }
    setPlans(p.data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const cancelPlan = async (planId: string) => {
    setActionError(null);
    const { error: err } = await deleteRepositoryReplication({
      path: { repositoryId: repo.id, replicationPlanId: planId },
    });
    if (err) {
      setActionError(err);
      return;
    }
    setNotice(
      text(
        "已取消复制计划，工作进程不再重试。",
        "Replication plan canceled. Workers will not retry it.",
      ),
    );
    void load();
  };

  const submit = async (kind: "promote" | "replicate") => {
    setBusy(kind);
    setActionError(null);
    setNotice("");
    const body = {
      targetRepositoryId: targetId,
      coordinate: coordinate.trim(),
      digest: digest.trim(),
    };
    const headers = { "Idempotency-Key": crypto.randomUUID() };
    const { error: err } =
      kind === "promote"
        ? await createRepositoryPromotion({
            path: { repositoryId: repo.id },
            body,
            headers,
          })
        : await createRepositoryReplication({
            path: { repositoryId: repo.id },
            body,
            headers,
          });
    setBusy(null);
    if (err) {
      setActionError(err);
      return;
    }
    setNotice(
      kind === "promote"
        ? text(
            "晋升任务已提交，请在「生命周期任务」查看进度",
            "Promotion task submitted. Track it on the Lifecycle jobs tab.",
          )
        : text(
            "复制计划已创建，下方查看进度",
            "Replication plan created. Track its progress below.",
          ),
    );
    setCoordinate("");
    setDigest("");
    void load();
  };

  const showDetail = async (planId: string) => {
    const { data, error: err } = await getRepositoryReplication({
      path: { repositoryId: repo.id, replicationPlanId: planId },
    });
    if (err) {
      setActionError(err);
      return;
    }
    setDetail(data ?? null);
  };

  const repoName = (id: string) =>
    repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + "…";

  if (error !== null) return <ErrorBanner error={error} onRetry={load} />;

  const planColumns: ColumnsType<ReplicationPlan> = [
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: text("目标仓库", "Target repository"),
      dataIndex: "targetRepositoryId",
      key: "targetRepositoryId",
      width: 220,
      render: (value: string) => (
        <span className="text-xs text-zinc-300">{repoName(value)}</span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("完成时间", "Completed"),
      dataIndex: "completedAt",
      key: "completedAt",
      width: 180,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 180,
      render: (_, plan) => (
        <Space size="small">
          <Button size="small" onClick={() => showDetail(plan.id)}>
            {text("进度", "Progress")}
          </Button>
          {(plan.state === "pending" || plan.state === "failed") && (
            <Popconfirm
              title={text("确认取消复制计划？", "Cancel replication plan?")}
              description={text(
                "取消后工作进程将不再重试，已复制的字节不会自动删除。",
                "Workers will not retry after cancellation. Bytes already copied are not deleted automatically.",
              )}
              okText={text("确认取消", "Cancel plan")}
              cancelText={text("返回", "Back")}
              okButtonProps={{ danger: true }}
              onConfirm={() => cancelPlan(plan.id)}
            >
              <Button size="small" danger>
                {text("取消", "Cancel")}
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];
  const checkpointColumns: ColumnsType<
    ReplicationPlanDetail["checkpoints"][number]
  > = [
    {
      title: text("对象", "Object"),
      dataIndex: "objectKey",
      key: "objectKey",
      width: 280,
      render: (value: string) => (
        <span
          className="block max-w-64 truncate font-mono text-xs text-zinc-300"
          title={value}
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
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("大小", "Size"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-zinc-400">{formatBytes(value)}</span>
      ),
    },
    {
      title: text("进度", "Progress"),
      key: "progress",
      width: 110,
      render: (_, checkpoint) => (
        <span className="text-xs text-zinc-400">
          {checkpoint.size > 0
            ? `${Math.round((checkpoint.byteOffset / checkpoint.size) * 100)}%`
            : "—"}
        </span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("重试", "Attempts"),
      dataIndex: "attempts",
      key: "attempts",
      width: 90,
      render: (value: number) => (
        <span className="text-xs text-zinc-500">{value}</span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {actionError !== null && <ErrorBanner error={actionError} />}
      {notice && <Alert type="success" showIcon title={notice} />}

      {/* 发起表单 */}
      <div className="rounded-lg border border-zinc-800 p-4">
        <div className="mb-3 text-sm font-medium text-zinc-200">
          {text("发起晋升 / 复制", "Start promotion / replication")}
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
          <Field label={text("目标仓库", "Target repository")}>
            <Select
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder={text(
                "选择同格式仓库…",
                "Select a repository with the same format…",
              )}
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              onChange={setTargetId}
            />
          </Field>
          <Field label={text("坐标 coordinate", "Coordinate")}>
            <Input
              className="font-mono"
              placeholder={text(
                "如 nginx:alpine 或 GAV",
                "For example: nginx:alpine or GAV",
              )}
              value={coordinate}
              onChange={(e) => setCoordinate(e.target.value)}
            />
          </Field>
          <Field label={text("摘要 digest", "Digest")}>
            <Input
              className="font-mono"
              placeholder="sha256:…"
              value={digest}
              onChange={(e) => setDigest(e.target.value)}
            />
          </Field>
          <div className="flex items-end gap-2">
            <Button
              type="primary"
              loading={busy === "promote"}
              onClick={() => submit("promote")}
              disabled={
                busy !== null ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("晋升", "Promote")}
            </Button>
            <Button
              loading={busy === "replicate"}
              onClick={() => submit("replicate")}
              disabled={
                busy !== null ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {text("复制", "Replicate")}
            </Button>
          </div>
        </div>
        <p className="mt-2 text-xs text-zinc-600">
          {text(
            "晋升：在目标仓库创建同一制品的可见副本（审计追踪）；复制：异步、带断点地拷贝制品字节到目标仓库。",
            "Promotion creates a visible copy of the artifact in the target repository with an audit trail. Replication copies artifact bytes asynchronously with checkpoints.",
          )}
        </p>
      </div>

      {/* 复制计划列表 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">
          {text(
            `复制计划（${plans?.length ?? 0}）`,
            `Replication plans (${plans?.length ?? 0})`,
          )}
        </div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title={text("暂无复制计划", "No replication plans")} />
        ) : (
          <Table<ReplicationPlan>
            className="ag-console-table"
            rowKey="id"
            size="middle"
            dataSource={plans}
            columns={planColumns}
            pagination={false}
            scroll={{ x: 1040 }}
          />
        )}
      </div>

      {/* 复制进度详情 */}
      <Modal
        open={!!detail}
        title={text("复制进度详情", "Replication progress")}
        onClose={() => setDetail(null)}
        wide
      >
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-zinc-400">
              <span>
                {text("状态：", "Status: ")}
                <StateBadge state={detail.state} />
              </span>
              <span>
                {text("目标：", "Target: ")}
                {repoName(detail.targetRepositoryId)}
              </span>
              <span>
                {text("创建：", "Created: ")}
                {formatDate(detail.createdAt)}
              </span>
              {detail.lastError && (
                <span className="text-rose-400">{detail.lastError}</span>
              )}
            </div>
            {detail.checkpoints.length === 0 ? (
              <p className="py-4 text-center text-sm text-zinc-500">
                {text("暂无检查点", "No checkpoints")}
              </p>
            ) : (
              <Table<ReplicationPlanDetail["checkpoints"][number]>
                className="ag-console-table"
                rowKey={(checkpoint, index) =>
                  `${checkpoint.objectKey}-${index}`
                }
                size="small"
                dataSource={detail.checkpoints}
                columns={checkpointColumns}
                pagination={false}
                scroll={{ x: 900 }}
              />
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}

/* ---------------- Jobs ---------------- */

function JobsTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [jobs, setJobs] = useState<LifecycleJob[] | null>(null);
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listRepositoryLifecycleJobs({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    setJobs(data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 10000);
    return () => clearInterval(timer);
  }, [load]);

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature={text("生命周期任务", "Lifecycle jobs")} />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );

  if (!jobs) return <Loading />;
  if (jobs.length === 0)
    return (
      <EmptyState
        title={text("暂无生命周期任务", "No lifecycle jobs")}
        hint={text(
          "保留清理、晋升、复制任务会显示在这里",
          "Retention cleanup, promotion, and replication tasks appear here.",
        )}
      />
    );

  const jobColumns: ColumnsType<LifecycleJob> = [
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: text("类型", "Kind"),
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (value: string) => <Badge tone="blue">{value}</Badge>,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("完成时间", "Completed"),
      dataIndex: "completedAt",
      key: "completedAt",
      width: 180,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("错误", "Error"),
      dataIndex: "lastError",
      key: "lastError",
      width: 300,
      render: (value?: string) => (
        <span
          className="block max-w-72 truncate text-xs text-rose-400"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
    },
  ];

  return (
    <Table<LifecycleJob>
      className="ag-console-table"
      rowKey="id"
      size="middle"
      dataSource={jobs}
      columns={jobColumns}
      pagination={false}
      scroll={{ x: 1100 }}
    />
  );
}

/* ---------------- Tombstones ---------------- */

function TombstonesTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [items, setItems] = useState<ArtifactTombstone[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [restoreError, setRestoreError] = useState<unknown>(null);
  const [restoreNotice, setRestoreNotice] = useState("");
  const [restoring, setRestoring] = useState<string | null>(null);

  const load = useCallback(
    async (pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const { data, error: err } = await listRepositoryTombstones({
        path: { repositoryId: repo.id },
        query: { pageSize: 50, pageToken },
      });
      setLoading(false);
      if (err) {
        setError(err);
        return;
      }
      setItems((prev) =>
        pageToken ? [...prev, ...(data?.items ?? [])] : (data?.items ?? []),
      );
      setNextToken(data?.nextPageToken);
    },
    [repo.id],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const restore = async (coordinate: string) => {
    setRestoring(coordinate);
    setRestoreError(null);
    setRestoreNotice("");
    const { error: err } = await restoreRepositoryArtifact({
      path: { repositoryId: repo.id },
      body: { coordinate },
    });
    setRestoring(null);
    if (err) {
      setRestoreError(err);
      return;
    }
    setRestoreNotice(text(`已恢复 ${coordinate}`, `Restored ${coordinate}`));
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature={text("墓碑管理", "Tombstone management")} />
    ) : (
      <ErrorBanner error={error} onRetry={() => load()} />
    );
  if (loading) return <Loading />;

  const tombstoneColumns: ColumnsType<ArtifactTombstone> = [
    {
      title: text("坐标", "Coordinate"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-zinc-200"
          title={value}
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
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("删除时间", "Deleted"),
      dataIndex: "tombstonedAt",
      key: "tombstonedAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, item) => (
        <Popconfirm
          title={text("确认恢复此制品？", "Restore this artifact?")}
          description={text(
            "恢复后制品会重新出现在仓库浏览与协议读取中。",
            "After restoration, the artifact is available again in repository browsing and protocol reads.",
          )}
          okText={text("恢复", "Restore")}
          cancelText={text("取消", "Cancel")}
          onConfirm={() => restore(item.coordinate)}
        >
          <Button size="small" loading={restoring === item.coordinate}>
            {text("恢复", "Restore")}
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      {restoreError !== null && (
        <div className="mb-3">
          <ErrorBanner error={restoreError} />
        </div>
      )}
      {restoreNotice && <Alert type="success" showIcon title={restoreNotice} />}
      {items.length === 0 ? (
        <EmptyState
          title={text("暂无墓碑", "No tombstones")}
          hint={text(
            "被删除的制品会保留墓碑记录，可在此恢复",
            "Deleted artifacts retain tombstone records and can be restored here.",
          )}
        />
      ) : (
        <>
          <Table<ArtifactTombstone>
            className="ag-console-table"
            rowKey={(item) =>
              `${item.coordinate}:${item.digest}:${item.tombstonedAt}`
            }
            size="middle"
            dataSource={items}
            columns={tombstoneColumns}
            pagination={false}
            scroll={{ x: 830 }}
          />
          <Pagination hasMore={!!nextToken} onMore={() => load(nextToken)} />
        </>
      )}
    </div>
  );
}

/* ---------------- Detail page ---------------- */

function NotEnabled({ feature }: { feature: string }) {
  const { text } = usePreferences();
  return (
    <EmptyState
      title={text(`${feature}功能未启用`, `${feature} is unavailable`)}
      hint={text(
        "当前后端构建尚未挂载此管理端点（返回 404）",
        "The current backend does not expose this endpoint (404)",
      )}
    />
  );
}

function RepositoryConceptHelp({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const typeLabel =
    repo.type === "proxy" ? "Proxy Repository" : "Hosted Repository";
  const concepts = [
    [
      "Repository",
      text(
        "一个格式命名空间，承载访问策略、制品或上游配置。",
        "A format namespace containing access policy, artifacts, and upstream configuration.",
      ),
    ],
    [
      typeLabel,
      repo.type === "proxy"
        ? text(
            "按需从上游拉取并缓存响应，不提供发布入口。",
            "Fetches and caches upstream responses on demand; publishing is disabled.",
          )
        : text(
            "保存已校验并发布的制品，可执行删除、恢复和保留。",
            "Stores verified published artifacts and supports deletion, restore, and retention.",
          ),
    ],
    [
      "Artifact",
      text(
        "用户可见的逻辑制品身份，例如 Maven 坐标或 OCI 镜像。",
        "A user-visible logical identity such as a Maven coordinate or OCI image.",
      ),
    ],
    [
      "Asset",
      text(
        "制品下的不可变文件或 Blob，例如 JAR、POM 或镜像层。",
        "An immutable file or blob under an artifact, such as a JAR, POM, or image layer.",
      ),
    ],
    ...(repo.type === "proxy"
      ? [
          [
            "Cache Entry",
            text(
              "上游响应的缓存索引与字节，不等同于 Hosted 制品。",
              "An index and bytes for an upstream response; it is not a hosted artifact.",
            ),
          ],
        ]
      : []),
    ...(repo.type === "hosted"
      ? [
          [
            "Publication",
            text(
              "将完整且通过校验的 staged 内容转为可见制品。",
              "Turns complete, validated staged content into a visible artifact.",
            ),
          ],
          [
            "Tombstone",
            text(
              "删除后的可恢复记录；字节会在确认无引用后回收。",
              "A restorable deletion record; bytes are reclaimed after references are gone.",
            ),
          ],
          [
            "Retention Policy",
            text(
              "按格式规则选择过期版本或路径，生成可审阅的回收任务。",
              "Selects expired versions or paths by format rules and creates a reviewable reclamation job.",
            ),
          ],
        ]
      : []),
  ];

  return (
    <Popover
      placement="bottomRight"
      title={text("概念说明", "Concepts")}
      content={
        <div className="grid max-w-[34rem] grid-cols-2 gap-x-5 gap-y-3 text-xs">
          {concepts.map(([term, description]) => (
            <div key={term}>
              <div className="font-medium text-zinc-200">{term}</div>
              <div className="mt-0.5 leading-5 text-zinc-500">
                {description}
              </div>
            </div>
          ))}
        </div>
      }
    >
      <Tooltip title={text("查看概念说明", "View concepts")}>
        <Button
          type="text"
          size="small"
          icon={<InfoCircleOutlined />}
          aria-label={text("查看概念说明", "View concepts")}
        />
      </Tooltip>
    </Popover>
  );
}

function RepositorySummary({
  repo,
  capacity,
  onOpenCapacity,
}: {
  repo: Repository;
  capacity: RepositoryCapacity | null;
  onOpenCapacity: () => void;
}) {
  const { text } = usePreferences();
  const protocolPath = `${window.location.origin}/${repo.format}/${repo.name}`;

  return (
    <div
      className="mb-3 border-b border-zinc-800/70 pb-3"
      role="group"
      aria-label={text("仓库摘要", "Repository summary")}
    >
      <div className="flex min-w-0 items-start justify-between gap-6">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-xl font-semibold text-zinc-50">
              {repo.name}
            </h1>
            <FormatBadge format={repo.format} />
            <StateBadge state={repo.state} />
          </div>
          <div className="mt-1 flex items-center gap-2 text-xs text-zinc-500">
            <span>{repo.type ?? "hosted"}</span>
            <span aria-hidden="true">·</span>
            <span>
              {repo.anonymousRead
                ? text("允许匿名读取", "Anonymous reads")
                : text("私有读取", "Private reads")}
            </span>
            <span aria-hidden="true">·</span>
            <span className="font-mono">ID {repo.id}</span>
            <span aria-hidden="true">·</span>
            <span>v{repo.version}</span>
          </div>
        </div>
        <div className="flex min-w-0 shrink-0 items-center gap-2 pt-0.5">
          <RepositoryConceptHelp repo={repo} />
          <span className="text-xs text-zinc-500">
            {text("协议入口", "Protocol endpoint")}
          </span>
          <code
            className="max-w-[32rem] truncate font-mono text-xs text-zinc-300"
            title={protocolPath}
          >
            {protocolPath}
          </code>
          <CopyButton text={protocolPath} />
        </div>
      </div>
      <div className="mt-2 flex items-center gap-4 text-xs">
        {repo.anonymousRead && (
          <Link
            to={`/browse?repository=${encodeURIComponent(repo.id)}`}
            className="font-medium text-cyan-300 hover:text-cyan-200"
          >
            {text("打开公开浏览", "Open public browser")}
          </Link>
        )}
        <Button
          type="link"
          size="small"
          className="h-auto p-0 text-xs text-zinc-400"
          onClick={onOpenCapacity}
        >
          {capacity
            ? text(
                `${formatBytes(capacity.usedBytes)} · ${formatNumber(capacity.objectCount)} 个对象`,
                `${formatBytes(capacity.usedBytes)} · ${formatNumber(capacity.objectCount)} objects`,
              )
            : text("查看容量", "View capacity")}
        </Button>
      </div>
    </div>
  );
}

function EffectiveAccessPanel({
  effectiveAccess,
}: {
  effectiveAccess: RepositoryEffectiveAccess;
}) {
  const { text } = usePreferences();
  return (
    <Collapse
      ghost
      className="mb-4 border-b border-zinc-800/60"
      items={[
        {
          key: "effective-access",
          label: (
            <span className="text-xs text-zinc-400">
              {text("当前访问判定", "Effective access")}
              <span className="ml-2 font-mono text-zinc-600">
                {effectiveAccess.actor}
              </span>
            </span>
          ),
          children: (
            <div className="border-t border-zinc-800/70 pt-3 text-xs">
              <IdentitySummary identity={effectiveAccess.identity} />
              <div className="mt-4">
                <AccessDecisionSummary access={effectiveAccess} />
              </div>
              <div className="mt-3 text-[10px] text-zinc-600">
                {text(
                  "判定顺序：管理员身份 → 全局角色 → 仓库授权 → 旧版静态策略。",
                  "Decision order: administrator identity → global role → repository grant → legacy static policy.",
                )}
              </div>
            </div>
          ),
        },
      ]}
    />
  );
}

function RepositorySettingsTab({
  repo,
  capabilities,
  onUpdated,
}: {
  repo: Repository;
  capabilities: RepositoryCapabilities | null;
  onUpdated: () => void;
}) {
  const { text } = usePreferences();
  const [endpoint, setEndpoint] = useState(repo.endpoint ?? "");
  const [hosts, setHosts] = useState((repo.allowedHosts ?? []).join(", "));
  const [anonymousRead, setAnonymousRead] = useState(repo.anonymousRead);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const egress = repo.egressProxy;
  const [egressMode, setEgressMode] = useState<
    "direct" | "environment" | "custom"
  >(egress?.mode ?? "environment");
  const [egressProtocol, setEgressProtocol] = useState<"http" | "socks5">(
    egress?.protocol ?? "http",
  );
  const [egressHost, setEgressHost] = useState(egress?.host ?? "");
  const [egressPort, setEgressPort] = useState<number | null>(
    egress?.port ?? null,
  );
  const [egressUsername, setEgressUsername] = useState(egress?.username ?? "");
  const [egressPassword, setEgressPassword] = useState("");
  const [egressClearCredentials, setEgressClearCredentials] = useState(false);
  const [egressRemoteDns, setEgressRemoteDns] = useState(
    egress?.remoteDns ?? false,
  );
  const [egressNoProxy, setEgressNoProxy] = useState(
    (egress?.noProxy ?? []).join(", "),
  );
  const [egressTesting, setEgressTesting] = useState(false);
  const [egressTestResult, setEgressTestResult] =
    useState<EgressProxyTestResult | null>(null);

  const requiresHosts = repo.format === "raw" || repo.format === "conan";

  const resetForm = () => {
    setEndpoint(repo.endpoint ?? "");
    setHosts((repo.allowedHosts ?? []).join(", "));
    setAnonymousRead(repo.anonymousRead);
    setEgressMode(repo.egressProxy?.mode ?? "environment");
    setEgressProtocol(repo.egressProxy?.protocol ?? "http");
    setEgressHost(repo.egressProxy?.host ?? "");
    setEgressPort(repo.egressProxy?.port ?? null);
    setEgressUsername(repo.egressProxy?.username ?? "");
    setEgressPassword("");
    setEgressClearCredentials(false);
    setEgressRemoteDns(repo.egressProxy?.remoteDns ?? false);
    setEgressNoProxy((repo.egressProxy?.noProxy ?? []).join(", "));
    setEgressTestResult(null);
    setError(null);
    setNotice("");
  };

  const buildEgressProxyBody = (): EgressProxyWritable => {
    if (egressMode !== "custom") {
      return { mode: egressMode };
    }
    const noProxy = egressNoProxy
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    return {
      mode: "custom",
      protocol: egressProtocol,
      host: egressHost.trim(),
      port: egressPort ?? 0,
      ...(egressUsername.trim() ? { username: egressUsername.trim() } : {}),
      ...(egressPassword ? { password: egressPassword } : {}),
      ...(egressClearCredentials ? { clearCredentials: true } : {}),
      remoteDns: egressProtocol === "socks5" ? egressRemoteDns : false,
      noProxy,
    };
  };

  const submit = async () => {
    setSaving(true);
    setError(null);
    setNotice("");
    const allowedHosts = hosts
      .split(",")
      .map((h) => h.trim())
      .filter(Boolean);
    const { error: err } = await updateRepository({
      path: { repositoryId: repo.id },
      headers: { "If-Match": repo.version },
      body: {
        anonymousRead,
        ...(repo.type === "proxy"
          ? {
              endpoint: endpoint.trim(),
              allowedHosts,
              egressProxy: buildEgressProxyBody(),
            }
          : {}),
      },
    });
    setSaving(false);
    if (err) {
      setError(err);
      return;
    }
    setNotice(text("仓库设置已保存", "Repository settings saved"));
    onUpdated();
  };

  const runEgressTest = async () => {
    setEgressTesting(true);
    setEgressTestResult(null);
    const { data, error: err } = await testEgressProxy({
      path: { repositoryId: repo.id },
    });
    setEgressTesting(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) setEgressTestResult(data);
  };

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-5 flex items-start justify-between gap-6 border-b border-zinc-800/70 pb-4">
        <div>
          <h2 className="text-sm font-semibold text-zinc-100">
            {text("仓库设置", "Repository settings")}
          </h2>
          <p className="mt-1 text-xs leading-5 text-zinc-500">
            {text(
              "管理读取方式与代理仓库的上游连接。仓库名称、格式和类型创建后不可修改。",
              "Manage read access and upstream connectivity for proxy repositories. Repository name, format, and type cannot be changed after creation.",
            )}
          </p>
        </div>
        <Space>
          <Button onClick={resetForm} disabled={saving}>
            {text("重置", "Reset")}
          </Button>
          <Button type="primary" onClick={submit} loading={saving}>
            {text("保存更改", "Save changes")}
          </Button>
        </Space>
      </div>
      {notice && (
        <Alert className="mb-4" type="success" showIcon title={notice} />
      )}
      <Space orientation="vertical" size="large" className="w-full">
        <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3">
          <div>
            <div className="text-sm font-medium text-zinc-200">
              {text("允许匿名读取", "Allow anonymous reads")}
            </div>
            <div className="mt-1 text-xs leading-5 text-zinc-500">
              {text(
                "开启后协议层 GET/HEAD 可在无需凭据时读取该仓库。",
                "When enabled, protocol GET/HEAD requests can read this repository without credentials.",
              )}
            </div>
          </div>
          <Switch checked={anonymousRead} onChange={setAnonymousRead} />
        </div>
        {repo.type === "proxy" && (
          <Space orientation="vertical" size="middle" className="w-full">
            <Field
              label={text("上游地址", "Upstream URL")}
              hint={text(
                "HTTPS 基础地址，修改后立即生效（按请求读取）。",
                "HTTPS base URL. Changes take effect immediately on the next request.",
              )}
            >
              <Input
                placeholder="https://upstream.example"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
              />
            </Field>
            <Field
              label={text("允许主机", "Allowed hosts")}
              hint={
                requiresHosts
                  ? text(
                      "逗号分隔，raw / conan 代理必填。",
                      "Comma-separated. Required for raw and Conan proxies.",
                    )
                  : text(
                      "逗号分隔；OCI / Maven 代理可留空。",
                      "Comma-separated. Optional for OCI and Maven proxies.",
                    )
              }
            >
              <Input
                placeholder="upstream.example, mirror.example"
                value={hosts}
                onChange={(e) => setHosts(e.target.value)}
              />
            </Field>
            <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3">
              <div className="text-sm font-medium text-zinc-200">
                {text("出口代理", "Egress proxy")}
              </div>
              <div className="mt-1 text-xs leading-5 text-zinc-500">
                {text(
                  "配置此代理仓库访问上游时的出口网络代理，用于企业内网或受限网络环境。",
                  "Configure the egress proxy used when this proxy repository reaches its upstream, for private or restricted networks.",
                )}
              </div>
              <Radio.Group
                className="mt-3 flex flex-col gap-2"
                value={egressMode}
                onChange={(e) => {
                  setEgressMode(e.target.value);
                  setEgressTestResult(null);
                }}
              >
                <Radio value="direct">
                  <span className="text-sm text-zinc-200">
                    {text("直连", "Direct")}
                  </span>
                  <span className="ml-2 text-xs text-zinc-500">
                    {text(
                      "不经过任何代理，保留私网地址防护",
                      "Do not use a proxy; retain private-address protection",
                    )}
                  </span>
                </Radio>
                <Radio value="environment">
                  <span className="text-sm text-zinc-200">
                    {text("跟随环境变量", "Use environment variables")}
                  </span>
                  <span className="ml-2 text-xs text-zinc-500">
                    {text(
                      "沿用进程级 HTTP(S)_PROXY 与 NO_PROXY",
                      "Use process-level HTTP(S)_PROXY and NO_PROXY",
                    )}
                  </span>
                </Radio>
                <Radio value="custom">
                  <span className="text-sm text-zinc-200">
                    {text("自定义代理", "Custom proxy")}
                  </span>
                  <span className="ml-2 text-xs text-zinc-500">
                    {text(
                      "为此仓库单独指定 HTTP 或 SOCKS5 代理",
                      "Set an HTTP or SOCKS5 proxy specifically for this repository",
                    )}
                  </span>
                </Radio>
              </Radio.Group>
              {egressMode === "custom" && (
                <Space
                  orientation="vertical"
                  size="middle"
                  className="mt-3 w-full border-t border-zinc-800/60 pt-3"
                >
                  <div className="flex flex-wrap gap-3">
                    <Field label={text("协议", "Protocol")}>
                      <Select
                        className="w-40"
                        value={egressProtocol}
                        onChange={setEgressProtocol}
                        options={[
                          { value: "http", label: "HTTP（CONNECT）" },
                          { value: "socks5", label: "SOCKS5" },
                        ]}
                      />
                    </Field>
                    <Field label={text("代理主机", "Proxy host")}>
                      <Input
                        className="w-64"
                        placeholder="proxy.corp.example"
                        value={egressHost}
                        onChange={(e) => setEgressHost(e.target.value)}
                      />
                    </Field>
                    <Field label={text("端口", "Port")}>
                      <InputNumber
                        className="w-28"
                        min={1}
                        max={65535}
                        placeholder="1080"
                        value={egressPort}
                        onChange={(value) => setEgressPort(value)}
                      />
                    </Field>
                  </div>
                  {egressProtocol === "socks5" && (
                    <div className="flex items-center justify-between gap-6">
                      <div>
                        <div className="text-xs font-medium text-zinc-400">
                          {text("远程 DNS（socks5h）", "Remote DNS (socks5h)")}
                        </div>
                        <div className="mt-1 text-xs leading-5 text-zinc-600">
                          {text(
                            "开启后由代理服务器解析上游域名，适用于本地 DNS 不可达上游的网络。",
                            "When enabled, the proxy resolves the upstream hostname. Use this when the local DNS cannot reach the upstream network.",
                          )}
                        </div>
                      </div>
                      <Switch
                        checked={egressRemoteDns}
                        onChange={setEgressRemoteDns}
                      />
                    </div>
                  )}
                  <div className="flex flex-wrap gap-3">
                    <Field
                      label={text(
                        "代理认证用户名（可选）",
                        "Proxy username (optional)",
                      )}
                    >
                      <Input
                        className="w-64"
                        placeholder="gateway"
                        value={egressUsername}
                        onChange={(e) => setEgressUsername(e.target.value)}
                      />
                    </Field>
                    <Field
                      label={text(
                        "代理认证密码（可选）",
                        "Proxy password (optional)",
                      )}
                      hint={text(
                        "AES-256-GCM 加密落库，留空则保留已存凭据。",
                        "Stored encrypted with AES-256-GCM. Leave blank to keep the current credential.",
                      )}
                    >
                      <Input.Password
                        className="w-64"
                        placeholder={
                          repo.egressProxy?.credentialsConfigured
                            ? text(
                                "已配置，输入以替换",
                                "Configured; enter a value to replace it",
                              )
                            : text("未配置", "Not configured")
                        }
                        value={egressPassword}
                        onChange={(e) => setEgressPassword(e.target.value)}
                      />
                    </Field>
                  </div>
                  {repo.egressProxy?.credentialsConfigured && (
                    <Checkbox
                      checked={egressClearCredentials}
                      onChange={(e) =>
                        setEgressClearCredentials(e.target.checked)
                      }
                    >
                      <span className="text-xs text-zinc-400">
                        {text(
                          "清除已存储的代理凭据",
                          "Clear stored proxy credentials",
                        )}
                      </span>
                    </Checkbox>
                  )}
                  <Field
                    label={text("绕过列表（noProxy）", "Bypass list (noProxy)")}
                    hint={text(
                      "逗号分隔的主机后缀或网段；命中的上游将绕过代理直连。",
                      "Comma-separated hostname suffixes or CIDRs. Matching upstreams bypass the proxy.",
                    )}
                  >
                    <Input
                      placeholder="*.internal.example, 10.0.0.0/8"
                      value={egressNoProxy}
                      onChange={(e) => setEgressNoProxy(e.target.value)}
                    />
                  </Field>
                </Space>
              )}
              <div className="mt-3 flex items-center gap-3 border-t border-zinc-800/60 pt-3">
                <Button onClick={runEgressTest} loading={egressTesting}>
                  {text("测试连接", "Test connection")}
                </Button>
                <span className="text-xs text-zinc-600">
                  {text(
                    "测试使用已保存的配置",
                    "The test uses the saved configuration",
                  )}
                </span>
                {egressTestResult &&
                  (egressTestResult.reachable ? (
                    <span className="text-xs text-emerald-400">
                      {text("代理可达", "Proxy reachable")}
                      {egressTestResult.upstreamStatus
                        ? ` · ${text(`上游返回 ${egressTestResult.upstreamStatus}`, `upstream returned ${egressTestResult.upstreamStatus}`)}`
                        : ""}
                      {egressTestResult.latencyMs !== undefined
                        ? ` · ${text(`延迟 ${egressTestResult.latencyMs} ms`, `latency ${egressTestResult.latencyMs} ms`)}`
                        : ""}
                    </span>
                  ) : (
                    <span className="text-xs text-red-400">
                      {text("连接失败：", "Connection failed: ")}
                      {egressTestResult.error ??
                        text("未知错误", "Unknown error")}
                    </span>
                  ))}
              </div>
            </div>
          </Space>
        )}
        {capabilities && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-zinc-800/70 pt-4 text-[11px] text-zinc-500">
            <span className="mr-1">
              {text("支持的操作", "Supported operations")}
            </span>
            {capabilities.operations.map((operation) => (
              <Badge key={operation} tone="zinc">
                {operation}
              </Badge>
            ))}
          </div>
        )}
        {error ? <ErrorBanner error={error} /> : null}
      </Space>
    </div>
  );
}

export function RepositoryDetailPage() {
  const { text } = usePreferences();
  const { repositoryId = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const artifactTarget = searchParams.get("artifact")?.trim() ?? "";
  const referenceTarget = searchParams.get("reference")?.trim() || undefined;
  const versionTarget = searchParams.get("version")?.trim() || undefined;
  const parsedBuildTarget = Number(searchParams.get("build") ?? "");
  const buildTarget =
    Number.isInteger(parsedBuildTarget) && parsedBuildTarget > 0
      ? parsedBuildTarget
      : undefined;
  const [repo, setRepo] = useState<Repository | null>(null);
  const [caps, setCaps] = useState<RepositoryCapabilities | null>(null);
  const [capacity, setCapacity] = useState<RepositoryCapacity | null>(null);
  const [effectiveAccess, setEffectiveAccess] =
    useState<RepositoryEffectiveAccess | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [tab, setTab] = useState<Tab>(() =>
    repositoryTabFromQuery(requestedTab),
  );

  const selectTab = useCallback(
    (nextTab: Tab) => {
      setTab(nextTab);
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          if (nextTab === "artifacts") next.delete("tab");
          else next.set("tab", nextTab);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRepository({
      path: { repositoryId },
    });
    if (err) {
      setError(err);
      return;
    }
    setRepo(data ?? null);
    const [capsRes, accessRes, capacityRes] = await Promise.all([
      getRepositoryCapabilities({ path: { repositoryId } }),
      getRepositoryEffectiveAccess({ path: { repositoryId } }),
      getRepositoryCapacity({ path: { repositoryId } }),
    ]);
    if (!capsRes.error) setCaps(capsRes.data ?? null);
    if (!accessRes.error) setEffectiveAccess(accessRes.data ?? null);
    if (!capacityRes.error) setCapacity(capacityRes.data ?? null);
  }, [repositoryId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setTab(repositoryTabFromQuery(requestedTab));
  }, [requestedTab]);

  useEffect(() => {
    if (!repo) return;
    const available = TABS.some(
      (item) =>
        item.key === tab &&
        (!item.formats || item.formats.includes(repo.format)) &&
        (!item.hostedOnly || repo.type === "hosted") &&
        !(item.key === "publish" && repo.type === "proxy"),
    );
    if (!available) selectTab("artifacts");
  }, [repo, selectTab, tab]);

  if (error !== null) {
    return (
      <div>
        <PageHeader title={text("仓库详情", "Repository details")} />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  }
  if (!repo) return <Loading />;

  return (
    <div>
      <div className="mb-1 text-xs text-zinc-500">
        <Link to="/repositories" className="hover:text-cyan-300">
          {text("仓库", "Repositories")}
        </Link>
        <span className="mx-1.5">/</span>
        <span className="text-zinc-400">{repo.name}</span>
      </div>
      <RepositorySummary
        repo={repo}
        capacity={capacity}
        onOpenCapacity={() => selectTab("capacity")}
      />
      <Tabs
        className="mb-3"
        size="small"
        activeKey={tab}
        onChange={(key) => selectTab(key as Tab)}
        items={TABS.filter(
          (t) =>
            (!t.formats || t.formats.includes(repo.format)) &&
            (!t.hostedOnly || repo.type === "hosted") &&
            !(t.key === "publish" && repo.type === "proxy"),
        ).map((t) => ({ key: t.key, label: text(t.label, t.labelEn) }))}
      />
      <Card bodyClassName="p-4">
        {tab === "artifacts" && (
          <ArtifactsTab
            repo={repo}
            canWrite={effectiveAccess?.permissions.write.allowed === true}
            artifactTarget={artifactTarget}
            buildTarget={buildTarget}
            referenceTarget={referenceTarget}
            versionTarget={versionTarget}
            onVersionChange={(coordinate, version) =>
              setSearchParams(
                (current) => {
                  const next = new URLSearchParams(current);
                  next.set("artifact", coordinate);
                  next.set("version", version);
                  return next;
                },
                { replace: true },
              )
            }
          />
        )}
        {tab === "publish" &&
          repo.format === "maven" &&
          repo.type !== "proxy" && (
            <MavenPublishWizard
              repositoryId={repo.id}
              onPublished={() => selectTab("artifacts")}
            />
          )}
        {tab === "publish" &&
          repo.format === "npm" &&
          repo.type !== "proxy" && <NpmPublishGuide repoName={repo.name} />}
        {tab === "grants" && (
          <>
            {effectiveAccess && (
              <EffectiveAccessPanel effectiveAccess={effectiveAccess} />
            )}
            <GrantsTab repo={repo} />
          </>
        )}
        {tab === "retention" && <RetentionTab repo={repo} />}
        {tab === "capacity" && <CapacityTab repo={repo} />}
        {tab === "distribute" && <DistributeTab repo={repo} />}
        {tab === "jobs" && <JobsTab repo={repo} />}
        {tab === "tombstones" && <TombstonesTab repo={repo} />}
        {tab === "settings" && (
          <RepositorySettingsTab
            repo={repo}
            capabilities={caps}
            onUpdated={load}
          />
        )}
      </Card>
    </div>
  );
}
