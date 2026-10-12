import { formatDescriptor } from "../../formats";
import {
  Button,
  Checkbox,
  Input,
  InputNumber,
  Radio,
  Select,
  Space,
  Switch,
} from "antd";
import type { Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import type { ProxySettingsDraft } from "./useProxySettingsDraft";
import { UpstreamAuthFields } from "./UpstreamAuthFields";

/** A proxy's upstream: endpoint, allowed hosts, egress proxy and upstream auth. */
export function ProxyUpstreamSettings({
  repo,
  draft,
}: {
  repo: Repository;
  draft: ProxySettingsDraft;
}) {
  const { text } = usePreferences();
  const descriptor = formatDescriptor(repo.format);
  const requiresHosts = descriptor?.upstreamHostAllowList ?? false;
  const {
    endpoint,
    setEndpoint,
    hosts,
    setHosts,
    egressMode,
    setEgressMode,
    egressProtocol,
    setEgressProtocol,
    egressHost,
    setEgressHost,
    egressPort,
    setEgressPort,
    egressUsername,
    setEgressUsername,
    egressPassword,
    setEgressPassword,
    egressClearCredentials,
    setEgressClearCredentials,
    egressRemoteDns,
    setEgressRemoteDns,
    egressNoProxy,
    setEgressNoProxy,
    egressTesting,
    egressTestResult,
    setEgressTestResult,
    runEgressTest,
  } = draft;
  return (
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
                "逗号分隔，Raw / Conan / npm / PyPI / Go / Cargo / APT 代理必填。Cargo 通常需要 index.crates.io 和 static.crates.io。",
                "Comma-separated. Required for Raw, Conan, npm, PyPI, Go, Cargo, and APT proxies. Cargo normally needs index.crates.io and static.crates.io.",
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
      <div className="rounded-lg border border-line bg-surface-translucent px-4 py-3">
        <div className="text-sm font-medium text-fg">
          {text("出口代理", "Egress proxy")}
        </div>
        <div className="mt-1 text-xs leading-5 text-fg-tertiary">
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
            <span className="text-sm text-fg">{text("直连", "Direct")}</span>
            <span className="ml-2 text-xs text-fg-tertiary">
              {text(
                "不经过任何代理，保留私网地址防护",
                "Do not use a proxy; retain private-address protection",
              )}
            </span>
          </Radio>
          <Radio value="environment">
            <span className="text-sm text-fg">
              {text("跟随环境变量", "Use environment variables")}
            </span>
            <span className="ml-2 text-xs text-fg-tertiary">
              {text(
                "沿用进程级 HTTP(S)_PROXY 与 NO_PROXY",
                "Use process-level HTTP(S)_PROXY and NO_PROXY",
              )}
            </span>
          </Radio>
          <Radio value="custom">
            <span className="text-sm text-fg">
              {text("自定义代理", "Custom proxy")}
            </span>
            <span className="ml-2 text-xs text-fg-tertiary">
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
            className="mt-3 w-full border-t border-line pt-3"
          >
            <div className="flex flex-wrap gap-3">
              <Field label={text("协议", "Protocol")}>
                <Select
                  className="w-40"
                  value={egressProtocol}
                  onChange={setEgressProtocol}
                  options={[
                    {
                      value: "http",
                      label: text("HTTP（CONNECT）", "HTTP (CONNECT)"),
                    },
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
                  <div className="text-xs font-medium text-fg-secondary">
                    {text("远程 DNS（socks5h）", "Remote DNS (socks5h)")}
                  </div>
                  <div className="mt-1 text-xs leading-5 text-fg-disabled">
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
                onChange={(e) => setEgressClearCredentials(e.target.checked)}
              >
                <span className="text-xs text-fg-secondary">
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
        <div className="mt-3 flex items-center gap-3 border-t border-line pt-3">
          <Button onClick={runEgressTest} loading={egressTesting}>
            {text("测试连接", "Test connection")}
          </Button>
          <span className="text-xs text-fg-disabled">
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
                {egressTestResult.error ?? text("未知错误", "Unknown error")}
              </span>
            ))}
        </div>
      </div>
      {descriptor?.upstreamAuthentication && (
        <UpstreamAuthFields repo={repo} draft={draft} />
      )}
    </Space>
  );
}
