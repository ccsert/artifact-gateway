import { useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Input,
  InputNumber,
  Radio,
  Select,
  Space,
  Switch,
} from "antd";
import { testEgressProxy, updateRepository } from "../../client";
import type {
  EgressProxyTestResult,
  EgressProxyWritable,
  Repository,
  RepositoryCapabilities,
  UpstreamAuthWritable,
} from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

export function RepositorySettingsTab({
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
  const [mavenStrictPublication, setMavenStrictPublication] = useState(
    repo.mavenStrictPublication,
  );
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

  const [upstreamScheme, setUpstreamScheme] = useState<
    "none" | "basic" | "bearer"
  >(repo.upstreamAuth?.scheme ?? "none");
  const [upstreamUsername, setUpstreamUsername] = useState(
    repo.upstreamAuth?.username ?? "",
  );
  const [upstreamSecret, setUpstreamSecret] = useState("");

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
    setMavenStrictPublication(repo.mavenStrictPublication);
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
    setUpstreamScheme(repo.upstreamAuth?.scheme ?? "none");
    setUpstreamUsername(repo.upstreamAuth?.username ?? "");
    setUpstreamSecret("");
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

  const buildUpstreamAuthBody = (): UpstreamAuthWritable => {
    if (upstreamScheme === "none") {
      return { scheme: "none" };
    }
    return {
      scheme: upstreamScheme,
      ...(upstreamScheme === "basic" && upstreamUsername.trim()
        ? { username: upstreamUsername.trim() }
        : {}),
      ...(upstreamSecret ? { secret: upstreamSecret } : {}),
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
        ...(repo.type === "hosted" && repo.format === "maven"
          ? { mavenStrictPublication }
          : {}),
        ...(repo.type === "proxy"
          ? {
              endpoint: endpoint.trim(),
              allowedHosts,
              egressProxy: buildEgressProxyBody(),
              ...(repo.format === "go"
                ? { upstreamAuth: buildUpstreamAuthBody() }
                : {}),
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
          <Switch
            checked={anonymousRead}
            onChange={setAnonymousRead}
            aria-label={text("允许匿名读取", "Allow anonymous reads")}
          />
        </div>
        {repo.type === "hosted" && repo.format === "maven" && (
          <div className="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-zinc-200">
                {text("严格发布", "Strict publication")}
              </div>
              <div className="mt-1 max-w-[72ch] text-xs leading-5 text-zinc-500">
                {text(
                  "默认关闭时与 Nexus 的直接可见行为一致，标准 Maven/Gradle 客户端无需额外集成。开启后，上传内容会在 Gateway 坐标提交成功前保持不可见。请不要在发布任务进行中切换。",
                  "When off, Nexus-style direct visibility lets standard Maven/Gradle clients publish without an extra integration. When enabled, uploads stay hidden until the Gateway coordinate commit succeeds. Do not change this policy during an active publication.",
                )}
              </div>
            </div>
            <Switch
              checked={mavenStrictPublication}
              onChange={setMavenStrictPublication}
              aria-label={text("严格发布", "Strict publication")}
            />
          </div>
        )}
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
                    <span className="text-xs text-[var(--ag-status-success)]">
                      {text("代理可达", "Proxy reachable")}
                      {egressTestResult.upstreamStatus
                        ? ` · ${text(`上游返回 ${egressTestResult.upstreamStatus}`, `upstream returned ${egressTestResult.upstreamStatus}`)}`
                        : ""}
                      {egressTestResult.latencyMs !== undefined
                        ? ` · ${text(`延迟 ${egressTestResult.latencyMs} ms`, `latency ${egressTestResult.latencyMs} ms`)}`
                        : ""}
                    </span>
                  ) : (
                    <span className="text-xs text-[var(--ag-status-danger)]">
                      {text("连接失败：", "Connection failed: ")}
                      {egressTestResult.error ??
                        text("未知错误", "Unknown error")}
                    </span>
                  ))}
              </div>
            </div>
            {repo.format === "go" && (
              <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3">
                <div className="text-sm font-medium text-zinc-200">
                  {text("上游认证", "Upstream authentication")}
                </div>
                <div className="mt-1 text-xs leading-5 text-zinc-500">
                  {text(
                    "当上游 Go 模块代理或代码托管要求认证时，为拉取请求附带凭据。凭据只发送给上游端点主机；跨主机重定向会剥离凭据。",
                    "Attach a credential to upstream fetches when the Go module proxy or code host requires authentication. The credential is sent only to the upstream endpoint host and is stripped on any cross-host redirect.",
                  )}
                </div>
                <Radio.Group
                  className="mt-3 flex flex-col gap-2"
                  value={upstreamScheme}
                  onChange={(e) => setUpstreamScheme(e.target.value)}
                >
                  <Radio value="none">
                    <span className="text-sm text-zinc-200">
                      {text("匿名访问", "Anonymous")}
                    </span>
                    <span className="ml-2 text-xs text-zinc-500">
                      {repo.upstreamAuth?.credentialsConfigured
                        ? text(
                            "不附带凭据，并清除已存储的凭据",
                            "Send no credential and clear the stored one",
                          )
                        : text("不附带任何凭据", "Send no credential")}
                    </span>
                  </Radio>
                  <Radio value="basic">
                    <span className="text-sm text-zinc-200">
                      {text("基本认证", "Basic")}
                    </span>
                    <span className="ml-2 text-xs text-zinc-500">
                      {text(
                        "发送 HTTP Basic 用户名与密码",
                        "Send an HTTP Basic username and password",
                      )}
                    </span>
                  </Radio>
                  <Radio value="bearer">
                    <span className="text-sm text-zinc-200">
                      {text("Bearer 令牌", "Bearer token")}
                    </span>
                    <span className="ml-2 text-xs text-zinc-500">
                      {text(
                        "发送 Authorization: Bearer 令牌",
                        "Send Authorization: Bearer <token>",
                      )}
                    </span>
                  </Radio>
                </Radio.Group>
                {upstreamScheme !== "none" && (
                  <Space
                    orientation="vertical"
                    size="middle"
                    className="mt-3 w-full border-t border-zinc-800/60 pt-3"
                  >
                    <div className="flex flex-wrap gap-3">
                      {upstreamScheme === "basic" && (
                        <Field label={text("上游用户名", "Upstream username")}>
                          <Input
                            className="w-64"
                            placeholder="gateway"
                            value={upstreamUsername}
                            onChange={(e) =>
                              setUpstreamUsername(e.target.value)
                            }
                          />
                        </Field>
                      )}
                      <Field
                        label={
                          upstreamScheme === "basic"
                            ? text("上游密码", "Upstream password")
                            : text("上游令牌", "Upstream token")
                        }
                        hint={text(
                          "AES-256-GCM 加密落库，留空则保留已存凭据。切换认证方式时必须重新填写。",
                          "Stored encrypted with AES-256-GCM. Leave blank to keep the current credential; changing the scheme requires resubmitting it.",
                        )}
                      >
                        <Input.Password
                          className="w-64"
                          placeholder={
                            repo.upstreamAuth?.credentialsConfigured
                              ? text(
                                  "已配置，输入以替换",
                                  "Configured; enter a value to replace it",
                                )
                              : text("未配置", "Not configured")
                          }
                          value={upstreamSecret}
                          onChange={(e) => setUpstreamSecret(e.target.value)}
                        />
                      </Field>
                    </div>
                  </Space>
                )}
              </div>
            )}
          </Space>
        )}
        {capabilities && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-zinc-800/70 pt-4 text-xs text-zinc-500">
            <span className="mr-1">
              {text("支持的操作", "Supported operations")}
            </span>
            {capabilities.operations.map((operation) => (
              <Badge key={operation} tone="neutral">
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
