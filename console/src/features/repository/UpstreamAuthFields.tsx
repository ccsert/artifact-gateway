import { Input, Radio, Space } from "antd";
import type { Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import type { ProxySettingsDraft } from "./useProxySettingsDraft";

/** Credentials sent only to the upstream host (Go module proxies). */
export function UpstreamAuthFields({
  repo,
  draft,
}: {
  repo: Repository;
  draft: ProxySettingsDraft;
}) {
  const { text } = usePreferences();
  const {
    upstreamScheme,
    setUpstreamScheme,
    upstreamUsername,
    setUpstreamUsername,
    upstreamSecret,
    setUpstreamSecret,
  } = draft;
  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-4 py-3">
      <div className="text-sm font-medium text-fg">
        {text("上游认证", "Upstream authentication")}
      </div>
      <div className="mt-1 text-xs leading-5 text-fg-tertiary">
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
          <span className="text-sm text-fg">
            {text("匿名访问", "Anonymous")}
          </span>
          <span className="ml-2 text-xs text-fg-tertiary">
            {repo.upstreamAuth?.credentialsConfigured
              ? text(
                  "不附带凭据，并清除已存储的凭据",
                  "Send no credential and clear the stored one",
                )
              : text("不附带任何凭据", "Send no credential")}
          </span>
        </Radio>
        <Radio value="basic">
          <span className="text-sm text-fg">{text("基本认证", "Basic")}</span>
          <span className="ml-2 text-xs text-fg-tertiary">
            {text(
              "发送 HTTP Basic 用户名与密码",
              "Send an HTTP Basic username and password",
            )}
          </span>
        </Radio>
        <Radio value="bearer">
          <span className="text-sm text-fg">
            {text("Bearer 令牌", "Bearer token")}
          </span>
          <span className="ml-2 text-xs text-fg-tertiary">
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
          className="mt-3 w-full border-t border-line pt-3"
        >
          <div className="flex flex-wrap gap-3">
            {upstreamScheme === "basic" && (
              <Field label={text("上游用户名", "Upstream username")}>
                <Input
                  className="w-64"
                  placeholder="gateway"
                  value={upstreamUsername}
                  onChange={(e) => setUpstreamUsername(e.target.value)}
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
  );
}
