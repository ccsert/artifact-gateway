import { usePreferences } from "../../lib/preferences";
import { RepositorySnippetBlock as SnippetBlock } from "./RepositoryUsageGuides";
import { ProxyMavenInvalidateCard } from "./ProxyMavenInvalidateCard";
import { ProxyMavenWarmCard } from "./ProxyMavenWarmCard";
import { useProxyHealth } from "./repositoryQueries";

export const PROXY_MAVEN_PAGE_SIZE = 50;

export type ProxyMavenAssetFilter = "primary" | "all" | "jar" | "pom";

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

  const healthQuery = useProxyHealth(repoId);
  const health = healthQuery.data ?? null;
  const healthError = healthQuery.error
    ? text("读取上游状态失败", "Failed to load upstream status")
    : "";

  return (
    <div className="mb-5 space-y-3 rounded-lg border border-line bg-surface-translucent p-4">
      <div className="grid gap-3 lg:grid-cols-4">
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
            {text("上游", "Upstream")}
          </div>
          <div
            className="mt-1 truncate font-mono text-xs text-fg"
            title={health?.endpoint}
          >
            {health?.endpoint ?? text("检查中…", "Checking…")}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
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
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
            Circuit
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.circuitOpen ? "text-[var(--ag-status-danger)]" : "text-[var(--ag-status-success)]"}`}
          >
            {health?.circuitOpen ? "open" : "closed"}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs uppercase tracking-wider text-fg-tertiary">
            {text("缓存", "Cache")}
          </div>
          <div className="mt-1 text-xs font-semibold text-fg-strong">
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
          <span className="text-xs text-fg-disabled group-open:hidden">
            {text("（展开）", "(expand)")}
          </span>
        </summary>
        <div className="mt-2 space-y-2">
          <div className="text-xs text-fg-tertiary">
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
      <ProxyMavenWarmCard
        repoId={repoId}
        repoName={repoName}
        token={token}
        onWarmed={onWarmed}
      />
      <ProxyMavenInvalidateCard repoId={repoId} onWarmed={onWarmed} />
    </div>
  );
}

export { ProxyMavenCacheDetail } from "./ProxyMavenCacheDetail";
