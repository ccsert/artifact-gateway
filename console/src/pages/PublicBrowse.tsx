import { Fragment, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { searchRepositoryArtifacts } from "../client";
import type { ArtifactSummary } from "../client";
import { Card, DataTable, inputClass, btnPrimary } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usageFor, type UsageSnippet } from "../lib/usage";

interface PublicRepository {
  id: string;
  name: string;
  format: "oci" | "maven" | "conan" | "raw";
  type?: string;
}

interface PublicRepositoryCatalogResponse {
  enabled?: boolean;
  items?: PublicRepository[];
}

interface ConanRevision {
  revision: string;
  digest?: string;
  createdAt?: string;
}

type ProtocolVersion = {
  value: string;
  label: string;
  searchText: string;
  createdAt?: string;
  digest?: string;
};

interface MavenArtifactGroup {
  key: string;
  versions: ArtifactSummary[];
}

function mavenArtifactGroups(items: ArtifactSummary[]): MavenArtifactGroup[] {
  const groups = new Map<string, ArtifactSummary[]>();
  for (const item of items) {
    const parts = item.coordinate.split(":");
    const key = parts.length >= 2 ? `${parts[0]}:${parts[1]}` : item.coordinate;
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }
  return [...groups.entries()]
    .map(([key, versions]) => ({
      key,
      versions: versions.sort((a, b) =>
        b.coordinate.localeCompare(a.coordinate),
      ),
    }))
    .sort((a, b) => a.key.localeCompare(b.key));
}

function mavenVersionKey(version: ArtifactSummary, index: number): string {
  return `${version.coordinate}-${version.buildNumber ?? index}`;
}

function repositoryUsage(
  format: PublicRepository["format"],
  repoName: string,
): UsageSnippet[] {
  const origin = window.location.origin;
  const host = window.location.host;
  if (format === "maven") {
    const url = `${origin}/maven/${repoName}`;
    return [
      { label: "Maven 仓库 URL", code: url },
      {
        label: "settings.xml",
        code: `<repository>\n  <id>${repoName}</id>\n  <url>${url}</url>\n</repository>`,
      },
      { label: "Gradle repositories", code: `maven { url = uri("${url}") }` },
    ];
  }
  if (format === "oci") {
    return [
      { label: "OCI Registry 地址", code: `${host}/${repoName}` },
      {
        label: "Docker Registry 配置",
        code: `docker login ${host}\n# 镜像前缀：${host}/${repoName}/`,
      },
    ];
  }
  if (format === "conan") {
    return [
      {
        label: "Conan remote 地址",
        code: `conan remote add ${repoName} ${origin}/conan/v2/${repoName}`,
      },
    ];
  }
  return [{ label: "Raw 仓库地址", code: `${origin}/raw/${repoName}/` }];
}

export function PublicBrowsePage() {
  const [params, setParams] = useSearchParams();
  const repositoryId = params.get("repository") ?? "";
  const query = params.get("q") ?? "";
  const artifactParam = params.get("artifact") ?? "";
  const buildParam = params.get("build") ?? "";
  const tagParam = params.get("tag") ?? "";
  const revisionParam = params.get("revision") ?? "";
  const [queryDraft, setQueryDraft] = useState(query);
  const [items, setItems] = useState<ArtifactSummary[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [repositories, setRepositories] = useState<PublicRepository[] | null>(
    null,
  );
  const [anonymousEnabled, setAnonymousEnabled] = useState<boolean | null>(
    null,
  );
  const [catalogError, setCatalogError] = useState<unknown>(null);
  const [copiedCoordinate, setCopiedCoordinate] = useState<string | null>(null);
  const [expandedCoordinate, setExpandedCoordinate] = useState<string | null>(
    null,
  );
  const [expandedMavenGroup, setExpandedMavenGroup] = useState<string | null>(
    null,
  );
  const [selectedMavenVersion, setSelectedMavenVersion] = useState<
    string | null
  >(null);
  const [mavenVersionFilter, setMavenVersionFilter] = useState("");
  const [protocolVersionFilter, setProtocolVersionFilter] = useState("");
  const [selectedProtocolVersions, setSelectedProtocolVersions] = useState<
    Record<string, string>
  >({});
  const [ociTags, setOciTags] = useState<Record<string, string[]>>({});
  const [conanRevisions, setConanRevisions] = useState<
    Record<string, ConanRevision[]>
  >({});
  const selectedRepository = repositories?.find(
    (repository) => repository.id === repositoryId,
  );

  useEffect(() => {
    void fetch("/api/v2/public/repositories")
      .then(async (response) => {
        if (!response.ok)
          throw new Error(`读取公开仓库失败 (${response.status})`);
        return response.json() as Promise<PublicRepositoryCatalogResponse>;
      })
      .then((data) => {
        setAnonymousEnabled(data.enabled !== false);
        setRepositories(data.items ?? []);
      })
      .catch(setCatalogError);
  }, []);

  useEffect(() => {
    if (!repositoryId) {
      setItems(null);
      setError(null);
      return;
    }
    let cancelled = false;
    setItems(null);
    setError(null);
    void searchRepositoryArtifacts({
      path: { repositoryId },
      query: { q: query || undefined, pageSize: 100 },
    }).then(({ data, error: requestError }) => {
      if (cancelled) return;
      if (requestError) setError(requestError);
      else setItems(data?.items ?? []);
    });
    return () => {
      cancelled = true;
    };
  }, [repositoryId, query]);

  useEffect(() => {
    if (selectedRepository?.format !== "oci" || !items) {
      setOciTags({});
      return;
    }
    let cancelled = false;
    void Promise.all(
      items.map(async (item) => {
        const imagePath = item.coordinate
          .split("/")
          .map(encodeURIComponent)
          .join("/");
        const response = await fetch(
          `/v2/${encodeURIComponent(selectedRepository.name)}/${imagePath}/tags/list?n=200`,
        );
        if (!response.ok) return [item.coordinate, []] as const;
        const data = (await response.json()) as { tags?: string[] };
        return [item.coordinate, data.tags ?? []] as const;
      }),
    ).then((entries) => {
      if (!cancelled) setOciTags(Object.fromEntries(entries));
    });
    return () => {
      cancelled = true;
    };
  }, [items, selectedRepository]);

  useEffect(() => {
    if (selectedRepository?.format !== "conan" || !items) {
      setConanRevisions({});
      return;
    }
    let cancelled = false;
    void Promise.all(
      items.map(async (item) => {
        const response = await fetch(
          `/api/v2/repositories/${encodeURIComponent(selectedRepository.id)}/conan/recipe-revisions?reference=${encodeURIComponent(item.coordinate)}`,
        );
        if (!response.ok) return [item.coordinate, []] as const;
        const data = (await response.json()) as { items?: ConanRevision[] };
        return [item.coordinate, data.items ?? []] as const;
      }),
    ).then((entries) => {
      if (!cancelled) setConanRevisions(Object.fromEntries(entries));
    });
    return () => {
      cancelled = true;
    };
  }, [items, selectedRepository]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!repositoryId) return;
    setParams({
      repository: repositoryId,
      ...(queryDraft.trim() ? { q: queryDraft.trim() } : {}),
    });
  };

  const globalUsage = selectedRepository
    ? repositoryUsage(selectedRepository.format, selectedRepository.name)
    : [];
  const groupedItems =
    selectedRepository?.format === "maven"
      ? mavenArtifactGroups(items ?? [])
      : null;

  const copyCoordinate = async (coordinate: string) => {
    try {
      await navigator.clipboard.writeText(coordinate);
      setCopiedCoordinate(coordinate);
      window.setTimeout(
        () =>
          setCopiedCoordinate((current) =>
            current === coordinate ? null : current,
          ),
        1400,
      );
    } catch {
      setCopiedCoordinate(null);
    }
  };

  const copyUsage = async (snippet: UsageSnippet) => {
    try {
      await navigator.clipboard.writeText(snippet.code);
      setCopiedCoordinate(snippet.code);
      window.setTimeout(
        () =>
          setCopiedCoordinate((current) =>
            current === snippet.code ? null : current,
          ),
        1400,
      );
    } catch {
      setCopiedCoordinate(null);
    }
  };

  const artifactHref = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
  ) => {
    const next = new URLSearchParams(params);
    next.set("artifact", coordinate);
    if (buildNumber && buildNumber > 0) next.set("build", String(buildNumber));
    else next.delete("build");
    if (tag) next.set("tag", tag);
    else next.delete("tag");
    if (revision) next.set("revision", revision);
    else next.delete("revision");
    return `/browse?${next.toString()}`;
  };

  const openArtifact = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
  ) => {
    const next = new URLSearchParams(params);
    next.set("artifact", coordinate);
    if (buildNumber && buildNumber > 0) next.set("build", String(buildNumber));
    else next.delete("build");
    if (tag) next.set("tag", tag);
    else next.delete("tag");
    if (revision) next.set("revision", revision);
    else next.delete("revision");
    setParams(next);
  };

  const copyPageLink = async (href: string) => {
    try {
      await navigator.clipboard.writeText(
        new URL(href, window.location.origin).toString(),
      );
      setCopiedCoordinate(href);
      window.setTimeout(
        () =>
          setCopiedCoordinate((current) => (current === href ? null : current)),
        1400,
      );
    } catch {
      setCopiedCoordinate(null);
    }
  };

  const usageLabel =
    selectedRepository?.format === "oci"
      ? "注册 OCI 镜像源"
      : selectedRepository?.format === "maven"
        ? "注册 Maven 仓库源"
        : selectedRepository?.format === "conan"
          ? "注册 Conan remote"
          : "注册 Raw 源地址";

  return (
    <main className="min-h-screen bg-[#090a0c] px-4 py-8 text-zinc-200 sm:px-6 sm:py-12">
      <div className="mx-auto max-w-7xl">
        <div className="mb-8 flex items-center justify-between gap-4">
          <div>
            <div className="text-lg font-semibold text-zinc-100">
              Artifact Gateway
            </div>
            <div className="mt-1 text-sm text-zinc-500">公开制品浏览</div>
          </div>
          <Link
            to="/login"
            className="text-sm text-zinc-400 hover:text-cyan-300"
          >
            管理登录
          </Link>
        </div>
        <Card className="p-5 sm:p-6">
          <h1 className="text-xl font-semibold text-zinc-50">公开制品</h1>
          <p className="mt-1 text-sm text-zinc-500">
            仅显示已启用匿名读取的仓库内容；写入与管理操作仍需登录。
          </p>
          {selectedRepository && (
            <div className="mt-4 flex items-center gap-2 text-sm text-zinc-300">
              <span>正在浏览</span>
              <span className="font-medium text-zinc-100">
                {selectedRepository.name}
              </span>
              <FormatBadge format={selectedRepository.format} />
              <button
                onClick={() => setParams({})}
                className="ml-auto text-xs text-zinc-500 hover:text-cyan-300"
              >
                返回公开仓库
              </button>
            </div>
          )}
          {repositoryId && (
            <form onSubmit={submit} className="mt-5 flex gap-2">
              <input
                className={`${inputClass} font-mono text-xs`}
                placeholder="坐标、路径或名称前缀（可选）"
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
              />
              <button className={btnPrimary}>搜索</button>
            </form>
          )}
        </Card>
        <div
          className={
            selectedRepository
              ? "mt-6 grid gap-5 lg:grid-cols-[minmax(0,1fr)_360px] lg:items-start"
              : "mt-6"
          }
        >
          <section>
            {catalogError ? (
              <ErrorBanner error={catalogError} />
            ) : !repositories ? (
              <Loading label="正在读取公开仓库…" />
            ) : anonymousEnabled === false ? (
              <EmptyState
                title="全局匿名读取未启用"
                hint="请管理员在访问控制中启用全局匿名读取，仓库的匿名读取设置才会生效。"
                action={
                  <Link
                    to="/access"
                    className="text-sm text-cyan-300 hover:text-cyan-200"
                  >
                    前往访问控制
                  </Link>
                }
              />
            ) : repositoryId && !selectedRepository ? (
              <EmptyState
                title="公开仓库不存在或不可见"
                hint="返回公开仓库目录，选择一个已启用匿名读取的仓库。"
                action={
                  <button className={btnPrimary} onClick={() => setParams({})}>
                    返回目录
                  </button>
                }
              />
            ) : !repositoryId ? (
              repositories.length === 0 ? (
                <EmptyState
                  title="暂无公开仓库"
                  hint="管理员需先启用全局匿名访问，并在仓库上允许匿名读取。"
                />
              ) : (
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {repositories.map((repository) => (
                    <button
                      key={repository.id}
                      onClick={() => setParams({ repository: repository.id })}
                      className="text-left"
                    >
                      <Card className="h-full border-zinc-800 px-4 py-4 transition-colors hover:border-cyan-500/50 hover:bg-zinc-900">
                        <div className="flex items-center justify-between gap-3">
                          <span className="min-w-0 truncate font-medium text-zinc-100">
                            {repository.name}
                          </span>
                          <FormatBadge format={repository.format} />
                        </div>
                        <div className="mt-3">
                          <Badge
                            tone={
                              repository.type === "proxy" ? "amber" : "cyan"
                            }
                          >
                            {repository.type ?? "hosted"}
                          </Badge>
                        </div>
                        <div className="mt-4 text-xs text-cyan-300">
                          浏览制品
                        </div>
                      </Card>
                    </button>
                  ))}
                </div>
              )
            ) : error ? (
              <ErrorBanner error={error} />
            ) : !items ? (
              <Loading label="正在读取公开制品…" />
            ) : items.length === 0 ? (
              <EmptyState
                title="没有匹配的公开制品"
                hint="确认仓库已启用匿名读取，或调整查询条件。"
              />
            ) : (
              <Card>
                <div className="flex items-center justify-between border-b border-zinc-800/80 px-4 py-3">
                  <div className="text-xs text-zinc-500">
                    找到{" "}
                    <span className="font-medium text-zinc-300">
                      {groupedItems?.length ?? items.length}
                    </span>{" "}
                    个制品
                    {groupedItems && (
                      <span className="ml-1 text-zinc-600">
                        （{items.length} 个版本）
                      </span>
                    )}
                  </div>
                  <div className="text-[11px] text-zinc-600">匿名只读</div>
                </div>
                {groupedItems ? (
                  <DataTable
                    className="min-w-[720px]"
                    columns={["制品", "最新版本", "版本数", "", ""]}
                  >
                    {groupedItems.map((group) => {
                      const latest = group.versions[0];
                      const urlVersion = group.versions.find(
                        (version) =>
                          version.coordinate === artifactParam &&
                          (!buildParam ||
                            String(version.buildNumber ?? 0) === buildParam),
                      );
                      const expanded =
                        expandedMavenGroup === group.key || Boolean(urlVersion);
                      const normalizedFilter = mavenVersionFilter
                        .trim()
                        .toLowerCase();
                      const visibleVersions = normalizedFilter
                        ? group.versions.filter((version) =>
                            `${version.coordinate} ${version.buildNumber ?? ""} ${version.digest ?? ""}`
                              .toLowerCase()
                              .includes(normalizedFilter),
                          )
                        : group.versions;
                      const preferredKey = urlVersion
                        ? mavenVersionKey(
                            urlVersion,
                            group.versions.indexOf(urlVersion),
                          )
                        : selectedMavenVersion &&
                            group.versions.some(
                              (version, index) =>
                                mavenVersionKey(version, index) ===
                                selectedMavenVersion,
                            )
                          ? selectedMavenVersion
                          : mavenVersionKey(latest, 0);
                      const selectedKey = visibleVersions.some(
                        (version) =>
                          mavenVersionKey(
                            version,
                            group.versions.indexOf(version),
                          ) === preferredKey,
                      )
                        ? preferredKey
                        : mavenVersionKey(
                            visibleVersions[0] ?? latest,
                            group.versions.indexOf(
                              visibleVersions[0] ?? latest,
                            ),
                          );
                      const selectedVersion =
                        group.versions.find(
                          (version, index) =>
                            mavenVersionKey(version, index) === selectedKey,
                        ) ?? latest;
                      const snippets = selectedRepository
                        ? usageFor(
                            selectedRepository.format,
                            selectedRepository.name,
                            selectedVersion.coordinate,
                          )
                        : [];
                      return (
                        <Fragment key={group.key}>
                          <tr className="hover:bg-zinc-800/30">
                            <td className="px-4 py-3 font-mono text-xs text-zinc-100">
                              {group.key}
                            </td>
                            <td className="px-4 py-3 font-mono text-xs text-zinc-400">
                              {latest.coordinate.split(":").slice(2).join(":")}
                            </td>
                            <td className="px-4 py-3 text-xs text-zinc-500">
                              {group.versions.length}
                            </td>
                            <td />
                            <td className="px-3 py-2 text-right">
                              <button
                                type="button"
                                onClick={() => {
                                  setExpandedMavenGroup(
                                    expanded ? null : group.key,
                                  );
                                  if (!expanded) {
                                    setSelectedMavenVersion(
                                      mavenVersionKey(latest, 0),
                                    );
                                    setMavenVersionFilter("");
                                  }
                                }}
                                className="rounded px-2 py-1 text-[11px] text-zinc-500 hover:bg-zinc-800 hover:text-cyan-300"
                              >
                                {expanded ? "收起" : "选择版本"}
                              </button>
                            </td>
                          </tr>
                          {expanded && (
                            <tr>
                              <td
                                colSpan={5}
                                className="bg-zinc-950/50 px-4 py-4"
                              >
                                <div className="grid gap-5 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
                                  <div>
                                    <label className="mb-1.5 block text-[11px] font-medium text-zinc-500">
                                      筛选版本
                                    </label>
                                    <input
                                      className={`${inputClass} w-full font-mono text-xs`}
                                      placeholder="版本号、构建号或 digest"
                                      value={mavenVersionFilter}
                                      onChange={(event) =>
                                        setMavenVersionFilter(
                                          event.target.value,
                                        )
                                      }
                                    />
                                    <label className="mb-1.5 mt-3 block text-[11px] font-medium text-zinc-500">
                                      选择版本{" "}
                                      <span className="font-normal text-zinc-600">
                                        ({visibleVersions.length}/
                                        {group.versions.length})
                                      </span>
                                    </label>
                                    <select
                                      className={`${inputClass} h-36 w-full font-mono text-xs`}
                                      size={Math.min(
                                        Math.max(visibleVersions.length, 2),
                                        6,
                                      )}
                                      value={selectedKey}
                                      onChange={(event) => {
                                        setSelectedMavenVersion(
                                          event.target.value,
                                        );
                                        const version = group.versions.find(
                                          (item, index) =>
                                            mavenVersionKey(item, index) ===
                                            event.target.value,
                                        );
                                        if (version)
                                          openArtifact(
                                            version.coordinate,
                                            version.buildNumber,
                                          );
                                      }}
                                    >
                                      {visibleVersions.length === 0 ? (
                                        <option disabled>没有匹配版本</option>
                                      ) : (
                                        visibleVersions.map((version) => {
                                          const index =
                                            group.versions.indexOf(version);
                                          return (
                                            <option
                                              key={mavenVersionKey(
                                                version,
                                                index,
                                              )}
                                              value={mavenVersionKey(
                                                version,
                                                index,
                                              )}
                                            >
                                              {version.coordinate
                                                .split(":")
                                                .slice(2)
                                                .join(":")}
                                              {version.buildNumber
                                                ? ` · SNAPSHOT #${version.buildNumber}`
                                                : ""}
                                            </option>
                                          );
                                        })
                                      )}
                                    </select>
                                    <p className="mt-2 text-[11px] leading-5 text-zinc-600">
                                      版本不会全部铺开；输入版本号、SNAPSHOT
                                      构建号或 digest 即可定位。
                                    </p>
                                  </div>
                                  <div className="min-w-0">
                                    <div className="flex flex-wrap items-center gap-2">
                                      <span className="font-mono text-xs text-zinc-100">
                                        {selectedVersion.coordinate}
                                      </span>
                                      {selectedVersion.buildNumber ? (
                                        <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-300">
                                          SNAPSHOT #
                                          {selectedVersion.buildNumber}
                                        </span>
                                      ) : null}
                                      <a
                                        href={artifactHref(
                                          selectedVersion.coordinate,
                                          selectedVersion.buildNumber,
                                        )}
                                        className="text-[11px] text-cyan-300 hover:text-cyan-200"
                                      >
                                        打开版本页
                                      </a>
                                      <button
                                        type="button"
                                        onClick={() =>
                                          void copyPageLink(
                                            artifactHref(
                                              selectedVersion.coordinate,
                                              selectedVersion.buildNumber,
                                            ),
                                          )
                                        }
                                        className="text-[11px] text-zinc-500 hover:text-cyan-300"
                                      >
                                        {copiedCoordinate ===
                                        artifactHref(
                                          selectedVersion.coordinate,
                                          selectedVersion.buildNumber,
                                        )
                                          ? "链接已复制"
                                          : "复制链接"}
                                      </button>
                                    </div>
                                    <div className="mt-1 flex flex-wrap items-center gap-x-3 text-[11px] text-zinc-600">
                                      <span>
                                        {formatDate(selectedVersion.createdAt)}
                                      </span>
                                      <span
                                        className="max-w-[min(70vw,560px)] truncate font-mono text-zinc-500"
                                        title={selectedVersion.digest}
                                      >
                                        {selectedVersion.digest ?? "—"}
                                      </span>
                                      <span>
                                        {formatBytes(selectedVersion.size)}
                                      </span>
                                    </div>
                                    <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
                                      <MetadataItem
                                        label="发布时间"
                                        value={formatDate(
                                          selectedVersion.createdAt,
                                        )}
                                      />
                                      <MetadataItem
                                        label="发布者"
                                        value={
                                          selectedVersion.publisher ?? "未记录"
                                        }
                                        mono
                                      />
                                      <MetadataItem
                                        label="校验摘要"
                                        value={
                                          selectedVersion.digest ?? "未记录"
                                        }
                                        mono
                                      />
                                      <MetadataItem
                                        label="构建类型"
                                        value={
                                          selectedVersion.buildNumber
                                            ? `SNAPSHOT #${selectedVersion.buildNumber}`
                                            : "Release"
                                        }
                                      />
                                    </div>
                                    <div className="mt-3 grid gap-3 sm:grid-cols-2">
                                      {snippets.map((snippet) => (
                                        <UsageSnippetBlock
                                          key={snippet.label}
                                          snippet={snippet}
                                          copied={
                                            copiedCoordinate === snippet.code
                                          }
                                          onCopy={() => void copyUsage(snippet)}
                                        />
                                      ))}
                                    </div>
                                  </div>
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </DataTable>
                ) : (
                  <DataTable
                    className={
                      selectedRepository?.format === "oci" ||
                      selectedRepository?.format === "conan"
                        ? ""
                        : "min-w-[720px]"
                    }
                    columnClassNames={
                      selectedRepository?.format === "oci" ||
                      selectedRepository?.format === "conan"
                        ? [
                            "",
                            "whitespace-nowrap",
                            "hidden sm:table-cell",
                            "hidden sm:table-cell",
                            "whitespace-nowrap",
                          ]
                        : undefined
                    }
                    columns={
                      selectedRepository?.format === "oci"
                        ? ["镜像", "标签数", "最近标签", "镜像摘要", ""]
                        : selectedRepository?.format === "conan"
                          ? [
                              "Conan reference",
                              "Revision 数",
                              "最近 revision",
                              "最近摘要",
                              "",
                            ]
                          : ["制品坐标", "摘要", "大小", "创建时间", ""]
                    }
                  >
                    {items.map((item, index) => {
                      const isOci = selectedRepository?.format === "oci";
                      const isConan = selectedRepository?.format === "conan";
                      const isVersioned = isOci || isConan;
                      const expanded =
                        expandedCoordinate === item.coordinate ||
                        artifactParam === item.coordinate;
                      const tags =
                        isOci
                          ? (ociTags[item.coordinate] ?? [])
                          : [];
                      const revisions = isConan
                        ? (conanRevisions[item.coordinate] ?? [])
                        : [];
                      const protocolVersionsLoaded = isOci
                        ? Object.prototype.hasOwnProperty.call(
                            ociTags,
                            item.coordinate,
                          )
                        : isConan
                          ? Object.prototype.hasOwnProperty.call(
                              conanRevisions,
                              item.coordinate,
                            )
                          : true;
                      const protocolVersions: ProtocolVersion[] = isOci
                        ? tags.map((tag) => ({
                            value: tag,
                            label: tag,
                            searchText: tag,
                          }))
                        : revisions.map((revision) => ({
                            value: revision.revision,
                            label: revision.revision,
                            searchText: `${revision.revision} ${revision.digest ?? ""} ${revision.createdAt ?? ""}`,
                            createdAt: revision.createdAt,
                            digest: revision.digest,
                          }));
                      const requestedProtocolVersion = isOci
                        ? tagParam
                        : isConan
                          ? revisionParam
                          : "";
                      const normalizedProtocolFilter =
                        protocolVersionFilter.trim().toLowerCase();
                      const visibleProtocolVersions = normalizedProtocolFilter
                        ? protocolVersions.filter((version) =>
                            version.searchText
                              .toLowerCase()
                              .includes(normalizedProtocolFilter),
                          )
                        : protocolVersions;
                      const preferredProtocolVersion =
                        (requestedProtocolVersion &&
                          protocolVersions.some(
                            (version) =>
                              version.value === requestedProtocolVersion,
                          ) &&
                          requestedProtocolVersion) ||
                        (selectedProtocolVersions[item.coordinate] &&
                          protocolVersions.some(
                            (version) =>
                              version.value ===
                              selectedProtocolVersions[item.coordinate],
                          ) &&
                          selectedProtocolVersions[item.coordinate]) ||
                        protocolVersions[0]?.value ||
                        "";
                      const selectedProtocolVersionValue =
                        visibleProtocolVersions.some(
                          (version) =>
                            version.value === preferredProtocolVersion,
                        )
                          ? preferredProtocolVersion
                          : visibleProtocolVersions[0]?.value ||
                            preferredProtocolVersion;
                      const selectedProtocolVersionItem =
                        protocolVersions.find(
                          (version) =>
                            version.value === selectedProtocolVersionValue,
                        );
                      const selectedTag = isOci
                        ? selectedProtocolVersionItem?.value
                        : undefined;
                      const selectedRevision = isConan
                        ? selectedProtocolVersionItem?.value
                        : undefined;
                      const selectedProtocolHref = artifactHref(
                        item.coordinate,
                        undefined,
                        selectedTag,
                        selectedRevision,
                      );
                      const snippets = selectedRepository
                        ? usageFor(
                            selectedRepository.format,
                            selectedRepository.name,
                            item.coordinate,
                            selectedTag,
                          )
                        : [];
                      return (
                        <Fragment key={`${item.coordinate}-${index}`}>
                          <tr
                            key={`${item.coordinate}-${index}`}
                            className="hover:bg-zinc-800/30"
                          >
                            <td
                              className="max-w-md px-4 py-3 font-mono text-xs text-zinc-100"
                              title={item.coordinate}
                            >
                              <div className="truncate">{item.coordinate}</div>
                            </td>
                            {isVersioned ? (
                              <>
                                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">
                                  {!protocolVersionsLoaded
                                    ? "读取中…"
                                    : protocolVersions.length > 0
                                    ? `${protocolVersions.length} 个`
                                    : "—"}
                                </td>
                                <td
                                  className="hidden max-w-[220px] px-4 py-3 font-mono text-xs text-zinc-500 sm:table-cell"
                                  title={
                                    isOci
                                      ? protocolVersions[0]?.label
                                      : protocolVersions[0]?.value
                                  }
                                >
                                  <div className="truncate">
                                    {!protocolVersionsLoaded
                                      ? "读取中…"
                                      : protocolVersions.length > 0
                                      ? isOci
                                        ? protocolVersions[0]?.label
                                        : protocolVersions[0]?.value
                                      : "—"}
                                  </div>
                                </td>
                                <td className="hidden px-4 py-3 font-mono text-xs text-zinc-500 sm:table-cell">
                                  {shortDigest(
                                    protocolVersions[0]?.digest ?? item.digest,
                                  )}
                                </td>
                              </>
                            ) : (
                              <>
                                <td className="px-4 py-3 font-mono text-xs text-zinc-500">
                                  {shortDigest(item.digest)}
                                </td>
                                <td className="px-4 py-3 text-xs text-zinc-400">
                                  {formatBytes(item.size)}
                                </td>
                              </>
                            )}
                            {!isVersioned && (
                              <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">
                                {formatDate(item.createdAt)}
                              </td>
                            )}
                            <td className="whitespace-nowrap px-3 py-2 text-right">
                              <button
                                type="button"
                                title={
                                  isVersioned ? "选择版本" : "查看使用方式"
                                }
                                onClick={() =>
                                  (() => {
                                    setExpandedCoordinate(
                                      expanded ? null : item.coordinate,
                                    );
                                    if (!expanded) {
                                      setProtocolVersionFilter("");
                                      if (selectedProtocolVersionValue) {
                                        setSelectedProtocolVersions(
                                          (current) => ({
                                            ...current,
                                            [item.coordinate]:
                                              selectedProtocolVersionValue,
                                          }),
                                        );
                                      }
                                    }
                                  })()
                                }
                                className="mr-1 rounded px-2 py-1 text-[11px] text-zinc-500 hover:bg-zinc-800 hover:text-cyan-300"
                              >
                                {expanded
                                  ? "收起"
                                  : isVersioned
                                    ? "选择版本"
                                    : "使用方式"}
                              </button>
                              <a
                                href={
                                  isVersioned
                                    ? selectedProtocolHref
                                    : artifactHref(item.coordinate)
                                }
                                className={`mr-1 rounded px-2 py-1 text-[11px] text-cyan-300 hover:bg-zinc-800 ${isVersioned ? "hidden sm:inline" : ""}`}
                              >
                                {isVersioned && selectedProtocolVersionItem
                                  ? "打开版本"
                                  : "打开"}
                              </a>
                              <button
                                type="button"
                                title="复制制品坐标"
                                aria-label={`复制 ${item.coordinate}`}
                                onClick={() =>
                                  void copyCoordinate(item.coordinate)
                                }
                                className="rounded p-1.5 text-zinc-500 hover:bg-zinc-800 hover:text-cyan-300"
                              >
                                {copiedCoordinate === item.coordinate
                                  ? "✓"
                                  : "⧉"}
                              </button>
                            </td>
                          </tr>
                          {expanded && isVersioned && (
                            <tr key={`${item.coordinate}-${index}-versions`}>
                              <td
                                colSpan={5}
                                className="bg-zinc-950/50 px-4 py-4"
                              >
                                <div className="grid gap-5 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
                                  <div>
                                    <div className="flex items-center justify-between gap-3">
                                      <label className="block text-[11px] font-medium text-zinc-500">
                                        筛选{isOci ? "标签" : "recipe revision"}
                                      </label>
                                      <span className="text-[11px] text-zinc-600">
                                        {visibleProtocolVersions.length}/
                                        {protocolVersions.length}
                                      </span>
                                    </div>
                                    <input
                                      className={`${inputClass} mt-1.5 w-full font-mono text-xs`}
                                      placeholder={
                                        isOci
                                          ? "输入 tag 搜索"
                                          : "输入 revision 或 digest 搜索"
                                      }
                                      value={protocolVersionFilter}
                                      onChange={(event) =>
                                        setProtocolVersionFilter(
                                          event.target.value,
                                        )
                                      }
                                    />
                                    <label className="mb-1.5 mt-3 block text-[11px] font-medium text-zinc-500">
                                      选择版本
                                    </label>
                                    <select
                                      className={`${inputClass} h-36 w-full font-mono text-xs`}
                                      size={Math.min(
                                        Math.max(
                                          visibleProtocolVersions.length,
                                          2,
                                        ),
                                        6,
                                      )}
                                      value={selectedProtocolVersionValue}
                                      onChange={(event) => {
                                        const value = event.target.value;
                                        setSelectedProtocolVersions(
                                          (current) => ({
                                            ...current,
                                            [item.coordinate]: value,
                                          }),
                                        );
                                        const version = protocolVersions.find(
                                          (entry) => entry.value === value,
                                        );
                                        if (version) {
                                          openArtifact(
                                            item.coordinate,
                                            undefined,
                                            isOci ? version.value : undefined,
                                            isConan
                                              ? version.value
                                              : undefined,
                                          );
                                        }
                                      }}
                                    >
                                      {visibleProtocolVersions.length ===
                                      0 ? (
                                        <option disabled>
                                          没有匹配版本
                                        </option>
                                      ) : (
                                        visibleProtocolVersions.map((version) => (
                                          <option
                                            key={version.value}
                                            value={version.value}
                                          >
                                            {version.label}
                                            {isConan && version.digest
                                              ? ` · ${shortDigest(version.digest)}`
                                              : ""}
                                          </option>
                                        ))
                                      )}
                                    </select>
                                    <p className="mt-2 text-[11px] leading-5 text-zinc-600">
                                      版本不会全部铺开；选择后可查看该版本的详情与使用方式。
                                    </p>
                                  </div>
                                  <div className="min-w-0">
                                    {selectedProtocolVersionItem ? (
                                      <>
                                        <div className="flex flex-wrap items-center gap-2">
                                          <span className="font-mono text-xs text-zinc-100">
                                            {item.coordinate}
                                          </span>
                                          <span
                                            className={`rounded px-1.5 py-0.5 text-[10px] ${isOci ? "bg-cyan-500/10 text-cyan-300" : "bg-violet-500/10 text-violet-300"}`}
                                          >
                                            {selectedProtocolVersionItem.label}
                                          </span>
                                          <a
                                            href={selectedProtocolHref}
                                            className="text-[11px] text-cyan-300 hover:text-cyan-200"
                                          >
                                            打开版本页
                                          </a>
                                          <button
                                            type="button"
                                            onClick={() =>
                                              void copyPageLink(
                                                selectedProtocolHref,
                                              )
                                            }
                                            className="text-[11px] text-zinc-500 hover:text-cyan-300"
                                          >
                                            {copiedCoordinate ===
                                            selectedProtocolHref
                                              ? "链接已复制"
                                              : "复制链接"}
                                          </button>
                                        </div>
                                        <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
                                          <MetadataItem
                                            label={isOci ? "镜像" : "Conan reference"}
                                            value={item.coordinate}
                                            mono
                                          />
                                          <MetadataItem
                                            label={isOci ? "标签" : "Recipe revision"}
                                            value={selectedProtocolVersionItem.value}
                                            mono
                                          />
                                          <MetadataItem
                                            label="发布时间"
                                            value={
                                              isConan
                                                ? formatDate(
                                                    selectedProtocolVersionItem.createdAt,
                                                  )
                                                : formatDate(item.createdAt)
                                            }
                                          />
                                          <MetadataItem
                                            label="发布者"
                                            value={item.publisher ?? "未记录"}
                                            mono
                                          />
                                          <MetadataItem
                                            label="校验摘要"
                                            value={
                                              selectedProtocolVersionItem.digest ??
                                              item.digest ??
                                              "未记录"
                                            }
                                            mono
                                          />
                                          {isOci && (
                                            <MetadataItem
                                              label="镜像大小"
                                              value={formatBytes(item.size)}
                                            />
                                          )}
                                        </div>
                                        <div className="mt-3 grid gap-3 sm:grid-cols-2">
                                          {snippets.map((snippet) => (
                                            <UsageSnippetBlock
                                              key={snippet.label}
                                              snippet={snippet}
                                              copied={
                                                copiedCoordinate ===
                                                snippet.code
                                              }
                                              onCopy={() =>
                                                void copyUsage(snippet)
                                              }
                                            />
                                          ))}
                                        </div>
                                      </>
                                    ) : (
                                      <div className="rounded-md border border-dashed border-zinc-800 px-4 py-6 text-sm text-zinc-600">
                                        {isOci
                                          ? "标签加载完成后，可在左侧搜索并选择一个版本。"
                                          : "Recipe revisions 加载完成后，可在左侧搜索并选择一个版本。"}
                                      </div>
                                    )}
                                  </div>
                                </div>
                              </td>
                            </tr>
                          )}
                          {expanded && !isVersioned && snippets.length > 0 && (
                            <tr key={`${item.coordinate}-${index}-usage`}>
                              <td
                                colSpan={5}
                                className="bg-zinc-950/50 px-4 py-4"
                              >
                                <div className="mb-4 grid grid-cols-2 gap-x-4 gap-y-3 border-b border-zinc-800/80 pb-4 text-xs sm:grid-cols-4">
                                  <MetadataItem
                                    label="仓库"
                                    value={selectedRepository?.name ?? "—"}
                                    mono
                                  />
                                  <MetadataItem
                                    label="制品坐标"
                                    value={item.coordinate}
                                    mono
                                  />
                                  <MetadataItem
                                    label={
                                      selectedRepository?.format === "oci"
                                        ? "镜像标签"
                                        : "发布时间"
                                    }
                                    value={
                                      selectedRepository?.format === "oci"
                                        ? tagParam || "选择标签后显示"
                                        : formatDate(item.createdAt)
                                    }
                                    mono={selectedRepository?.format === "oci"}
                                  />
                                  <MetadataItem
                                    label="发布者"
                                    value={item.publisher ?? "未记录"}
                                    mono
                                  />
                                </div>
                                <div className="grid gap-2 sm:grid-cols-2">
                                  {snippets.map((snippet) => (
                                    <UsageSnippetBlock
                                      key={snippet.label}
                                      snippet={snippet}
                                      copied={copiedCoordinate === snippet.code}
                                      onCopy={() => void copyUsage(snippet)}
                                    />
                                  ))}
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </DataTable>
                )}
              </Card>
            )}
          </section>
          {selectedRepository && (
            <aside className="lg:sticky lg:top-24">
              <Card className="overflow-hidden">
                <div className="border-b border-zinc-800/80 px-4 py-3">
                  <div className="text-sm font-medium text-zinc-100">
                    {usageLabel}
                  </div>
                  <div className="mt-1 text-xs text-zinc-500">
                    适用于仓库{" "}
                    <span className="font-mono text-zinc-300">
                      {selectedRepository.name}
                    </span>
                  </div>
                </div>
                <div className="space-y-3 p-3">
                  {globalUsage.map((snippet) => (
                    <UsageSnippetBlock
                      key={snippet.label}
                      snippet={snippet}
                      copied={copiedCoordinate === snippet.code}
                      onCopy={() => void copyUsage(snippet)}
                      compact
                    />
                  ))}
                  <div className="border-t border-zinc-800/80 pt-3 text-[11px] leading-5 text-zinc-600">
                    匿名浏览无需 Token；推送、私有仓库和管理操作仍需登录。
                  </div>
                </div>
              </Card>
            </aside>
          )}
        </div>
      </div>
    </main>
  );
}

function UsageSnippetBlock({
  snippet,
  copied,
  onCopy,
  compact = false,
}: {
  snippet: UsageSnippet;
  copied: boolean;
  onCopy: () => void;
  compact?: boolean;
}) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/70 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium text-zinc-400">
          {snippet.label}
        </span>
        <button
          type="button"
          title="复制使用方式"
          aria-label={`复制${snippet.label}`}
          onClick={onCopy}
          className="shrink-0 rounded p-1 text-zinc-600 hover:bg-zinc-800 hover:text-cyan-300"
        >
          {copied ? "✓" : "⧉"}
        </button>
      </div>
      <pre
        className={`max-w-full overflow-x-auto whitespace-pre font-mono text-[11px] leading-5 text-cyan-100 ${compact ? "max-h-24" : ""}`}
      >
        {snippet.code}
      </pre>
    </div>
  );
}

function MetadataItem({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium text-zinc-600">{label}</div>
      <div
        className={`mt-1 truncate text-zinc-300 ${mono ? "font-mono text-[11px]" : "text-xs"}`}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}
