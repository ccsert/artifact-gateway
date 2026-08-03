import { Fragment, useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import {
  ArrowLeftOutlined,
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LinkOutlined,
  ReloadOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { Button, Input, Select, Tooltip } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { listConanRecipeRevisions, searchRepositoryArtifacts } from "../client";
import type { ArtifactSummary } from "../client";
import { Card, DataTable } from "../components/Layout";
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

interface OciTagPage {
  items: string[];
  nextCursor?: string;
  loaded: boolean;
  loading: boolean;
  error?: string;
}

interface ConanRevisionPage {
  items: ConanRevision[];
  nextPageToken?: string;
  query: string;
  loaded: boolean;
  loading: boolean;
  error?: string;
}

const VERSION_PAGE_SIZE = 50;

type VersionSelectOption = { value: string; label: string };

function SearchableVersionSelect({
  value,
  options,
  onChange,
  loading = false,
  placeholder = "搜索并选择版本",
  notFoundContent = "没有匹配版本",
  className = "",
}: {
  value: string;
  options: VersionSelectOption[];
  onChange: (value: string) => void;
  loading?: boolean;
  placeholder?: string;
  notFoundContent?: string;
  className?: string;
}) {
  return (
    <Select
      showSearch={{
        optionFilterProp: "label",
        filterOption: (input, option) =>
          String(option?.label ?? "").toLowerCase().includes(input.toLowerCase()),
      }}
      value={value || undefined}
      options={options}
      onChange={onChange}
      loading={loading}
      placeholder={placeholder}
      notFoundContent={notFoundContent}
      listHeight={280}
      className={`w-full ${className}`}
    />
  );
}

function nextOciTagCursor(response: Response, tags: string[]): string | undefined {
  const link = response.headers.get("Link");
  const target = link?.match(/<([^>]+)>;\s*rel="next"/i)?.[1];
  if (target) {
    try {
      return new URL(target, window.location.origin).searchParams.get("last") ?? undefined;
    } catch {
      // Fall through to the page-size heuristic for non-standard registries.
    }
  }
  return tags.length === VERSION_PAGE_SIZE ? tags.at(-1) : undefined;
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

interface ConanArtifactGroup {
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

function conanReferenceParts(reference: string): { key: string; version: string } {
  const canonical = reference.split("/");
  if (canonical.length >= 4 && !reference.includes("@")) {
    return {
      key: `${canonical[0]}/${canonical[2]}/${canonical.slice(3).join("/")}`,
      version: canonical[1],
    };
  }
  const [nameAndVersion, channel = ""] = reference.split("@", 2);
  const separator = nameAndVersion.indexOf("/");
  if (separator < 0) return { key: reference, version: reference };
  const name = nameAndVersion.slice(0, separator);
  const version = nameAndVersion.slice(separator + 1);
  return { key: channel ? `${name}@${channel}` : name, version };
}

function conanArtifactGroups(items: ArtifactSummary[]): ConanArtifactGroup[] {
  const groups = new Map<string, ArtifactSummary[]>();
  for (const item of items) {
    const { key } = conanReferenceParts(item.coordinate);
    const current = groups.get(key) ?? [];
    if (!current.some((entry) => entry.coordinate === item.coordinate)) current.push(item);
    groups.set(key, current);
  }
  return [...groups.entries()]
    .map(([key, versions]) => ({
      key,
      versions: versions.sort((a, b) => {
        const left = conanReferenceParts(a.coordinate).version;
        const right = conanReferenceParts(b.coordinate).version;
        return right.localeCompare(left, undefined, { numeric: true, sensitivity: "base" });
      }),
    }))
    .sort((a, b) => a.key.localeCompare(b.key));
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

interface ConanGroupTableProps {
  groups: ConanArtifactGroup[];
  repository: PublicRepository;
  artifactParam: string;
  revisionParam: string;
  expandedGroup: string | null;
  selectedReferences: Record<string, string>;
  selectedRevisions: Record<string, string>;
  revisionPages: Record<string, ConanRevisionPage>;
  versionFilter: string;
  copiedCoordinate: string | null;
  onExpand: (key: string, reference: string) => void;
  onCollapse: () => void;
  onSelectReference: (key: string, reference: string) => void;
  onSelectRevision: (reference: string, revision: string) => void;
  onFilterChange: (value: string) => void;
  onLoadRevisions: (reference: string, query?: string, pageToken?: string) => void;
  onOpenArtifact: (reference: string, revision?: string) => void;
  onClearArtifactParams: () => void;
  artifactHref: (reference: string, revision?: string) => string;
  onCopyCoordinate: (value: string) => void;
  onCopyPageLink: (value: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

function ConanGroupTable({
  groups,
  repository,
  artifactParam,
  revisionParam,
  expandedGroup,
  selectedReferences,
  selectedRevisions,
  revisionPages,
  versionFilter,
  copiedCoordinate,
  onExpand,
  onCollapse,
  onSelectReference,
  onSelectRevision,
  onFilterChange,
  onLoadRevisions,
  onOpenArtifact,
  onClearArtifactParams,
  artifactHref,
  onCopyCoordinate,
  onCopyPageLink,
  onCopyUsage,
}: ConanGroupTableProps) {
  return (
    <DataTable
      className="min-w-[860px]"
      columns={["Conan 包", "最新版本", "版本数", "当前 revision", ""]}
    >
      {groups.map((group) => {
        const latest = group.versions[0];
        const urlReference = group.versions.some((version) => version.coordinate === artifactParam)
          ? artifactParam
          : "";
        const selectedReference = urlReference || selectedReferences[group.key] || latest.coordinate;
        const expanded = expandedGroup === group.key || Boolean(urlReference);
        const page = revisionPages[selectedReference];
        const revisions = page?.items ?? [];
        const normalizedFilter = versionFilter.trim().toLowerCase();
        const visibleRevisions = normalizedFilter
          ? revisions.filter((revision) => `${revision.revision} ${revision.digest ?? ""} ${revision.createdAt ?? ""}`.toLowerCase().includes(normalizedFilter))
          : revisions;
        const selectedRevision = selectedRevisions[selectedReference];
        const requestedRevision = artifactParam === selectedReference ? revisionParam : "";
        const preferredRevision =
          (selectedRevision && revisions.some((revision) => revision.revision === selectedRevision) && selectedRevision) ||
          (requestedRevision && revisions.some((revision) => revision.revision === requestedRevision) && requestedRevision) ||
          revisions[0]?.revision ||
          "";
        const selectedRevisionValue = visibleRevisions.some((revision) => revision.revision === preferredRevision)
          ? preferredRevision
          : visibleRevisions[0]?.revision || preferredRevision;
        const selectedRevisionItem = revisions.find((revision) => revision.revision === selectedRevisionValue);
        const referenceVersion = conanReferenceParts(selectedReference).version;
        const versionHref = selectedRevisionValue
          ? artifactHref(selectedReference, selectedRevisionValue)
          : artifactHref(selectedReference);
        const snippets = usageFor(repository.format, repository.name, selectedReference);
        return (
          <Fragment key={group.key}>
            <tr className="hover:bg-zinc-800/30">
              <td className="px-4 py-3 font-mono text-xs text-zinc-100">{group.key}</td>
              <td className="px-4 py-3 font-mono text-xs text-zinc-400">{conanReferenceParts(latest.coordinate).version}</td>
              <td className="px-4 py-3 text-xs text-zinc-500">{group.versions.length}</td>
              <td className="max-w-[240px] truncate px-4 py-3 font-mono text-xs text-zinc-500" title={selectedRevisionItem?.revision}>
                {selectedRevisionItem?.revision ?? (expanded ? "读取中…" : "展开后加载")}
              </td>
              <td className="whitespace-nowrap px-3 py-2 text-right">
                <Button
                  type="text"
                  size="small"
                  icon={expanded ? <UpOutlined /> : <DownOutlined />}
                  onClick={() => {
                    if (expanded) {
                      onCollapse();
                      onClearArtifactParams();
                      return;
                    }
                    onExpand(group.key, selectedReference);
                    onFilterChange("");
                    if (!revisionPages[selectedReference]) onLoadRevisions(selectedReference);
                    onOpenArtifact(selectedReference, selectedRevisionValue || undefined);
                  }}
                >
                  {expanded ? "收起" : "选择版本"}
                </Button>
                <Button type="link" size="small" icon={<LinkOutlined />} href={versionHref}>
                  {selectedRevisionValue ? "打开版本" : "打开"}
                </Button>
                <Tooltip title={copiedCoordinate === group.key ? "已复制" : "复制 Conan 包标识"}>
                  <Button
                    type="text"
                    size="small"
                    aria-label={`复制 ${group.key}`}
                    icon={copiedCoordinate === group.key ? <CheckOutlined /> : <CopyOutlined />}
                    onClick={() => onCopyCoordinate(group.key)}
                  />
                </Tooltip>
              </td>
            </tr>
            {expanded && (
              <tr>
                <td colSpan={5} className="bg-zinc-950/50 px-4 py-4">
                  <div className="grid gap-5 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
                    <div>
                      <div className="flex items-center justify-between gap-3">
                        <label className="text-[11px] font-medium text-zinc-500">
                          选择包版本 <span className="font-normal text-zinc-600">({group.versions.length})</span>
                        </label>
                        <span className="text-[11px] text-zinc-600">{referenceVersion}</span>
                      </div>
                      <SearchableVersionSelect
                        className="mt-1.5"
                        value={selectedReference}
                        options={group.versions.map((version) => ({
                          value: version.coordinate,
                          label: version.coordinate,
                        }))}
                        onChange={(reference) => {
                          onSelectReference(group.key, reference);
                          onFilterChange("");
                          onLoadRevisions(reference);
                          onOpenArtifact(reference);
                        }}
                        placeholder="搜索并选择 Conan 包版本"
                      />
                      <p className="mt-2 text-[11px] leading-5 text-zinc-600">同一 name@user/channel 下收拢不同版本；选定版本后再查看 recipe revision。</p>
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center justify-between gap-3">
                        <label className="text-[11px] font-medium text-zinc-500">Recipe revision</label>
                        <span className="text-[11px] text-zinc-600">{visibleRevisions.length}/{revisions.length}</span>
                      </div>
                      <div className="mt-1.5 flex gap-2">
                        <Input
                          className="min-w-0 flex-1 font-mono text-xs"
                          placeholder="输入 revision 或 digest"
                          value={versionFilter}
                          onChange={(event) => onFilterChange(event.target.value)}
                          onPressEnter={() => onLoadRevisions(selectedReference, versionFilter)}
                        />
                        <Button
                          loading={page?.loading === true}
                          onClick={() => onLoadRevisions(selectedReference, versionFilter)}
                        >
                          搜索
                        </Button>
                      </div>
                      {page?.error && <div className="mt-2 text-[11px] text-rose-300">{page.error}</div>}
                      <SearchableVersionSelect
                        className="mt-3"
                        value={selectedRevisionValue}
                        options={visibleRevisions.map((revision) => ({
                          value: revision.revision,
                          label: `${revision.revision} · ${shortDigest(revision.digest)}`,
                        }))}
                        loading={page?.loading === true}
                        notFoundContent={
                          page?.loading && visibleRevisions.length === 0
                            ? "正在读取 revision…"
                            : "没有匹配 revision"
                        }
                        placeholder="搜索并选择 recipe revision"
                        onChange={(revision) => {
                          onSelectRevision(selectedReference, revision);
                          onOpenArtifact(selectedReference, revision);
                        }}
                      />
                      {page?.nextPageToken && (
                        <Button
                          block
                          size="small"
                          loading={page.loading}
                          onClick={() => onLoadRevisions(selectedReference, page.query, page.nextPageToken)}
                          className="mt-2"
                        >
                          {page.loading ? "加载中…" : `再加载 ${VERSION_PAGE_SIZE} 个 revision`}
                        </Button>
                      )}
                      {selectedRevisionItem ? (
                        <>
                          <div className="mt-3 flex flex-wrap items-center gap-2">
                            <span className="font-mono text-xs text-zinc-100">{selectedReference}</span>
                            <span className="rounded bg-violet-500/10 px-1.5 py-0.5 text-[10px] text-violet-300">{selectedRevisionItem.revision}</span>
                            <Button type="link" size="small" icon={<LinkOutlined />} href={versionHref}>打开版本页</Button>
                            <Button type="link" size="small" onClick={() => onCopyPageLink(versionHref)}>
                              {copiedCoordinate === versionHref ? "链接已复制" : "复制链接"}
                            </Button>
                          </div>
                          <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
                            <MetadataItem label="Conan reference" value={selectedReference} mono />
                            <MetadataItem label="Recipe revision" value={selectedRevisionItem.revision} mono />
                            <MetadataItem label="发布时间" value={formatDate(selectedRevisionItem.createdAt)} />
                            <MetadataItem label="发布者" value={latest.publisher ?? "未记录"} mono />
                            <MetadataItem label="校验摘要" value={selectedRevisionItem.digest ?? "未记录"} mono />
                          </div>
                          <div className="mt-3 grid gap-3 sm:grid-cols-2">
                            {snippets.map((snippet) => (
                              <UsageSnippetBlock
                                key={snippet.label}
                                snippet={snippet}
                                copied={copiedCoordinate === snippet.code}
                                onCopy={() => onCopyUsage(snippet)}
                              />
                            ))}
                          </div>
                        </>
                      ) : (
                        <div className="mt-3 rounded-md border border-dashed border-zinc-800 px-4 py-6 text-sm text-zinc-600">选择一个 recipe revision 查看详情与使用方式。</div>
                      )}
                    </div>
                  </div>
                </td>
              </tr>
            )}
          </Fragment>
        );
      })}
    </DataTable>
  );
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
  const [expandedConanGroup, setExpandedConanGroup] = useState<string | null>(
    null,
  );
  const [selectedMavenVersion, setSelectedMavenVersion] = useState<
    string | null
  >(null);
  const [protocolVersionFilter, setProtocolVersionFilter] = useState("");
  const [selectedProtocolVersions, setSelectedProtocolVersions] = useState<
    Record<string, string>
  >({});
  const [selectedConanReferences, setSelectedConanReferences] = useState<
    Record<string, string>
  >({});
  const [ociTagPages, setOciTagPages] = useState<Record<string, OciTagPage>>({});
  const [conanRevisionPages, setConanRevisionPages] = useState<
    Record<string, ConanRevisionPage>
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
    setOciTagPages({});
    setConanRevisionPages({});
    setExpandedConanGroup(null);
    setSelectedConanReferences({});
  }, [repositoryId]);

  const loadOciTags = useCallback(async (coordinate: string, after = "") => {
    if (selectedRepository?.format !== "oci") return;
    setOciTagPages((current) => ({
      ...current,
      [coordinate]: {
        items: after ? current[coordinate]?.items ?? [] : [],
        loaded: current[coordinate]?.loaded ?? false,
        loading: true,
      },
    }));
    const imagePath = coordinate.split("/").map(encodeURIComponent).join("/");
    try {
      const query = new URLSearchParams({ n: String(VERSION_PAGE_SIZE) });
      if (after) query.set("last", after);
      const response = await fetch(`/v2/${encodeURIComponent(selectedRepository.name)}/${imagePath}/tags/list?${query}`);
      if (!response.ok) throw new Error(`读取 OCI 标签失败 (${response.status})`);
      const data = (await response.json()) as { tags?: string[] };
      const page = data.tags ?? [];
      setOciTagPages((current) => ({
        ...current,
        [coordinate]: {
          items: after ? [...new Set([...(current[coordinate]?.items ?? []), ...page])] : page,
          nextCursor: nextOciTagCursor(response, page),
          loaded: true,
          loading: false,
        },
      }));
    } catch (requestError) {
      setOciTagPages((current) => ({
        ...current,
        [coordinate]: {
          items: current[coordinate]?.items ?? [],
          loaded: true,
          loading: false,
          error: requestError instanceof Error ? requestError.message : "读取 OCI 标签失败",
        },
      }));
    }
  }, [selectedRepository]);

  const loadConanRevisions = useCallback(async (coordinate: string, query = "", pageToken = "") => {
    if (selectedRepository?.format !== "conan") return;
    const normalizedQuery = query.trim();
    setConanRevisionPages((current) => ({
      ...current,
      [coordinate]: {
        items: pageToken && current[coordinate]?.query === normalizedQuery ? current[coordinate]?.items ?? [] : [],
        query: normalizedQuery,
        loaded: current[coordinate]?.loaded ?? false,
        loading: true,
      },
    }));
    const { data, error: requestError } = await listConanRecipeRevisions({
      path: { repositoryId: selectedRepository.id },
      query: {
        reference: coordinate,
        q: normalizedQuery || undefined,
        pageSize: VERSION_PAGE_SIZE,
        pageToken: pageToken || undefined,
      },
    });
    if (requestError || !data) {
      setConanRevisionPages((current) => ({
        ...current,
        [coordinate]: {
          items: current[coordinate]?.items ?? [],
          query: normalizedQuery,
          loaded: true,
          loading: false,
          error: "读取 Conan revisions 失败",
        },
      }));
      return;
    }
    setConanRevisionPages((current) => ({
      ...current,
      [coordinate]: {
        items: pageToken && current[coordinate]?.query === normalizedQuery
          ? [...current[coordinate].items, ...data.items]
          : data.items,
        nextPageToken: data.nextPageToken,
        query: normalizedQuery,
        loaded: true,
        loading: false,
      },
    }));
  }, [selectedRepository]);

  useEffect(() => {
    if (!artifactParam || !items?.some((item) => item.coordinate === artifactParam)) return;
    if (selectedRepository?.format === "oci" && !ociTagPages[artifactParam]) {
      void loadOciTags(artifactParam);
    }
    if (selectedRepository?.format === "conan" && !conanRevisionPages[artifactParam]) {
      if (revisionParam) setProtocolVersionFilter(revisionParam);
      void loadConanRevisions(artifactParam, revisionParam);
    }
  }, [artifactParam, conanRevisionPages, items, loadConanRevisions, loadOciTags, ociTagPages, revisionParam, selectedRepository?.format]);

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
  const groupedConanItems =
    selectedRepository?.format === "conan"
      ? conanArtifactGroups(items ?? [])
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
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const clearArtifactParams = () => {
    const next = new URLSearchParams(params);
    next.delete("artifact");
    next.delete("build");
    next.delete("tag");
    next.delete("revision");
    setParams(next, { replace: true, preventScrollReset: true });
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
              <Button type="link" size="small" icon={<ArrowLeftOutlined />} className="ml-auto" onClick={() => setParams({})}>
                返回公开仓库
              </Button>
            </div>
          )}
          {repositoryId && (
            <form onSubmit={submit} className="mt-5 flex items-center gap-2">
              <Input
                className="min-w-0 flex-1 font-mono text-xs"
                placeholder="坐标、路径或名称前缀（可选）"
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
              />
              <Button type="primary" htmlType="submit" className="shrink-0 whitespace-nowrap">搜索</Button>
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
                  <Button type="primary" onClick={() => setParams({})}>
                    返回目录
                  </Button>
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
                    <Link
                      key={repository.id}
                      to={`/browse?repository=${encodeURIComponent(repository.id)}`}
                      className="block text-left"
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
                    </Link>
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
                      {groupedItems?.length ?? groupedConanItems?.length ?? items.length}
                    </span>{" "}
                    个制品
                    {(groupedItems || groupedConanItems) && (
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
                      const visibleVersions = group.versions;
                      const preferredKey =
                        selectedMavenVersion &&
                        group.versions.some(
                          (version, index) =>
                            mavenVersionKey(version, index) ===
                            selectedMavenVersion,
                        )
                          ? selectedMavenVersion
                          : urlVersion
                            ? mavenVersionKey(
                                urlVersion,
                                group.versions.indexOf(urlVersion),
                              )
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
                            undefined,
                            {
                              buildNumber: selectedVersion.buildNumber,
                              createdAt: selectedVersion.createdAt,
                            },
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
                              <Button
                                type="text"
                                size="small"
                                icon={expanded ? <UpOutlined /> : <DownOutlined />}
                                onClick={() => {
                                  if (expanded) {
                                    setExpandedMavenGroup(null);
                                    clearArtifactParams();
                                    return;
                                  }
                                  setExpandedMavenGroup(group.key);
                                  setSelectedMavenVersion(mavenVersionKey(latest, 0));
                                }}
                              >
                                {expanded ? "收起" : "选择版本"}
                              </Button>
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
                                      选择版本{" "}
                                      <span className="font-normal text-zinc-600">
                                        ({group.versions.length})
                                      </span>
                                    </label>
                                    <SearchableVersionSelect
                                      value={selectedKey}
                                      options={visibleVersions.map((version) => {
                                        const index = group.versions.indexOf(version);
                                        return {
                                          value: mavenVersionKey(version, index),
                                          label: `${version.coordinate.split(":").slice(2).join(":")}${
                                            version.buildNumber
                                              ? ` · SNAPSHOT #${version.buildNumber}`
                                              : ""
                                          }`,
                                        };
                                      })}
                                      onChange={(value) => {
                                        setSelectedMavenVersion(value);
                                        const version = group.versions.find(
                                          (item, index) =>
                                            mavenVersionKey(item, index) ===
                                            value,
                                        );
                                        if (version)
                                          openArtifact(
                                            version.coordinate,
                                            version.buildNumber,
                                          );
                                      }}
                                      placeholder="搜索并选择 Maven 版本"
                                    />
                                    <p className="mt-2 text-[11px] leading-5 text-zinc-600">
                                      在选择器中输入版本号或 SNAPSHOT 构建号即可定位，不会铺开全部版本。
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
                                      <Button
                                        type="link"
                                        size="small"
                                        icon={<LinkOutlined />}
                                        href={artifactHref(
                                          selectedVersion.coordinate,
                                          selectedVersion.buildNumber,
                                        )}
                                      >
                                        打开版本页
                                      </Button>
                                      <Button
                                        type="link"
                                        size="small"
                                        onClick={() =>
                                          void copyPageLink(
                                            artifactHref(
                                              selectedVersion.coordinate,
                                              selectedVersion.buildNumber,
                                            ),
                                          )
                                        }
                                      >
                                        {copiedCoordinate ===
                                        artifactHref(
                                          selectedVersion.coordinate,
                                          selectedVersion.buildNumber,
                                        )
                                          ? "链接已复制"
                                          : "复制链接"}
                                      </Button>
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
                ) : groupedConanItems && selectedRepository ? (
                  <ConanGroupTable
                    groups={groupedConanItems}
                    repository={selectedRepository}
                    artifactParam={artifactParam}
                    revisionParam={revisionParam}
                    expandedGroup={expandedConanGroup}
                    selectedReferences={selectedConanReferences}
                    selectedRevisions={selectedProtocolVersions}
                    revisionPages={conanRevisionPages}
                    versionFilter={protocolVersionFilter}
                    copiedCoordinate={copiedCoordinate}
                    onExpand={(key, reference) => {
                      setExpandedConanGroup(key);
                      setSelectedConanReferences((current) => ({ ...current, [key]: reference }));
                    }}
                    onCollapse={() => setExpandedConanGroup(null)}
                    onSelectReference={(key, reference) => setSelectedConanReferences((current) => ({ ...current, [key]: reference }))}
                    onSelectRevision={(reference, revision) => setSelectedProtocolVersions((current) => ({ ...current, [reference]: revision }))}
                    onFilterChange={setProtocolVersionFilter}
                    onLoadRevisions={(reference, filter, pageToken) => void loadConanRevisions(reference, filter, pageToken)}
                    onOpenArtifact={(reference, revision) => openArtifact(reference, undefined, undefined, revision)}
                    onClearArtifactParams={clearArtifactParams}
                    artifactHref={(reference, revision) => artifactHref(reference, undefined, undefined, revision)}
                    onCopyCoordinate={(value) => void copyCoordinate(value)}
                    onCopyPageLink={(value) => void copyPageLink(value)}
                    onCopyUsage={(snippet) => void copyUsage(snippet)}
                  />
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
                        ? ["镜像", "已加载标签", "当前标签", "镜像摘要", ""]
                        : selectedRepository?.format === "conan"
                          ? [
                              "Conan reference",
                              "已加载版本",
                              "当前 revision",
                              "当前摘要",
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
                      const ociPage = isOci ? ociTagPages[item.coordinate] : undefined;
                      const conanPage = isConan ? conanRevisionPages[item.coordinate] : undefined;
                      const tags = isOci ? (ociPage?.items ?? []) : [];
                      const revisions = isConan ? (conanPage?.items ?? []) : [];
                      const protocolVersionsLoaded = isOci
                        ? ociPage?.loaded === true
                        : isConan
                          ? conanPage?.loaded === true
                          : true;
                      const protocolVersionsLoading = isOci ? ociPage?.loading === true : isConan ? conanPage?.loading === true : false;
                      const protocolVersionsError = isOci ? ociPage?.error : conanPage?.error;
                      const nextProtocolPage = isOci ? ociPage?.nextCursor : conanPage?.nextPageToken;
                      const protocolVersions: ProtocolVersion[] = isOci
                        ? [...new Set(
                            tagParam && artifactParam === item.coordinate && !tags.includes(tagParam)
                              ? [...tags, tagParam]
                              : tags,
                          )].map((tag) => ({
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
                      const visibleProtocolVersions = protocolVersions;
                      const preferredProtocolVersion =
                        (selectedProtocolVersions[item.coordinate] &&
                          protocolVersions.some(
                            (version) =>
                              version.value ===
                              selectedProtocolVersions[item.coordinate],
                          ) &&
                          selectedProtocolVersions[item.coordinate]) ||
                        (requestedProtocolVersion &&
                          protocolVersions.some(
                            (version) =>
                              version.value === requestedProtocolVersion,
                          ) &&
                          requestedProtocolVersion) ||
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
                                  {protocolVersionsLoading && !protocolVersionsLoaded
                                    ? "读取中…"
                                    : !protocolVersionsLoaded
                                    ? "展开后加载"
                                    : protocolVersions.length > 0
                                    ? `${protocolVersions.length}${nextProtocolPage ? "+" : ""} 个`
                                    : "—"}
                                </td>
                                <td
                                  className="hidden max-w-[220px] px-4 py-3 font-mono text-xs text-zinc-500 sm:table-cell"
                                  title={
                                    isOci
                                      ? selectedProtocolVersionItem?.label
                                      : protocolVersions[0]?.value
                                  }
                                >
                                  <div className="truncate">
                                    {protocolVersionsLoading && !protocolVersionsLoaded
                                      ? "读取中…"
                                      : !protocolVersionsLoaded
                                      ? "—"
                                      : protocolVersions.length > 0
                                        ? isOci
                                        ? selectedProtocolVersionItem?.label
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
                              <Button
                                type="text"
                                size="small"
                                icon={expanded ? <UpOutlined /> : <DownOutlined />}
                                title={
                                  isVersioned ? "选择版本" : "查看使用方式"
                                }
                                onClick={() =>
                                  (() => {
                                    if (expanded) {
                                      setExpandedCoordinate(null);
                                      clearArtifactParams();
                                      return;
                                    }
                                    setExpandedCoordinate(item.coordinate);
                                    setProtocolVersionFilter("");
                                    if (isOci && !ociPage) void loadOciTags(item.coordinate);
                                    if (isConan && !conanPage) void loadConanRevisions(item.coordinate);
                                    if (selectedProtocolVersionValue) {
                                      setSelectedProtocolVersions(
                                        (current) => ({
                                          ...current,
                                          [item.coordinate]:
                                            selectedProtocolVersionValue,
                                        }),
                                      );
                                    }
                                  })()
                                }
                              >
                                {expanded
                                  ? "收起"
                                  : isVersioned
                                    ? "选择版本"
                                    : "使用方式"}
                              </Button>
                              <Button
                                type="link"
                                size="small"
                                icon={<LinkOutlined />}
                                href={
                                  isVersioned
                                    ? selectedProtocolHref
                                    : artifactHref(item.coordinate)
                                }
                                className={isVersioned ? "hidden sm:inline-flex" : ""}
                              >
                                {isVersioned && selectedProtocolVersionItem
                                  ? "打开版本"
                                  : "打开"}
                              </Button>
                              <Tooltip title={copiedCoordinate === item.coordinate ? "已复制" : "复制制品坐标"}>
                                <Button
                                type="text"
                                size="small"
                                aria-label={`复制 ${item.coordinate}`}
                                onClick={() =>
                                  void copyCoordinate(item.coordinate)
                                }
                                icon={copiedCoordinate === item.coordinate ? <CheckOutlined /> : <CopyOutlined />}
                              />
                              </Tooltip>
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
                                        {isOci ? "选择镜像标签" : "搜索 recipe revision"}
                                      </label>
                                      <span className="text-[11px] text-zinc-600">
                                        已加载 {protocolVersions.length}
                                      </span>
                                    </div>
                                    {isConan && (
                                      <div className="mt-1.5 flex gap-2">
                                        <Input
                                          className="min-w-0 flex-1 font-mono text-xs"
                                          placeholder="输入 revision 或 digest"
                                          value={protocolVersionFilter}
                                          onChange={(event) => setProtocolVersionFilter(event.target.value)}
                                          onPressEnter={() => void loadConanRevisions(item.coordinate, protocolVersionFilter)}
                                        />
                                        <Button
                                          loading={protocolVersionsLoading}
                                          onClick={() => void loadConanRevisions(item.coordinate, protocolVersionFilter)}
                                        >
                                          搜索
                                        </Button>
                                      </div>
                                    )}
                                    {protocolVersionsError && (
                                      <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-rose-300">
                                        <span>{protocolVersionsError}</span>
                                        <Button
                                          type="link"
                                          size="small"
                                          icon={<ReloadOutlined />}
                                          onClick={() => isOci ? void loadOciTags(item.coordinate) : void loadConanRevisions(item.coordinate, protocolVersionFilter)}
                                        >
                                          重试
                                        </Button>
                                      </div>
                                    )}
                                    <label className={`${isConan ? "mt-3 " : "mt-1.5 "}mb-1.5 block text-[11px] font-medium text-zinc-500`}>
                                      选择版本
                                    </label>
                                    <SearchableVersionSelect
                                      value={selectedProtocolVersionValue}
                                      options={visibleProtocolVersions.map((version) => ({
                                        value: version.value,
                                        label: `${version.label}${
                                          isConan && version.digest
                                            ? ` · ${shortDigest(version.digest)}`
                                            : ""
                                        }`,
                                      }))}
                                      loading={protocolVersionsLoading}
                                      notFoundContent={
                                        protocolVersionsLoading && visibleProtocolVersions.length === 0
                                          ? "正在读取版本…"
                                          : "没有匹配版本"
                                      }
                                      placeholder={`搜索并选择${isOci ? "镜像标签" : " recipe revision"}`}
                                      onChange={(value) => {
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
                                    />
                                    {nextProtocolPage && (
                                      <Button
                                        block
                                        size="small"
                                        loading={protocolVersionsLoading}
                                        onClick={() => isOci
                                          ? void loadOciTags(item.coordinate, nextProtocolPage)
                                          : void loadConanRevisions(item.coordinate, conanPage?.query ?? protocolVersionFilter, nextProtocolPage)}
                                        className="mt-2"
                                      >
                                        {protocolVersionsLoading ? "加载中…" : `再加载 ${VERSION_PAGE_SIZE} 个版本`}
                                      </Button>
                                    )}
                                    <p className="mt-2 text-[11px] leading-5 text-zinc-600">
                                      每次最多读取 {VERSION_PAGE_SIZE} 个版本；选择后可查看详情与使用方式。
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
                                          <Button
                                            type="link"
                                            size="small"
                                            icon={<LinkOutlined />}
                                            href={selectedProtocolHref}
                                          >
                                            打开版本页
                                          </Button>
                                          <Button
                                            type="link"
                                            size="small"
                                            onClick={() =>
                                              void copyPageLink(
                                                selectedProtocolHref,
                                              )
                                            }
                                          >
                                            {copiedCoordinate ===
                                            selectedProtocolHref
                                              ? "链接已复制"
                                              : "复制链接"}
                                          </Button>
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
        <Tooltip title={copied ? "已复制" : "复制使用方式"}>
          <Button
            type="text"
            size="small"
            aria-label={`复制${snippet.label}`}
            onClick={onCopy}
            icon={copied ? <CheckOutlined /> : <CopyOutlined />}
          />
        </Tooltip>
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
