import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Input,
  InputNumber,
  Popconfirm,
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
  PlusOutlined,
  SettingOutlined,
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
import { Modal, useDisclosure } from "../components/Modal";
import { OciImageDetail } from "../components/OciImageDetail";
import { MavenPublishWizard } from "../components/MavenPublishWizard";
import {
  MavenArtifactDetail,
  ConanArtifactDetail,
  RawArtifactDetail,
} from "../components/ArtifactRowDetail";
import { RawUploadDialog } from "../components/RawUploadDialog";
import { useAuth } from "../lib/auth";
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
  | "tombstones";

const TABS: {
  key: Tab;
  label: string;
  formats?: string[];
  hostedOnly?: boolean;
}[] = [
  { key: "artifacts", label: "制品" },
  { key: "publish", label: "发布", formats: ["maven"] },
  { key: "grants", label: "访问授权" },
  {
    key: "retention",
    label: "保留策略",
    formats: ["maven", "oci", "conan", "raw"],
    hostedOnly: true,
  },
  { key: "capacity", label: "容量" },
  { key: "distribute", label: "晋升 / 复制" },
  { key: "jobs", label: "生命周期任务" },
  { key: "tombstones", label: "墓碑" },
];

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
      {copied ? "已复制" : "复制"}
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
      if (error || !data) throw new Error("读取上游状态失败");
      setHealth(data);
    } catch (error) {
      setHealthError(
        error instanceof Error ? error.message : "读取上游状态失败",
      );
    }
  }, [repoId, token]);

  useEffect(() => {
    void loadHealth();
  }, [loadHealth]);

  const warm = async () => {
    const path = mavenWarmPath(warmInput);
    if (!path) {
      setWarmError(
        "请输入 Maven GAV（groupId:artifactId:version[:extension[:classifier]]）或仓库路径。",
      );
      return;
    }
    setWarming(true);
    setWarmError("");
    setWarmResult(null);
    try {
      const response = await fetch(`/maven/${repoName}/${path}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      const body = await response.arrayBuffer();
      setWarmResult({ status: response.status, bytes: body.byteLength });
      if (response.ok) onWarmed();
    } catch (error) {
      setWarmError(error instanceof Error ? error.message : "预热请求失败");
    } finally {
      setWarming(false);
    }
  };

  const refresh = async () => {
    const value = warmInput.trim();
    if (!value) {
      setRefreshError("请输入 Maven GAV 或缓存路径。");
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
      if (error || !result) throw new Error("刷新缓存失败");
      setRefreshResult(result);
      onWarmed();
      void loadHealth();
    } catch (error) {
      setRefreshError(error instanceof Error ? error.message : "刷新缓存失败");
    } finally {
      setRefreshing(false);
    }
  };

  const invalidate = async () => {
    const value = invalidateInput.trim();
    if (invalidateScope !== "repository" && !value) {
      setInvalidateError("请输入失效目标。");
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
      if (error || !result) throw new Error("失效缓存失败");
      setInvalidateResult(result.invalidated);
      onWarmed();
    } catch (error) {
      setInvalidateError(
        error instanceof Error ? error.message : "失效缓存失败",
      );
    } finally {
      setInvalidating(false);
    }
  };

  const clearNegative = async () => {
    const path = invalidateInput.trim() ? mavenWarmPath(invalidateInput) : null;
    if (invalidateInput.trim() && !path) {
      setNegativeError("请输入 Maven GAV 或缓存路径。");
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
      if (error || !result) throw new Error("清理负缓存失败");
      setNegativeResult(result.cleared);
      onWarmed();
    } catch (error) {
      setNegativeError(
        error instanceof Error ? error.message : "清理负缓存失败",
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
            上游
          </div>
          <div
            className="mt-1 truncate font-mono text-xs text-zinc-200"
            title={health?.endpoint}
          >
            {health?.endpoint ?? "检查中…"}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            健康
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.reachable ? "text-emerald-300" : "text-rose-300"}`}
          >
            {health
              ? health.reachable
                ? `可达${health.status ? ` · ${health.status}` : ""}`
                : health.error || "不可达"
              : "检查中…"}
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
            缓存
          </div>
          <div className="mt-1 text-xs font-semibold text-zinc-100">
            {health?.cacheEnabled ? "enabled" : "disabled"}
          </div>
        </div>
      </div>
      {healthError && (
        <div className="text-xs text-rose-300">{healthError}</div>
      )}
      <details className="group">
        <summary className="cursor-pointer text-sm font-medium text-zinc-100 hover:text-cyan-300">
          使用方法{" "}
          <span className="text-xs text-zinc-600 group-open:hidden">
            （展开）
          </span>
        </summary>
        <div className="mt-2 space-y-2">
          <div className="text-xs text-zinc-500">
            Maven Proxy Repository 需要 Basic 认证：用户名任意且非空，密码使用
            resolver token。
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            <SnippetBlock label="settings.xml" code={settings} />
            <SnippetBlock label="Docker Maven" code={docker} />
            <SnippetBlock label="直接下载" code={direct} />
          </div>
        </div>
      </details>
      <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-3">
        <div className="mb-2 text-sm font-medium text-zinc-200">预热缓存</div>
        <div className="flex flex-wrap gap-2">
          <Input
            className="min-w-80 flex-1 font-mono"
            placeholder="org.springframework.boot:spring-boot:3.4.4:pom"
            value={warmInput}
            onChange={(e) => setWarmInput(e.target.value)}
          />
          <Button onClick={warm} loading={warming} disabled={!warmInput.trim()}>
            预热
          </Button>
          <Button
            onClick={refresh}
            loading={refreshing}
            disabled={!warmInput.trim()}
          >
            强制刷新
          </Button>
          <Button onClick={() => void loadHealth()}>检查上游</Button>
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
            刷新 HTTP {refreshResult.status}
            {refreshResult.size !== undefined
              ? ` · ${formatBytes(refreshResult.size)}`
              : ""}
          </div>
        )}
      </div>
      <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-3">
        <div className="mb-2 text-sm font-medium text-zinc-200">失效缓存</div>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            className="w-28"
            value={invalidateScope}
            options={[
              { value: "path", label: "路径" },
              { value: "version", label: "版本" },
              { value: "component", label: "组件" },
              { value: "repository", label: "全部" },
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
              按前缀
            </Checkbox>
          )}
          <Button
            onClick={invalidate}
            loading={invalidating}
            disabled={
              invalidateScope !== "repository" && !invalidateInput.trim()
            }
          >
            失效
          </Button>
          <Button onClick={clearNegative} loading={clearingNegative}>
            清理负缓存
          </Button>
        </div>
        <div className="mt-1 text-[11px] text-zinc-600">
          版本、组件和全部会按对应 Maven
          缓存前缀失效；只删除缓存索引，字节对象由 Orphan Collector 延迟回收。
        </div>
        {invalidateError && (
          <div className="mt-2 text-xs text-rose-300">{invalidateError}</div>
        )}
        {invalidateResult !== null && (
          <div className="mt-2 text-xs text-emerald-300">
            已失效 {invalidateResult} 个缓存条目。
          </div>
        )}
        {negativeError && (
          <div className="mt-2 text-xs text-rose-300">{negativeError}</div>
        )}
        {negativeResult !== null && (
          <div className="mt-2 text-xs text-emerald-300">
            已清理 {negativeResult} 个负缓存条目。
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
            主文件大小
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {formatBytes(meta.size)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            文件数
          </div>
          <div className="mt-0.5 text-xs font-semibold text-zinc-100">
            {meta.fileCount ?? meta.files?.length ?? 0}
          </div>
        </div>
        {meta.publisher && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              成员
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
              主文件：{meta.primaryFiles?.join(", ") || "—"}
            </div>
          </div>
          <details className="group">
            <summary className="cursor-pointer text-sm font-medium text-zinc-200 hover:text-cyan-300">
              Maven 坐标用法{" "}
              <span className="text-xs text-zinc-600 group-open:hidden">
                （展开）
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
          文件明细
        </div>
        <div className="divide-y divide-zinc-800/70">
          {primary.map(renderFile)}
          {sidecars.length > 0 && (
            <details className="group">
              <summary className="cursor-pointer px-3 py-2 text-xs text-zinc-500 hover:text-zinc-300">
                校验 / 签名文件（{sidecars.length}）
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
}: {
  repo: Repository;
  canWrite: boolean;
  artifactTarget?: string;
  buildTarget?: number;
}) {
  const { token } = useAuth();
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
              throw new Error("读取 Proxy 缓存失败");
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
      repo.name,
      format,
      proxyMaven,
      proxyAssetFilter,
      token,
      artifactTarget,
      buildTarget,
    ],
  );

  useEffect(() => {
    setQ(artifactTarget);
    void load(artifactTarget);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, artifactTarget]);

  const searchPlaceholder: Record<string, string> = {
    oci: "按镜像名前缀过滤…",
    maven: "搜索 GAV 坐标…",
    conan: "按引用名过滤…",
    raw: "搜索路径…",
  };

  const columns: ColumnsType<ArtifactRow> =
    format === "oci" || format === "conan"
      ? [
          {
            title: "名称",
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
              title: "Maven 坐标",
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
              title: "文件",
              key: "fileCount",
              width: 120,
              render: (_, record) => (
                <Badge tone="zinc">
                  {record.fileCount ?? record.files?.length ?? 0} 个文件
                </Badge>
              ),
            },
            {
              title: "主文件大小",
              key: "size",
              width: 140,
              render: (_, record) => (
                <span className="text-xs text-zinc-400">
                  {formatBytes(record.size)}
                </span>
              ),
            },
            {
              title: "类型",
              dataIndex: "contentType",
              key: "contentType",
              width: 180,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span className="text-xs text-zinc-500">{value ?? "—"}</span>
              ),
            },
          ]
        : format === "maven"
          ? [
              {
                title: "制品",
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
                title: "版本",
                key: "versionCount",
                width: 120,
                render: (_, record) => (
                  <Badge tone="zinc">{record.versionCount ?? 1} 个版本</Badge>
                ),
              },
              {
                title: "最新版本",
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
                title: "更新时间",
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
                title: "坐标",
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
                title: "摘要",
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
                title: "大小",
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
                title: "最后更新时间",
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
          placeholder={searchPlaceholder[format] ?? "搜索…"}
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
          enterButton="搜索"
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
                { value: "primary", label: "主资产" },
                { value: "all", label: "全部文件" },
                { value: "jar", label: "仅 JAR" },
                { value: "pom", label: "仅 POM" },
              ]}
              onChange={(value: ProxyMavenAssetFilter) => {
                setProxyAssetFilter(value);
                setExpandedImage(null);
              }}
            />
            <span className="text-xs text-zinc-500">
              {formatNumber(proxyTotal)} 个 Maven 版本，当前显示{" "}
              {formatNumber(rows.length)} 个
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
            返回完整列表
          </Button>
        )}
      </div>
      {error !== null ? (
        isNotFound(error) ? (
          <NotEnabled feature="制品浏览" />
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
            title={q ? "没有匹配的制品" : "暂无制品"}
            hint={
              q
                ? "换个关键词试试"
                : proxyMaven
                  ? "通过 Maven 客户端拉取依赖后会显示代理缓存"
                  : `通过 ${format} 客户端推送制品后会显示在这里`
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
              y: "calc(100vh - 430px)",
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
              第 {formatNumber(proxyPage)} 页，每页{" "}
              {formatNumber(PROXY_MAVEN_PAGE_SIZE)} 个 Maven 版本
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

function resourcePrefixHint(format: Repository["format"]): string {
  switch (format) {
    case "maven":
      return "例如 org/example（Maven group 前缀）";
    case "oci":
      return "例如 team/backend（镜像名称前缀）";
    case "conan":
      return "例如 pkg/1.0/user/stable（reference 前缀）";
    case "raw":
      return "例如 releases/2026（路径前缀）";
  }
}

function grantLevelLabel(level: GrantLevel): string {
  if (level === "admin") return "管理员";
  if (level === "write") return "写入";
  return "读取";
}

function accessSourceLabel(source: string): string {
  switch (source) {
    case "administrator":
      return "管理员身份";
    case "role":
      return "全局角色";
    case "repository_grants":
      return "仓库授权";
    case "legacy_static":
      return "旧版静态策略";
    case "anonymous_policy":
      return "匿名访问策略";
    default:
      return source || "未说明";
  }
}

function accessReasonLabel(reason: string): string {
  const labels: Record<string, string> = {
    administrator: "管理员直接放行",
    role_admin: "全局 admin 角色",
    role_writer: "全局 writer 角色",
    role_reader: "全局 reader 角色",
    scope_granted: "匹配主体、权限和资源范围",
    scope_not_granted: "没有匹配的仓库授权",
    grant_lookup_failed: "读取仓库授权失败",
    read_pattern_granted: "匹配旧版读取规则",
    write_pattern_granted: "匹配旧版写入规则",
    global_anonymous_access_disabled: "全局匿名读取未启用",
    repository_anonymous_read_disabled: "仓库未允许匿名读取",
    repository_anonymous_read_enabled: "全局和仓库均允许匿名读取",
  };
  return labels[reason] ?? reason.replaceAll("_", " ");
}

function grantLevel(scopes: Grant["scopes"]): GrantLevel {
  if (scopes.includes("repositories:admin")) return "admin";
  if (scopes.includes("repositories:write")) return "write";
  return "read";
}

function scopesForLevel(level: GrantLevel): Grant["scopes"] {
  return [`repositories:${level}`] as Grant["scopes"];
}

function principalOptions(users: User[], apiKeys: ApiKey[]): PrincipalOption[] {
  return [
    ...users.map((user) => ({
      value: `user:${user.name}`,
      label: `用户 · ${user.name}`,
      detail: `全局角色 ${user.role}${user.state === "disabled" ? " · 已停用" : ""}`,
      disabled: user.state === "disabled",
    })),
    ...apiKeys.map((key) => ({
      value: `api-key:${key.id}`,
      label: `API Key · ${key.name}`,
      detail: `全局角色 ${key.roles.join(", ")}${key.revokedAt ? " · 已撤销" : ""}`,
      disabled: Boolean(key.revokedAt),
    })),
  ];
}

function GrantsTab({ repo }: { repo: Repository }) {
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
          new Error("无法加载用户或 API Key 列表，可继续使用自定义身份。"),
        );
      }
      setPrincipalChoices(
        principalOptions(
          usersResult.data?.items ?? [],
          apiKeysResult.data?.items ?? [],
        ),
      );
    })();
    return () => {
      cancelled = true;
    };
  }, []);

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
        new Error("请为每条授权规则选择或填写授权主体；不需要的空行请先移除。"),
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
          new Error("存在重复的授权主体与资源范围，请合并或删除重复规则。"),
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
      <NotEnabled feature="访问授权" />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!grants) return <Loading />;

  const grantColumns: ColumnsType<Grant> = [
    {
      title: "授权主体",
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
      title: "权限级别",
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
          {grantLevelLabel(grantLevel(grant.scopes))}
        </Badge>
      ),
    },
    {
      title: "资源范围",
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 260,
      render: (value?: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {value || "整个仓库"}
        </span>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <Button type="primary" onClick={openEditor}>
          编辑授权
        </Button>
      </div>
      {grants.length === 0 ? (
        <EmptyState
          title="暂无授权规则"
          hint="在编辑授权中选择用户、API Key，或填写 OIDC subject / 自定义 actor。"
        />
      ) : (
        <Table<Grant>
          className="ag-console-table"
          rowKey={(grant, index) =>
            `${grant.principal}-${grant.resourcePrefix ?? ""}-${index}`
          }
          size="middle"
          dataSource={grants}
          columns={grantColumns}
          pagination={false}
          scroll={{ x: 760 }}
        />
      )}
      <Modal
        open={editor.open}
        title="编辑访问授权"
        onClose={editor.hide}
        wide
        footer={
          <Space>
            <Button onClick={editor.hide}>取消</Button>
            <Button type="primary" onClick={save} loading={saving}>
              保存
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 px-3 py-3 text-xs leading-5 text-zinc-400">
            <div className="font-medium text-cyan-200">先记住这三个概念</div>
            <div className="mt-1 grid gap-1.5 sm:grid-cols-3">
              <div>
                <span className="font-medium text-zinc-300">主体</span>
                ：谁在访问，例如用户或 CI 的 API Key。
              </div>
              <div>
                <span className="font-medium text-zinc-300">权限</span>
                ：允许读取、写入，还是管理仓库。
              </div>
              <div>
                <span className="font-medium text-zinc-300">范围</span>
                ：限制到仓库的一部分；留空就是整个仓库。
              </div>
            </div>
            <div className="mt-2 border-t border-cyan-500/10 pt-2 text-zinc-500">
              用户/API Key
              的全局角色会先生效；仓库规则只能追加权限，不能撤销全局角色。
            </div>
          </div>
          {principalChoicesError !== null && (
            <div className="rounded-md border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-xs text-amber-200">
              用户和 API Key 列表暂时不可用；仍可选择“OIDC / 自定义
              actor”并填写主体标识。
            </div>
          )}
          <div className="min-w-[1020px]">
            <div className="grid grid-cols-[172px_minmax(280px,1.35fr)_188px_minmax(260px,1.2fr)_72px] items-center gap-3 px-3 pb-2 text-[11px] font-medium uppercase tracking-wider text-zinc-500">
              <span>主体类型</span>
              <span>主体</span>
              <span>权限级别</span>
              <span>资源范围</span>
              <span className="text-right">操作</span>
            </div>
            <div className="space-y-2">
              {draft.map((g, i) => {
                const kind = principalEditorKind(g.principal);
                return (
                  <div
                    key={i}
                    className="rounded-lg border border-zinc-800 bg-zinc-950/20 p-3"
                  >
                    <div className="grid grid-cols-[172px_minmax(280px,1.35fr)_188px_minmax(260px,1.2fr)_72px] items-start gap-3">
                      <div className="min-w-0">
                        <Select
                          className="w-full"
                          value={kind}
                          options={[
                            { value: "", label: "选择主体类型" },
                            { value: "user", label: "用户账号" },
                            {
                              value: "api-key",
                              label: "API Key（CI / 自动化）",
                            },
                            { value: "custom", label: "OIDC / 自定义 actor" },
                          ]}
                          onChange={(nextKind: PrincipalKind | "") => {
                            const first =
                              nextKind === "user"
                                ? (principalChoices.find(
                                    (choice) =>
                                      choice.value.startsWith("user:") &&
                                      !choice.disabled,
                                  )?.value ?? "")
                                : nextKind === "api-key"
                                  ? (principalChoices.find(
                                      (choice) =>
                                        choice.value.startsWith("api-key:") &&
                                        !choice.disabled,
                                    )?.value ?? "")
                                  : "";
                            setDraft((d) =>
                              d.map((x, j) =>
                                j === i
                                  ? {
                                      ...x,
                                      principal:
                                        nextKind === "custom"
                                          ? principalEditorKind(x.principal) ===
                                            "custom"
                                            ? x.principal
                                            : CUSTOM_PRINCIPAL
                                          : first,
                                    }
                                  : x,
                              ),
                            );
                          }}
                        />
                        <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                          选择规则主体的来源
                        </div>
                      </div>
                      <div className="min-w-0">
                        {kind === "user" || kind === "api-key" ? (
                          <Select
                            className="w-full font-mono"
                            showSearch={{ optionFilterProp: "label" }}
                            value={g.principal || undefined}
                            placeholder="请选择主体"
                            options={[
                              { value: "", label: "请选择主体" },
                              ...principalChoices
                                .filter((choice) =>
                                  choice.value.startsWith(`${kind}:`),
                                )
                                .map((choice) => ({
                                  value: choice.value,
                                  label: `${choice.label.replace(/^(用户|API Key) · /, "")} · ${choice.detail}`,
                                  disabled: choice.disabled,
                                })),
                            ]}
                            onChange={(value) =>
                              setDraft((d) =>
                                d.map((x, j) =>
                                  j === i ? { ...x, principal: value } : x,
                                ),
                              )
                            }
                          />
                        ) : kind === "custom" ? (
                          <Input
                            className="font-mono"
                            placeholder="例如 oidc:github:acme/release 或 ci-bot"
                            value={
                              g.principal === CUSTOM_PRINCIPAL
                                ? ""
                                : g.principal
                            }
                            onChange={(e) =>
                              setDraft((d) =>
                                d.map((x, j) =>
                                  j === i
                                    ? { ...x, principal: e.target.value }
                                    : x,
                                ),
                              )
                            }
                          />
                        ) : (
                          <div className="flex h-10 items-center rounded-md border border-dashed border-zinc-800 px-3 text-xs text-zinc-600">
                            先选择主体类型
                          </div>
                        )}
                        <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                          {kind === "custom"
                            ? "必须与认证系统传入的 actor 完全一致。"
                            : "从已有身份中选择授权对象。"}
                        </div>
                      </div>
                      <div className="min-w-0">
                        <Select
                          className="w-full"
                          value={grantLevel(g.scopes)}
                          options={[
                            { value: "read", label: "读取 · 浏览 / 拉取" },
                            { value: "write", label: "写入 · 发布 / 编辑" },
                            { value: "admin", label: "管理员 · 授权 / 删除" },
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
                        <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                          权限会叠加全局角色
                        </div>
                      </div>
                      <div className="min-w-0">
                        <Input
                          className="font-mono"
                          placeholder="留空表示整个仓库"
                          value={g.resourcePrefix ?? ""}
                          onChange={(e) =>
                            setDraft((d) =>
                              d.map((x, j) =>
                                j === i
                                  ? { ...x, resourcePrefix: e.target.value }
                                  : x,
                              ),
                            )
                          }
                        />
                        <div className="mt-1 min-h-4 text-[10px] leading-4 text-zinc-600">
                          {resourcePrefixHint(repo.format)}
                        </div>
                      </div>
                      <Button
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() =>
                          setDraft((d) => d.filter((_, j) => j !== i))
                        }
                      >
                        移除
                      </Button>
                    </div>
                  </div>
                );
              })}
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
            添加授权规则
          </Button>
        </div>
      </Modal>
    </div>
  );
}

/* ---------------- Retention ---------------- */

const RETENTION_DRY_RUN_PAGE_SIZE = 100;

function retentionFormatCopy(format: Repository["format"]) {
  switch (format) {
    case "oci":
      return {
        ageLabel: "镜像版本保留天数",
        ageHint: "Manifest 创建超过此天数后，才会进入清理候选。",
        minimumLabel: "每个镜像最少保留版本",
        minimumHint: "按镜像名称分组，始终保护最新的这些 manifest。",
        maximumLabel: "每个镜像最多保留版本",
        maximumHint: "0 表示不限制；超过上限的旧 manifest 会进入候选。",
        matchLabel: "只清理匹配镜像",
        matchHint: "可匹配镜像名、name@digest 或 name:tag；留空表示全部。",
        protectLabel: "保护镜像版本",
        protectHint: "可用镜像名保护全部版本，或用 digest、tag 精确保护。",
        matchPlaceholder: "如 ^team/backend(@|:)",
        protectPlaceholder: "如 ^team/backend:stable$",
        candidateName: "镜像版本",
      };
    case "conan":
      return {
        ageLabel: "Recipe revision 保留天数",
        ageHint: "Recipe revision 创建超过此天数后，才会进入清理候选。",
        minimumLabel: "每个 reference 最少保留版本",
        minimumHint:
          "按完整 Conan reference 分组，保护最新的 recipe revisions。",
        maximumLabel: "每个 reference 最多保留版本",
        maximumHint:
          "0 表示不限制；清理 recipe revision 时会同时隐藏其二进制包。",
        matchLabel: "只清理匹配 reference",
        matchHint: "可匹配完整 reference 或 reference#recipe-revision。",
        protectLabel: "保护 Conan 版本",
        protectHint:
          "匹配 reference 可保护全部 revisions，精确坐标只保护一个版本。",
        matchPlaceholder: "如 ^openssl/3\\.",
        protectPlaceholder: "如 @release/stable(#|$)",
        candidateName: "Recipe revision",
      };
    case "raw":
      return {
        ageLabel: "资产未更新保留天数",
        ageHint: "路径资产超过此天数未更新后，才会进入清理候选。",
        minimumLabel: "",
        minimumHint: "",
        maximumLabel: "",
        maximumHint: "",
        matchLabel: "只清理匹配路径",
        matchHint: "可选 RE2 路径正则；留空表示匹配仓库内全部资产。",
        protectLabel: "保护路径",
        protectHint: "匹配任一正则的路径永不进入清理候选。",
        matchPlaceholder: "如 ^releases/nightly/",
        protectPlaceholder: "如 ^releases/stable/",
        candidateName: "路径资产",
      };
    default:
      return {
        ageLabel: "发布版本保留天数",
        ageHint: "发布版本创建超过此天数后，才会进入清理候选。",
        minimumLabel: "每个模块最少保留版本",
        minimumHint: "按 groupId:artifactId 分组，始终保护最新的这些版本。",
        maximumLabel: "每个模块最多保留版本",
        maximumHint: "0 表示不限制；超过上限的旧版本会进入候选。",
        matchLabel: "只清理匹配坐标",
        matchHint: "可选 RE2 正则；留空表示匹配全部 Maven 坐标。",
        protectLabel: "保护 Maven 坐标",
        protectHint: "匹配任一正则的坐标永不进入清理候选。",
        matchPlaceholder: "如 ^com\\.example:",
        protectPlaceholder: "如 ^com\\.example:platform:",
        candidateName: "制品版本",
      };
  }
}

function retentionCandidateTypeLabel(
  versionType: RetentionDryRun["candidates"][number]["versionType"],
  format: Repository["format"],
) {
  if (versionType === "snapshot") return "快照构建";
  if (versionType === "release") return "发布版本";
  if (versionType === "asset") return "路径资产";
  return format === "oci" ? "Manifest 版本" : "Recipe revision";
}

function RetentionTab({ repo }: { repo: Repository }) {
  const { token } = useAuth();
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
  const copy = retentionFormatCopy(repo.format);

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
      setSaveError(new Error("最多保留版本数必须为 0，或不小于最少保留版本数"));
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
    setNotice("策略已保存");
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
        setSaveError(new Error("试运行结果已过期或策略已变化，请重新试运行"));
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
          new Error("保留策略已变化，当前预览不再有效，请重新试运行"),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    setNotice("保留执行任务已提交，请在「生命周期任务」标签页查看进度");
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
          headers: { Authorization: `Bearer ${token}` },
        },
      );
      if (!response.ok) {
        const problem = (await response.json().catch(() => null)) as {
          message?: string;
        } | null;
        throw new Error(problem?.message ?? "导出试运行结果失败");
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
      <NotEnabled feature="保留策略" />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!policy) return <Loading />;

  if (
    repo.type !== "hosted" ||
    !["maven", "oci", "conan", "raw"].includes(repo.format)
  ) {
    return <NotEnabled feature="Hosted 仓库保留策略" />;
  }

  const dryRunColumns: ColumnsType<RetentionDryRun["candidates"][number]> = [
    {
      title: "清理单位",
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
      title: "摘要",
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
      title: "类型",
      key: "versionType",
      width: 140,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {retentionCandidateTypeLabel(candidate.versionType, candidate.format)}
        </span>
      ),
    },
    {
      title: "原因",
      key: "reasons",
      width: 280,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {candidate.reasons
            .map((reason) =>
              reason === "maximum_versions"
                ? "超过版本上限"
                : candidate.versionType === "asset"
                  ? `已 ${candidate.ageDays} 天未更新`
                  : `已保留 ${candidate.ageDays} 天`,
            )
            .join("、")}
        </span>
      ),
    },
    {
      title: isRaw ? "最后更新时间" : "创建时间",
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
          title="Raw 按路径资产清理"
          description="Raw 没有版本分组，因此不应用最少或最多版本数；期限按资产最后更新时间计算。"
        />
      )}
      <div className="flex items-center justify-between border-b border-zinc-800 pb-4">
        <div>
          <div className="text-sm font-medium text-zinc-200">自动清理</div>
          <div className="mt-1 text-xs text-zinc-500">
            关闭时不会创建定时或手动清理任务，已有墓碑不受影响。
          </div>
        </div>
        <Switch
          checked={enabled}
          checkedChildren="已启用"
          unCheckedChildren="已停用"
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
            <Space.Addon>天</Space.Addon>
          </Space.Compact>
        </Field>
        {isMaven && (
          <Field
            label="快照版本保留天数"
            hint="Maven SNAPSHOT 可使用独立于发布版本的保留期限。"
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
              <Space.Addon>天</Space.Addon>
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
              <Space.Addon>个</Space.Addon>
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
              <Space.Addon>个</Space.Addon>
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
          保存策略
        </Button>
        <Button onClick={runDryRun} loading={dryRunning} disabled={!enabled}>
          试运行
        </Button>
        {dryRun && dryRun.candidates.length > 0 && (
          <Popconfirm
            title="确认执行保留清理？"
            description={`将清理全部 ${dryRun.totalCandidates} 个候选${copy.candidateName}；执行前会再次校验策略版本。`}
            okText="执行清理"
            cancelText="取消"
            okButtonProps={{ danger: true, loading: executing }}
            onConfirm={execute}
          >
            <Button danger loading={executing} disabled={!enabled}>
              执行清理（{dryRun.totalCandidates} 个）
            </Button>
          </Popconfirm>
        )}
      </Space>
      {dryRun && (
        <Card>
          <CardHeader
            title={`试运行结果：已加载 ${dryRun.candidates.length} / 共 ${dryRun.totalCandidates} 个候选${copy.candidateName}（策略版本 ${dryRun.policyVersion}）`}
            extra={
              dryRun.totalCandidates > 0 ? (
                <Tooltip title="导出完整候选集，不受当前分页影响">
                  <Button
                    size="small"
                    icon={<DownloadOutlined />}
                    loading={exporting}
                    onClick={() => void exportDryRun()}
                  >
                    导出 CSV
                  </Button>
                </Tooltip>
              ) : undefined
            }
          />
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2 border-b border-zinc-800/80 px-4 py-3 text-xs text-zinc-400">
            <span>
              按期限{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.age}
              </strong>
            </span>
            <span>
              超过版本上限{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.maximumVersions}
              </strong>
            </span>
            <span>
              类型：
              {[
                ["发布", dryRun.summary.versionTypeCounts.release],
                ["快照", dryRun.summary.versionTypeCounts.snapshot],
                ["版本", dryRun.summary.versionTypeCounts.version],
                ["资产", dryRun.summary.versionTypeCounts.asset],
              ]
                .filter(([, count]) => Number(count) > 0)
                .map(([label, count]) => `${label} ${count}`)
                .join("、") || "无"}
            </span>
            <span>最早候选 {formatDate(dryRun.summary.oldestCandidateAt)}</span>
          </div>
          {dryRun.candidates.length === 0 ? (
            <EmptyState title={`没有需要清理的${copy.candidateName}`} />
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
                加载更多候选
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
    setNotice("配额已更新");
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature="容量管理" />
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
          ? "Proxy Repository 的容量来自 read-through cache：已缓存的上游响应会计入缓存用量；它不是 Hosted 发布制品。"
          : "Hosted Repository 的容量来自已发布或可恢复的 Artifact/Asset 引用，并受发布配额约束。"}
      </div>
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {proxy ? "缓存用量" : "已用空间"}
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {formatBytes(capacity.usedBytes)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            {proxy ? "缓存对象" : "对象数量"}
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {formatNumber(capacity.objectCount)}
          </div>
        </div>
        <div className="rounded-lg border border-zinc-800 px-4 py-3">
          <div className="text-xs uppercase tracking-wider text-zinc-500">
            配额
          </div>
          <div className="mt-1 text-xl font-semibold text-zinc-100">
            {capacity.quotaBytes > 0
              ? formatBytes(capacity.quotaBytes)
              : "无限制"}
          </div>
        </div>
      </div>
      {proxy && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              主资产缓存
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.primaryBytes)}
            </div>
          </div>
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              校验/签名缓存
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.sidecarBytes)}
            </div>
          </div>
          <div className="rounded-lg border border-zinc-800 px-4 py-3">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              可回收缓存
            </div>
            <div className="mt-1 text-lg font-semibold text-zinc-100">
              {formatBytes(capacity.reclaimableBytes)}
            </div>
            <div className="mt-1 text-xs text-zinc-500">
              过期 {formatNumber(capacity.expiredObjectCount)} 项 · negative{" "}
              {formatNumber(capacity.negativeCount)} 项
            </div>
          </div>
        </div>
      )}
      {capacity.quotaBytes > 0 && (
        <div>
          <div className="mb-1.5 flex justify-between text-xs text-zinc-500">
            <span>使用率</span>
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
        <Field label="配额 (GiB，0 表示无限制)">
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
          更新配额
        </Button>
      </div>
    </div>
  );
}

/* ---------------- Distribute (Promotion / Replication) ---------------- */

function DistributeTab({ repo }: { repo: Repository }) {
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
    setNotice("已取消复制计划，工作进程不再重试。");
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
        ? "晋升任务已提交，请在「生命周期任务」查看进度"
        : "复制计划已创建，下方查看进度",
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
      title: "目标仓库",
      dataIndex: "targetRepositoryId",
      key: "targetRepositoryId",
      width: 220,
      render: (value: string) => (
        <span className="text-xs text-zinc-300">{repoName(value)}</span>
      ),
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "创建时间",
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
      title: "完成时间",
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
      title: "",
      key: "actions",
      fixed: "right",
      width: 180,
      render: (_, plan) => (
        <Space size="small">
          <Button size="small" onClick={() => showDetail(plan.id)}>
            进度
          </Button>
          {(plan.state === "pending" || plan.state === "failed") && (
            <Popconfirm
              title="确认取消复制计划？"
              description="取消后工作进程将不再重试，已复制的字节不会自动删除。"
              okText="确认取消"
              cancelText="返回"
              okButtonProps={{ danger: true }}
              onConfirm={() => cancelPlan(plan.id)}
            >
              <Button size="small" danger>
                取消
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
      title: "对象",
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
      title: "摘要",
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
      title: "大小",
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (value: number) => (
        <span className="text-xs text-zinc-400">{formatBytes(value)}</span>
      ),
    },
    {
      title: "进度",
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
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "重试",
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
          发起晋升 / 复制
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
          <Field label="目标仓库">
            <Select
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder="选择同格式仓库…"
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              onChange={setTargetId}
            />
          </Field>
          <Field label="坐标 coordinate">
            <Input
              className="font-mono"
              placeholder="如 nginx:alpine 或 GAV"
              value={coordinate}
              onChange={(e) => setCoordinate(e.target.value)}
            />
          </Field>
          <Field label="摘要 digest">
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
              晋升
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
              复制
            </Button>
          </div>
        </div>
        <p className="mt-2 text-xs text-zinc-600">
          晋升：在目标仓库创建同一制品的可见副本（审计追踪）；复制：异步、带断点地拷贝制品字节到目标仓库。
        </p>
      </div>

      {/* 复制计划列表 */}
      <div>
        <div className="mb-2 text-sm font-medium text-zinc-200">
          复制计划（{plans?.length ?? 0}）
        </div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title="暂无复制计划" />
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
        title="复制进度详情"
        onClose={() => setDetail(null)}
        wide
      >
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-zinc-400">
              <span>
                状态：
                <StateBadge state={detail.state} />
              </span>
              <span>目标：{repoName(detail.targetRepositoryId)}</span>
              <span>创建：{formatDate(detail.createdAt)}</span>
              {detail.lastError && (
                <span className="text-rose-400">{detail.lastError}</span>
              )}
            </div>
            {detail.checkpoints.length === 0 ? (
              <p className="py-4 text-center text-sm text-zinc-500">
                暂无检查点
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
      <NotEnabled feature="生命周期任务" />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );

  if (!jobs) return <Loading />;
  if (jobs.length === 0)
    return (
      <EmptyState
        title="暂无生命周期任务"
        hint="保留清理、晋升、复制任务会显示在这里"
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
      title: "类型",
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (value: string) => <Badge tone="blue">{value}</Badge>,
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "创建时间",
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
      title: "完成时间",
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
      title: "错误",
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
    setRestoreNotice(`已恢复 ${coordinate}`);
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <NotEnabled feature="墓碑管理" />
    ) : (
      <ErrorBanner error={error} onRetry={() => load()} />
    );
  if (loading) return <Loading />;

  const tombstoneColumns: ColumnsType<ArtifactTombstone> = [
    {
      title: "坐标",
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
      title: "摘要",
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
      title: "删除时间",
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
      title: "",
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, item) => (
        <Popconfirm
          title="确认恢复此制品？"
          description="恢复后制品会重新出现在仓库浏览与协议读取中。"
          okText="恢复"
          cancelText="取消"
          onConfirm={() => restore(item.coordinate)}
        >
          <Button size="small" loading={restoring === item.coordinate}>
            恢复
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
          title="暂无墓碑"
          hint="被删除的制品会保留墓碑记录，可在此恢复"
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
  return (
    <EmptyState
      title={`${feature}功能未启用`}
      hint="当前后端构建尚未挂载此管理端点（返回 404）"
    />
  );
}

function RepositoryOverview({
  repo,
  capacity,
  onOpenCapacity,
}: {
  repo: Repository;
  capacity: RepositoryCapacity | null;
  onOpenCapacity: () => void;
}) {
  const usage = capacity?.quotaBytes
    ? Math.min(100, (capacity.usedBytes / capacity.quotaBytes) * 100)
    : null;
  const usageTone =
    usage !== null && usage > 90
      ? "text-rose-300"
      : usage !== null && usage > 70
        ? "text-amber-300"
        : "text-emerald-300";
  const protocolPath = `${window.location.origin}/${repo.format}/${repo.name}`;

  return (
    <div className="mb-4 overflow-hidden rounded-lg border border-zinc-800/80 bg-zinc-900/25">
      <div className="flex items-center justify-between gap-6 border-b border-zinc-800/70 px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-3">
          <span
            className={`h-2 w-2 shrink-0 rounded-full ${repo.state === "active" ? "bg-emerald-400" : repo.state === "deleting" ? "bg-amber-400" : "bg-rose-400"}`}
          />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs font-semibold text-zinc-100">
                {repo.state === "active"
                  ? "仓库运行正常"
                  : `仓库状态：${repo.state}`}
              </span>
              <Badge tone={repo.type === "proxy" ? "amber" : "cyan"}>
                {repo.type ?? "hosted"}
              </Badge>
              <Badge tone={repo.anonymousRead ? "green" : "zinc"}>
                {repo.anonymousRead ? "允许匿名读取" : "私有读取"}
              </Badge>
            </div>
            <p className="mt-0.5 truncate text-[11px] text-zinc-500">
              {repo.type === "proxy"
                ? "上游缓存与代理请求由此仓库处理。"
                : "已发布制品及其可恢复引用由此仓库托管。"}
            </p>
          </div>
        </div>
        <div className="flex min-w-0 shrink-0 items-center gap-2">
          <span className="text-[10px] uppercase tracking-wider text-zinc-600">
            协议入口
          </span>
          <code
            className="max-w-[30rem] truncate font-mono text-xs text-zinc-300"
            title={protocolPath}
          >
            {protocolPath}
          </code>
          <CopyButton text={protocolPath} />
        </div>
      </div>
      <div className="grid grid-cols-3 divide-x divide-zinc-800/80">
        <div className="px-4 py-2.5">
          <div className="text-[10px] font-medium uppercase tracking-wider text-zinc-600">
            已用空间
          </div>
          <div className="mt-1 text-sm font-semibold text-zinc-100">
            {capacity ? formatBytes(capacity.usedBytes) : "读取中…"}
          </div>
        </div>
        <div className="px-4 py-2.5">
          <div className="text-[10px] font-medium uppercase tracking-wider text-zinc-600">
            对象数量
          </div>
          <div className="mt-1 text-sm font-semibold text-zinc-100">
            {capacity ? formatNumber(capacity.objectCount) : "—"}
          </div>
        </div>
        <div className="px-4 py-2.5">
          <div className="text-[10px] font-medium uppercase tracking-wider text-zinc-600">
            配额状态
          </div>
          <div className={`mt-1 text-sm font-semibold ${usageTone}`}>
            {usage === null ? "未设置限制" : `${usage.toFixed(1)}% 已使用`}
          </div>
          <Button
            type="link"
            size="small"
            className="mt-0.5 h-auto p-0 text-xs"
            onClick={onOpenCapacity}
          >
            查看详情
          </Button>
        </div>
      </div>
    </div>
  );
}

function EditRepositoryDialog({
  repo,
  onUpdated,
}: {
  repo: Repository;
  onUpdated: () => void;
}) {
  const dialog = useDisclosure();
  const [endpoint, setEndpoint] = useState(repo.endpoint ?? "");
  const [hosts, setHosts] = useState((repo.allowedHosts ?? []).join(", "));
  const [anonymousRead, setAnonymousRead] = useState(repo.anonymousRead);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

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
    dialog.hide();
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
    <>
      <Button
        icon={<SettingOutlined />}
        variant="outlined"
        onClick={() => {
          resetForm();
          dialog.show();
        }}
      >
        设置
      </Button>
      <Modal
        open={dialog.open}
        title={`设置仓库：${repo.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide}>取消</Button>
            <Button type="primary" onClick={submit} loading={saving}>
              保存
            </Button>
          </Space>
        }
      >
        <Space orientation="vertical" size="large" className="w-full">
          <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3">
            <div>
              <div className="text-sm font-medium text-zinc-200">
                允许匿名读取
              </div>
              <div className="mt-1 text-xs leading-5 text-zinc-500">
                开启后协议层 GET/HEAD 可在无需凭据时读取该 Repository。
              </div>
            </div>
            <Switch checked={anonymousRead} onChange={setAnonymousRead} />
          </div>
          {repo.type === "proxy" && (
            <Space orientation="vertical" size="middle" className="w-full">
              <Field
                label="上游地址"
                hint="https 基础地址，修改后立即生效（按请求读取）。"
              >
                <Input
                  placeholder="https://upstream.example"
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label="允许主机"
                hint={
                  requiresHosts
                    ? "逗号分隔，raw / conan 代理必填。"
                    : "逗号分隔；oci / maven 代理可留空。"
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
                  出口代理
                </div>
                <div className="mt-1 text-xs leading-5 text-zinc-500">
                  配置此代理仓库访问上游时的出口网络代理，用于企业内网或受限网络环境。
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
                    <span className="text-sm text-zinc-200">直连</span>
                    <span className="ml-2 text-xs text-zinc-500">
                      不经过任何代理，保留私网地址防护
                    </span>
                  </Radio>
                  <Radio value="environment">
                    <span className="text-sm text-zinc-200">跟随环境变量</span>
                    <span className="ml-2 text-xs text-zinc-500">
                      沿用进程级 HTTP(S)_PROXY 与 NO_PROXY
                    </span>
                  </Radio>
                  <Radio value="custom">
                    <span className="text-sm text-zinc-200">自定义代理</span>
                    <span className="ml-2 text-xs text-zinc-500">
                      为此仓库单独指定 HTTP 或 SOCKS5 代理
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
                      <Field label="协议">
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
                      <Field label="代理主机">
                        <Input
                          className="w-64"
                          placeholder="proxy.corp.example"
                          value={egressHost}
                          onChange={(e) => setEgressHost(e.target.value)}
                        />
                      </Field>
                      <Field label="端口">
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
                            远程 DNS（socks5h）
                          </div>
                          <div className="mt-1 text-xs leading-5 text-zinc-600">
                            开启后由代理服务器解析上游域名，适用于本地 DNS
                            不可达上游的网络。
                          </div>
                        </div>
                        <Switch
                          checked={egressRemoteDns}
                          onChange={setEgressRemoteDns}
                        />
                      </div>
                    )}
                    <div className="flex flex-wrap gap-3">
                      <Field label="代理认证用户名（可选）">
                        <Input
                          className="w-64"
                          placeholder="gateway"
                          value={egressUsername}
                          onChange={(e) => setEgressUsername(e.target.value)}
                        />
                      </Field>
                      <Field
                        label="代理认证密码（可选）"
                        hint="AES-256-GCM 加密落库，留空则保留已存凭据。"
                      >
                        <Input.Password
                          className="w-64"
                          placeholder={
                            repo.egressProxy?.credentialsConfigured
                              ? "已配置，输入以替换"
                              : "未配置"
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
                          清除已存储的代理凭据
                        </span>
                      </Checkbox>
                    )}
                    <Field
                      label="绕过列表（noProxy）"
                      hint="逗号分隔的主机后缀或网段；命中的上游将绕过代理直连。"
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
                    测试连接
                  </Button>
                  <span className="text-xs text-zinc-600">
                    测试使用已保存的配置
                  </span>
                  {egressTestResult &&
                    (egressTestResult.reachable ? (
                      <span className="text-xs text-emerald-400">
                        代理可达
                        {egressTestResult.upstreamStatus
                          ? ` · 上游返回 ${egressTestResult.upstreamStatus}`
                          : ""}
                        {egressTestResult.latencyMs !== undefined
                          ? ` · 延迟 ${egressTestResult.latencyMs} ms`
                          : ""}
                      </span>
                    ) : (
                      <span className="text-xs text-red-400">
                        连接失败：{egressTestResult.error ?? "未知错误"}
                      </span>
                    ))}
                </div>
              </div>
            </Space>
          )}
          {error ? <ErrorBanner error={error} /> : null}
        </Space>
      </Modal>
    </>
  );
}

export function RepositoryDetailPage() {
  const { repositoryId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const artifactTarget = searchParams.get("artifact")?.trim() ?? "";
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
  const [tab, setTab] = useState<Tab>("artifacts");

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
    if (repo?.type === "proxy" && tab === "publish") {
      setTab("artifacts");
    }
  }, [repo?.type, tab]);

  if (error !== null) {
    return (
      <div>
        <PageHeader title="仓库详情" />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  }
  if (!repo) return <Loading />;

  return (
    <div>
      <div className="mb-1 text-xs text-zinc-500">
        <Link to="/repositories" className="hover:text-cyan-300">
          仓库
        </Link>
        <span className="mx-1.5">/</span>
        <span className="text-zinc-400">{repo.name}</span>
      </div>
      <PageHeader
        title={repo.name}
        description={`ID: ${repo.id} · 版本 v${repo.version}`}
        actions={
          <div className="flex items-center gap-2">
            <EditRepositoryDialog repo={repo} onUpdated={load} />
            <FormatBadge format={repo.format} />
            <StateBadge state={repo.state} />
          </div>
        }
      />
      {repo.anonymousRead && (
        <div className="mb-3 flex items-center justify-between gap-3 border-b border-emerald-500/20 pb-2 text-xs">
          <span className="text-emerald-200">
            此仓库允许匿名读取；全局匿名策略启用时可公开浏览。
          </span>
          <Link
            to={`/browse?repository=${encodeURIComponent(repo.id)}`}
            className="text-xs font-medium text-cyan-300 hover:text-cyan-200"
          >
            打开公开浏览
          </Link>
        </div>
      )}
      <RepositoryOverview
        repo={repo}
        capacity={capacity}
        onOpenCapacity={() => setTab("capacity")}
      />
      {caps && (
        <div className="mb-3 flex flex-wrap items-center gap-1.5 text-[11px] text-zinc-500">
          <span className="mr-1">支持的操作:</span>
          {caps.operations.map((op) => (
            <Badge key={op} tone="zinc">
              {op}
            </Badge>
          ))}
        </div>
      )}
      {effectiveAccess && (
        <Collapse
          ghost
          className="mb-3"
          items={[
            {
              key: "effective-access",
              label: (
                <span className="text-xs text-zinc-400">
                  有效访问权限
                  <span className="ml-2 font-mono text-zinc-600">
                    {effectiveAccess.actor}
                  </span>
                </span>
              ),
              children: (
                <div className="border-t border-zinc-800/70 pt-3 text-xs">
                  <div className="grid grid-cols-4 gap-4">
                    {[
                      {
                        label: "匿名读取",
                        decision: effectiveAccess.anonymousRead,
                      },
                      {
                        label: "读取",
                        decision: effectiveAccess.permissions.read,
                      },
                      {
                        label: "写入",
                        decision: effectiveAccess.permissions.write,
                      },
                      {
                        label: "管理员",
                        decision: effectiveAccess.permissions.admin,
                      },
                    ].map(({ label, decision }) => (
                      <div key={label}>
                        <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                          {label}
                        </div>
                        <div
                          className={
                            decision.allowed
                              ? "mt-1 text-emerald-300"
                              : "mt-1 text-zinc-500"
                          }
                        >
                          {decision.allowed ? "允许" : "拒绝"}
                        </div>
                        <div
                          className="mt-0.5 truncate text-[10px] text-zinc-600"
                          title={`${accessSourceLabel(decision.source)} · ${accessReasonLabel(decision.reason)}`}
                        >
                          {accessSourceLabel(decision.source)}
                        </div>
                      </div>
                    ))}
                  </div>
                  <div className="mt-3 text-[10px] text-zinc-600">
                    判定顺序：管理员身份 → 全局角色 → 仓库授权 → 旧版静态策略。
                  </div>
                </div>
              ),
            },
          ]}
        />
      )}
      <Tabs
        className="mb-4"
        activeKey={tab}
        onChange={(key) => setTab(key as Tab)}
        items={TABS.filter(
          (t) =>
            (!t.formats || t.formats.includes(repo.format)) &&
            (!t.hostedOnly || repo.type === "hosted") &&
            !(t.key === "publish" && repo.type === "proxy"),
        ).map((t) => ({ key: t.key, label: t.label }))}
      />
      <Card bodyClassName="p-4">
        {tab === "artifacts" && (
          <ArtifactsTab
            repo={repo}
            canWrite={effectiveAccess?.permissions.write.allowed === true}
            artifactTarget={artifactTarget}
            buildTarget={buildTarget}
          />
        )}
        {tab === "publish" &&
          repo.format === "maven" &&
          repo.type !== "proxy" && (
            <MavenPublishWizard
              repositoryId={repo.id}
              onPublished={() => setTab("artifacts")}
            />
          )}
        {tab === "grants" && <GrantsTab repo={repo} />}
        {tab === "retention" && <RetentionTab repo={repo} />}
        {tab === "capacity" && <CapacityTab repo={repo} />}
        {tab === "distribute" && <DistributeTab repo={repo} />}
        {tab === "jobs" && <JobsTab repo={repo} />}
        {tab === "tombstones" && <TombstonesTab repo={repo} />}
      </Card>
    </div>
  );
}
