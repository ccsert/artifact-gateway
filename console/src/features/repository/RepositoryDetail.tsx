import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { Tabs } from "antd";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
} from "../../client";
import type {
  Repository,
  RepositoryCapabilities,
  RepositoryCapacity,
  RepositoryEffectiveAccess,
} from "../../client";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import { PageHeader } from "../../components/ui/Layout";
import { MavenPublishWizard } from "./MavenPublishWizard";
import { usePreferences } from "../../lib/preferences";
import { useAuth } from "../../lib/auth";
import {
  platformCapabilities,
  repositoryPermissions,
} from "../../lib/authorization";
import {
  NpmPublishGuide,
  OCIPublishGuide,
  PyPIPublishGuide,
} from "./RepositoryUsageGuides";
const RepositoryArtifactsTab = lazy(async () => ({
  default: (await import("./RepositoryArtifactsTab")).RepositoryArtifactsTab,
}));
const RepositoryCapacityTab = lazy(async () => ({
  default: (await import("./RepositoryCapacityTab")).RepositoryCapacityTab,
}));
const RepositoryUsageTab = lazy(async () => ({
  default: (await import("./RepositoryUsageTab")).RepositoryUsageTab,
}));
const RepositoryDistributionTab = lazy(async () => ({
  default: (await import("./RepositoryDistributionTab"))
    .RepositoryDistributionTab,
}));
const RepositoryGrantsTab = lazy(async () => ({
  default: (await import("./RepositoryGrantsTab")).RepositoryGrantsTab,
}));
const RepositoryJobsTab = lazy(async () => ({
  default: (await import("./RepositoryLifecycleTabs")).RepositoryJobsTab,
}));
const RepositoryTombstonesTab = lazy(async () => ({
  default: (await import("./RepositoryLifecycleTabs")).RepositoryTombstonesTab,
}));
const RepositoryRetentionTab = lazy(async () => ({
  default: (await import("./RepositoryRetentionTab")).RepositoryRetentionTab,
}));
const RepositoryScanningTab = lazy(async () => ({
  default: (await import("./RepositoryScanningTab")).RepositoryScanningTab,
}));
const RepositorySecurityTab = lazy(async () => ({
  default: (await import("./RepositorySecurityTab")).RepositorySecurityTab,
}));

const APTOperationsTab = lazy(async () => ({
  default: (await import("./APTOperationsTab")).APTOperationsTab,
}));

import {
  TABS,
  repositoryTabAvailable,
  repositoryTabFromQuery,
  type Tab,
} from "./repositoryTabs";
import {
  RepositoryTabSurface,
  RepositorySummary,
  EffectiveAccessPanel,
} from "./RepositoryDetailHeader";
import { RepositorySettingsTab } from "./RepositorySettingsTab";

export function RepositoryDetailPage() {
  const { text } = usePreferences();
  const { identity } = useAuth();
  const isAdmin = platformCapabilities(identity).platformAdmin;
  const { repositoryId = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const artifactTarget = searchParams.get("artifact")?.trim() ?? "";
  const assetTarget = searchParams.get("asset")?.trim() || undefined;
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
  const [accessResolved, setAccessResolved] = useState(false);
  const permissions = repositoryPermissions(effectiveAccess);
  const canWrite = permissions.write;
  // A tab is offered when the repository's own access answer grants the
  // authority its endpoints require, which is the same scope the server
  // enforces. The platform administrator keeps every tab so the console does
  // not depend on the access answer for its own administration.
  const tabAllowed = useCallback(
    (item: (typeof TABS)[number]) =>
      isAdmin || permissions[item.authority] === true,
    [isAdmin, permissions],
  );
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
    setAccessResolved(false);
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
    setAccessResolved(true);
    if (!capacityRes.error) setCapacity(capacityRes.data ?? null);
  }, [repositoryId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setTab(repositoryTabFromQuery(requestedTab));
  }, [requestedTab]);

  useEffect(() => {
    if (!repo || (!isAdmin && !accessResolved)) return;
    const available = TABS.some(
      (item) =>
        item.key === tab &&
        repositoryTabAvailable(item, repo) &&
        tabAllowed(item),
    );
    if (!available) selectTab("artifacts");
  }, [repo, selectTab, tab, tabAllowed, isAdmin, accessResolved]);

  if (error !== null) {
    return (
      <div className="ag-page-stack">
        <PageHeader title={text("仓库详情", "Repository details")} />
        <ErrorBanner error={error} onRetry={load} />
      </div>
    );
  }
  if (!repo) return <Loading />;

  const availableTabs = TABS.filter(
    (item) => repositoryTabAvailable(item, repo) && tabAllowed(item),
  );
  const activeTab = availableTabs.some((item) => item.key === tab)
    ? tab
    : "artifacts";

  return (
    <div className="ag-page-stack">
      <div>
        <div className="mb-1 text-xs text-zinc-500">
          <Link
            to="/repositories"
            className="hover:text-[var(--ag-link-hover)]"
          >
            {text("仓库", "Repositories")}
          </Link>
          <span className="mx-1.5">/</span>
          <span className="text-zinc-400">{repo.name}</span>
        </div>
        <RepositorySummary
          repo={repo}
          capacity={capacity}
          onOpenCapacity={isAdmin ? () => selectTab("capacity") : undefined}
        />
      </div>
      <nav
        className="ag-repository-navigation"
        aria-label={text("仓库任务", "Repository tasks")}
      >
        <Tabs
          className="ag-repository-tabs"
          size="small"
          animated={false}
          tabBarGutter={12}
          activeKey={activeTab}
          onChange={(key) => selectTab(key as Tab)}
          items={availableTabs.map((item) => ({
            key: item.key,
            label: text(item.label, item.labelEn),
          }))}
        />
      </nav>
      <RepositoryTabSurface
        standalone={activeTab === "scanning" || activeTab === "security"}
      >
        <Suspense fallback={<Loading />}>
          {activeTab === "artifacts" && (
            <RepositoryArtifactsTab
              repo={repo}
              canWrite={canWrite}
              canQuarantine={permissions.administer}
              artifactTarget={artifactTarget}
              buildTarget={buildTarget}
              assetTarget={assetTarget}
              onBrowseArtifact={(node) =>
                setSearchParams((current) => {
                  const next = new URLSearchParams(current);
                  next.set("artifact", node.coordinate ?? node.path ?? "");
                  if (node.buildNumber)
                    next.set("build", String(node.buildNumber));
                  else next.delete("build");
                  if (repo.type === "proxy" && node.path)
                    next.set("asset", node.path);
                  else next.delete("asset");
                  next.delete("reference");
                  next.delete("version");
                  return next;
                })
              }
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
          {activeTab === "publish" &&
            repo.format === "maven" &&
            repo.type !== "proxy" && (
              <MavenPublishWizard
                repositoryId={repo.id}
                onPublished={() => selectTab("artifacts")}
              />
            )}
          {activeTab === "publish" &&
            repo.format === "npm" &&
            repo.type !== "proxy" && <NpmPublishGuide repoName={repo.name} />}
          {activeTab === "publish" &&
            repo.format === "oci" &&
            repo.type !== "proxy" && <OCIPublishGuide repoName={repo.name} />}
          {activeTab === "publish" &&
            repo.format === "pypi" &&
            repo.type !== "proxy" && <PyPIPublishGuide repoName={repo.name} />}
          {activeTab === "grants" && (
            <>
              {effectiveAccess && (
                <EffectiveAccessPanel effectiveAccess={effectiveAccess} />
              )}
              <RepositoryGrantsTab repo={repo} />
            </>
          )}
          {activeTab === "apt-snapshots" && (
            <APTOperationsTab
              key={repo.id}
              repo={repo}
              canAdmin={permissions.administer}
            />
          )}
          {activeTab === "retention" && <RepositoryRetentionTab repo={repo} />}
          {activeTab === "scanning" && (
            <RepositoryScanningTab
              repo={repo}
              capabilities={caps}
              capabilitiesLoading={capsLoading}
              capabilitiesError={capsError}
              canManage={permissions.intelligence}
              canViewJobs={permissions.administer}
            />
          )}
          {activeTab === "security" && (
            <RepositorySecurityTab
              repo={repo}
              publicationScanning={caps?.publicationScanning ?? false}
            />
          )}
          {activeTab === "capacity" && <RepositoryCapacityTab repo={repo} />}
          {activeTab === "usage" && <RepositoryUsageTab repo={repo} />}
          {activeTab === "distribute" && (
            <RepositoryDistributionTab repo={repo} />
          )}
          {activeTab === "jobs" && <RepositoryJobsTab repo={repo} />}
          {activeTab === "tombstones" && (
            <RepositoryTombstonesTab repo={repo} />
          )}
          {activeTab === "settings" && (
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
