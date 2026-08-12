import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Input,
  InputNumber,
  Popover,
  Radio,
  Select,
  Space,
  Switch,
  Tabs,
  Tooltip,
} from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
  testEgressProxy,
  updateRepository,
} from "../client";
import type {
  EgressProxyTestResult,
  EgressProxyWritable,
  Repository,
  RepositoryCapabilities,
  RepositoryCapacity,
  RepositoryEffectiveAccess,
} from "../client";
import { AccessDecisionSummary } from "../components/AccessDecisionSummary";
import { Badge, FormatBadge, StateBadge } from "../components/Badge";
import { ErrorBanner, Loading } from "../components/Feedback";
import { IdentitySummary } from "../components/IdentitySummary";
import { Card, Field, PageHeader } from "../components/Layout";
import { MavenPublishWizard } from "../components/MavenPublishWizard";
import { formatBytes, formatNumber } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import {
  CopyButton,
  NpmPublishGuide,
  PyPIPublishGuide,
} from "./repository-detail/RepositoryUsageGuides";

const RepositoryArtifactsTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryArtifactsTab"))
    .RepositoryArtifactsTab,
}));
const RepositoryCapacityTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryCapacityTab"))
    .RepositoryCapacityTab,
}));
const RepositoryDistributionTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryDistributionTab"))
    .RepositoryDistributionTab,
}));
const RepositoryGrantsTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryGrantsTab"))
    .RepositoryGrantsTab,
}));
const RepositoryJobsTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryLifecycleTabs"))
    .RepositoryJobsTab,
}));
const RepositoryTombstonesTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryLifecycleTabs"))
    .RepositoryTombstonesTab,
}));
const RepositoryRetentionTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryRetentionTab"))
    .RepositoryRetentionTab,
}));
const RepositoryScanningTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositoryScanningTab"))
    .RepositoryScanningTab,
}));
const RepositorySecurityTab = lazy(async () => ({
  default: (await import("./repository-detail/RepositorySecurityTab"))
    .RepositorySecurityTab,
}));

type Tab =
  | "artifacts"
  | "publish"
  | "grants"
  | "retention"
  | "scanning"
  | "security"
  | "capacity"
  | "distribute"
  | "jobs"
  | "tombstones"
  | "settings";

function RepositoryTabSurface({
  standalone,
  children,
}: {
  standalone: boolean;
  children: ReactNode;
}) {
  return standalone ? children : <Card bodyClassName="p-4">{children}</Card>;
}

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
    formats: ["maven", "npm", "pypi"],
  },
  { key: "grants", label: "访问授权", labelEn: "Access grants" },
  {
    key: "retention",
    label: "保留策略",
    labelEn: "Retention",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi"],
    hostedOnly: true,
  },
  {
    key: "scanning",
    label: "制品扫描",
    labelEn: "Scanning",
  },
  {
    key: "security",
    label: "安全准入",
    labelEn: "Security admission",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi"],
    hostedOnly: true,
  },
  { key: "capacity", label: "容量", labelEn: "Capacity" },
  {
    key: "distribute",
    label: "晋升 / 复制",
    labelEn: "Promote / replicate",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi"],
  },
  {
    key: "jobs",
    label: "生命周期任务",
    labelEn: "Lifecycle jobs",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi"],
  },
  {
    key: "tombstones",
    label: "墓碑",
    labelEn: "Tombstones",
    formats: ["maven", "oci", "conan", "raw", "npm", "pypi"],
  },
  { key: "settings", label: "设置", labelEn: "Settings" },
];

function repositoryTabFromQuery(value: string | null): Tab {
  return TABS.find((tab) => tab.key === value)?.key ?? "artifacts";
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

  const requiresHosts =
    repo.format === "raw" ||
    repo.format === "conan" ||
    repo.format === "npm" ||
    repo.format === "pypi" ||
    repo.format === "go" ||
    repo.format === "apt";

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
                      "逗号分隔，Raw / Conan / npm / PyPI / Go 代理必填。",
                      "Comma-separated. Required for Raw, Conan, npm, PyPI, and Go proxies.",
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
  const [capsLoading, setCapsLoading] = useState(true);
  const [capsError, setCapsError] = useState<unknown>(null);
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
    setCapsLoading(true);
    setCapsError(null);
    const { data, error: err } = await getRepository({
      path: { repositoryId },
    });
    if (err) {
      setCapsLoading(false);
      setError(err);
      return;
    }
    setRepo(data ?? null);
    const [capsRes, accessRes, capacityRes] = await Promise.all([
      getRepositoryCapabilities({ path: { repositoryId } }),
      getRepositoryEffectiveAccess({ path: { repositoryId } }),
      getRepositoryCapacity({ path: { repositoryId } }),
    ]);
    if (capsRes.error) setCapsError(capsRes.error);
    else setCaps(capsRes.data ?? null);
    setCapsLoading(false);
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
      <RepositoryTabSurface
        standalone={tab === "scanning" || tab === "security"}
      >
        <Suspense fallback={<Loading />}>
          {tab === "artifacts" && (
            <RepositoryArtifactsTab
              repo={repo}
              canWrite={effectiveAccess?.permissions.write.allowed === true}
              canQuarantine={
                effectiveAccess?.permissions.admin.allowed === true
              }
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
          {tab === "publish" &&
            repo.format === "pypi" &&
            repo.type !== "proxy" && <PyPIPublishGuide repoName={repo.name} />}
          {tab === "grants" && (
            <>
              {effectiveAccess && (
                <EffectiveAccessPanel effectiveAccess={effectiveAccess} />
              )}
              <RepositoryGrantsTab repo={repo} />
            </>
          )}
          {tab === "retention" && <RepositoryRetentionTab repo={repo} />}
          {tab === "scanning" && (
            <RepositoryScanningTab
              repo={repo}
              capabilities={caps}
              capabilitiesLoading={capsLoading}
              capabilitiesError={capsError}
              canManage={
                effectiveAccess?.permissions.intelligence.allowed === true
              }
              canViewJobs={effectiveAccess?.permissions.admin.allowed === true}
            />
          )}
          {tab === "security" && (
            <RepositorySecurityTab
              repo={repo}
              publicationScanning={caps?.publicationScanning ?? false}
            />
          )}
          {tab === "capacity" && <RepositoryCapacityTab repo={repo} />}
          {tab === "distribute" && <RepositoryDistributionTab repo={repo} />}
          {tab === "jobs" && <RepositoryJobsTab repo={repo} />}
          {tab === "tombstones" && <RepositoryTombstonesTab repo={repo} />}
          {tab === "settings" && (
            <RepositorySettingsTab
              repo={repo}
              capabilities={caps}
              onUpdated={load}
            />
          )}
        </Suspense>
      </RepositoryTabSurface>
    </div>
  );
}
