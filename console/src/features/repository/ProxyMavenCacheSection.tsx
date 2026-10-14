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
  const { t } = usePreferences();
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
    ? t("repository.failedToLoadUpstreamStatus")
    : "";

  return (
    <div className="mb-5 space-y-3 rounded-lg border border-line bg-surface-translucent p-4">
      <div className="grid gap-3 lg:grid-cols-4">
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("repository.upstream")}
          </div>
          <div
            className="mt-1 truncate font-mono text-xs text-fg"
            title={health?.endpoint}
          >
            {health?.endpoint ?? t("repository.checking")}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("repository.health")}
          </div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.reachable ? "text-[var(--ag-status-success)]" : "text-[var(--ag-status-danger)]"}`}
          >
            {health
              ? health.reachable
                ? `${t("repository.reachable")}${health.status ? ` · ${health.status}` : ""}`
                : health.error || t("repository.unreachable")
              : t("repository.checking")}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs text-fg-tertiary">Circuit</div>
          <div
            className={`mt-1 text-xs font-semibold ${health?.circuitOpen ? "text-[var(--ag-status-danger)]" : "text-[var(--ag-status-success)]"}`}
          >
            {health?.circuitOpen ? "open" : "closed"}
          </div>
        </div>
        <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("repository.cache")}
          </div>
          <div className="mt-1 text-xs font-semibold text-fg-strong">
            {health?.cacheEnabled
              ? t("repository.enabled")
              : t("repository.disabled")}
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
          {t("repository.usage")}{" "}
          <span className="text-xs text-fg-disabled group-open:hidden">
            {t("repository.expand")}
          </span>
        </summary>
        <div className="mt-2 space-y-2">
          <div className="text-xs text-fg-tertiary">
            {t("repository.theMavenProxyRequiresBasicAuthenticationUseAny")}
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            <SnippetBlock label="settings.xml" code={settings} />
            <SnippetBlock label="Docker Maven" code={docker} />
            <SnippetBlock
              label={t("repository.directDownload")}
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
