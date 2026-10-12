import { formatDescriptor } from "../../formats";
import { useState } from "react";
import { Button, Space, Switch } from "antd";
import { updateRepository } from "../../client";
import type { Repository, RepositoryCapabilities } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { ProxyUpstreamSettings } from "./ProxyUpstreamSettings";
import { useProxySettingsDraft } from "./useProxySettingsDraft";

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
  const [anonymousRead, setAnonymousRead] = useState(repo.anonymousRead);
  const [mavenStrictPublication, setMavenStrictPublication] = useState(
    repo.mavenStrictPublication,
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const proxyDraft = useProxySettingsDraft(repo, setError);

  const descriptor = formatDescriptor(repo.format);

  const resetForm = () => {
    proxyDraft.reset();
    setAnonymousRead(repo.anonymousRead);
    setMavenStrictPublication(repo.mavenStrictPublication);
    setError(null);
    setNotice("");
  };

  const submit = async () => {
    setSaving(true);
    setError(null);
    setNotice("");
    try {
      const { error: err } = await updateRepository({
        path: { repositoryId: repo.id },
        headers: { "If-Match": repo.version },
        body: {
          anonymousRead,
          ...(repo.type === "hosted" && descriptor?.strictPublicationSetting
            ? { mavenStrictPublication }
            : {}),
          ...(repo.type === "proxy"
            ? proxyDraft.body(Boolean(descriptor?.upstreamAuthentication))
            : {}),
        },
      });
      if (err) {
        setError(err);
        return;
      }
      setNotice(text("仓库设置已保存", "Repository settings saved"));
      onUpdated();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-5 flex items-start justify-between gap-6 border-b border-line pb-4">
        <div>
          <h2 className="text-sm font-semibold text-fg-strong">
            {text("仓库设置", "Repository settings")}
          </h2>
          <p className="mt-1 text-xs leading-5 text-fg-tertiary">
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
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      <Space orientation="vertical" size="large" className="w-full">
        <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-surface-translucent px-4 py-3">
          <div>
            <div className="text-sm font-medium text-fg">
              {text("允许匿名读取", "Allow anonymous reads")}
            </div>
            <div className="mt-1 text-xs leading-5 text-fg-tertiary">
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
        {repo.type === "hosted" && descriptor?.strictPublicationSetting && (
          <div className="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-line bg-surface-translucent px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-fg">
                {text("严格发布", "Strict publication")}
              </div>
              <div className="mt-1 max-w-[72ch] text-xs leading-5 text-fg-tertiary">
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
          <ProxyUpstreamSettings repo={repo} draft={proxyDraft} />
        )}
        {capabilities && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-line pt-4 text-xs text-fg-tertiary">
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
