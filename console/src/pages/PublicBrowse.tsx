import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LinkOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { Button, Input, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useSearchParams } from "react-router-dom";
import {
  listConanRecipeRevisions,
  listOciManifests,
  searchRepositoryArtifacts,
} from "../client";
import type { ArtifactSummary } from "../client";
import { Card } from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  EmptyStateArtwork,
} from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usageFor, type UsageSnippet } from "../lib/usage";
import { PreferenceControls } from "../components/PreferenceControls";
import { usePreferences } from "../lib/preferences";
import {
  artifactBrowseParams,
  artifactBrowsePath,
  clearArtifactBrowseParams,
  conanArtifactGroups,
  conanReferenceParts,
  mavenArtifactGroups,
  mavenVersionKey,
  missingDeepLinkedArtifact,
  type ConanArtifactGroup,
  type MavenArtifactGroup,
} from "../lib/publicBrowseModel";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../components/PublicBrowsePrimitives";
import emptyPublicCatalogDark from "../assets/empty-public-catalog.webp";
import emptyPublicCatalogLight from "../assets/empty-public-catalog-light.webp";
import { NpmPackageDetail } from "../components/NpmPackageDetail";
import { PyPIProjectDetail } from "../components/PyPIProjectDetail";
import { GoModuleDetail } from "../components/GoModuleDetail";
import { APTAssetDetail } from "../components/APTAssetDetail";
import { useClipboardAction } from "../components/ConsolePrimitives";
import { SiteBrandMark, SiteName } from "../components/SiteBrand";

type PublicRepositoryFormat =
  "oci" | "maven" | "conan" | "raw" | "npm" | "pypi" | "go" | "apt";

interface PublicRepository {
  id: string;
  name: string;
  format: PublicRepositoryFormat;
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

interface OciManifestDetail {
  loading: boolean;
  digest?: string;
  size?: number;
  createdAt?: string;
  publisher?: string;
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

const PUBLIC_FORMAT_ORDER: PublicRepositoryFormat[] = [
  "oci",
  "maven",
  "npm",
  "pypi",
  "go",
  "apt",
  "conan",
  "raw",
];

const PUBLIC_FORMAT_STYLE: Record<
  PublicRepositoryFormat,
  { icon: string; surface: string }
> = {
  oci: {
    icon: "OCI",
    surface: "border-violet-400/20 bg-violet-400/10 text-violet-200",
  },
  maven: {
    icon: "MVN",
    surface: "border-orange-400/20 bg-orange-400/10 text-orange-200",
  },
  npm: {
    icon: "NPM",
    surface: "border-rose-400/20 bg-rose-400/10 text-rose-200",
  },
  pypi: {
    icon: "PY",
    surface: "border-blue-400/20 bg-blue-400/10 text-blue-200",
  },
  go: {
    icon: "GO",
    surface: "border-cyan-400/20 bg-cyan-400/10 text-cyan-200",
  },
  apt: {
    icon: "APT",
    surface: "border-emerald-400/20 bg-emerald-400/10 text-emerald-200",
  },
  conan: {
    icon: "C++",
    surface: "border-amber-400/20 bg-amber-400/10 text-amber-200",
  },
  raw: {
    icon: "RAW",
    surface: "border-zinc-400/20 bg-zinc-400/10 text-zinc-200",
  },
};

function nextOciTagCursor(
  response: Response,
  tags: string[],
): string | undefined {
  const link = response.headers.get("Link");
  const target = link?.match(/<([^>]+)>;\s*rel="next"/i)?.[1];
  if (target) {
    try {
      return (
        new URL(target, window.location.origin).searchParams.get("last") ??
        undefined
      );
    } catch {
      // Fall through to the page-size heuristic for non-standard registries.
    }
  }
  return tags.length === VERSION_PAGE_SIZE ? tags.at(-1) : undefined;
}

const OCI_MANIFEST_ACCEPT = [
  "application/vnd.oci.image.manifest.v1+json",
  "application/vnd.docker.distribution.manifest.v2+json",
  "application/vnd.oci.image.index.v1+json",
  "application/vnd.docker.distribution.manifest.list.v2+json",
].join(", ");

type OciManifestEnvelope = {
  config?: { digest?: string; size?: number };
  layers?: Array<{ size?: number }>;
  manifests?: Array<{ digest?: string }>;
};

type OciConfigEnvelope = {
  created?: string;
  author?: string;
  config?: { Labels?: Record<string, string> };
};

async function readOciManifestDetail(
  repositoryName: string,
  imageName: string,
  reference: string,
  depth = 0,
): Promise<Omit<OciManifestDetail, "loading">> {
  const imagePath = imageName.split("/").map(encodeURIComponent).join("/");
  const response = await fetch(
    `/v2/${encodeURIComponent(repositoryName)}/${imagePath}/manifests/${encodeURIComponent(reference)}`,
    { headers: { Accept: OCI_MANIFEST_ACCEPT } },
  );
  if (!response.ok)
    throw new Error(`读取 OCI manifest 失败 (${response.status})`);
  const envelope = (await response.json()) as OciManifestEnvelope;
  const digest = response.headers.get("Docker-Content-Digest") ?? undefined;
  const layerSize = (envelope.layers ?? []).reduce(
    (total, layer) => total + (layer.size ?? 0),
    envelope.config?.size ?? 0,
  );

  // Multi-platform indexes point to a child manifest. Read its config for the
  // same useful metadata while retaining the digest of the selected tag.
  if (!envelope.config && depth < 2) {
    const child = envelope.manifests?.find((entry) => entry.digest);
    if (child?.digest) {
      const nested = await readOciManifestDetail(
        repositoryName,
        imageName,
        child.digest,
        depth + 1,
      );
      return {
        ...nested,
        digest: digest ?? nested.digest,
        size: nested.size ?? layerSize,
      };
    }
  }

  let createdAt: string | undefined;
  let publisher: string | undefined;
  if (envelope.config?.digest) {
    const configResponse = await fetch(
      `/v2/${encodeURIComponent(repositoryName)}/${imagePath}/blobs/${encodeURIComponent(envelope.config.digest)}`,
    );
    if (configResponse.ok) {
      const config = (await configResponse.json()) as OciConfigEnvelope;
      const labels = config.config?.Labels ?? {};
      createdAt = config.created ?? labels["org.opencontainers.image.created"];
      publisher =
        config.author ||
        labels["org.opencontainers.image.authors"] ||
        labels["org.opencontainers.image.vendor"];
    }
  }
  return { digest, size: layerSize || undefined, createdAt, publisher };
}

type ProtocolVersion = {
  value: string;
  label: string;
  searchText: string;
  createdAt?: string;
  digest?: string;
};

interface PublicArtifactTableRow {
  key: string;
  item: ArtifactSummary;
  isOci: boolean;
  expanded: boolean;
  page?: OciTagPage;
  protocolVersions: ProtocolVersion[];
  protocolVersionsLoaded: boolean;
  protocolVersionsLoading: boolean;
  protocolVersionsError?: string;
  nextProtocolPage?: string;
  selectedProtocolVersionValue: string;
  selectedProtocolVersionItem?: ProtocolVersion;
  selectedTag?: string;
  ociDetail?: OciManifestDetail;
  selectedProtocolHref: string;
  snippets: UsageSnippet[];
}

function repositoryUsage(
  format: PublicRepository["format"],
  repoName: string,
  text: (chinese: string, english: string) => string,
): UsageSnippet[] {
  const origin = window.location.origin;
  const host = window.location.host;
  if (format === "maven") {
    const url = `${origin}/maven/${repoName}`;
    return [
      { label: text("Maven 仓库 URL", "Maven repository URL"), code: url },
      {
        label: "settings.xml",
        code: `<repository>\n  <id>${repoName}</id>\n  <url>${url}</url>\n</repository>`,
      },
      { label: "Gradle repositories", code: `maven { url = uri("${url}") }` },
    ];
  }
  if (format === "oci") {
    return [
      {
        label: text("OCI Registry 地址", "OCI registry address"),
        code: `${host}/${repoName}`,
      },
      {
        label: text("Docker Registry 配置", "Docker registry setup"),
        code: text(
          `docker login ${host}\n# 镜像前缀：${host}/${repoName}/`,
          `docker login ${host}\n# Image prefix: ${host}/${repoName}/`,
        ),
      },
    ];
  }
  if (format === "conan") {
    return [
      {
        label: text("Conan remote 地址", "Conan remote address"),
        code: `conan remote add ${repoName} ${origin}/conan/v2/${repoName}`,
      },
    ];
  }
  if (format === "npm") {
    const registry = `${origin}/npm/${repoName}/`;
    return [
      { label: text("npm Registry 地址", "npm registry URL"), code: registry },
      { label: ".npmrc", code: `registry=${registry}` },
    ];
  }
  if (format === "pypi") {
    const index = `${origin}/pypi/${repoName}/simple/`;
    return [
      { label: text("PyPI Simple API", "PyPI Simple API"), code: index },
      {
        label: "pip",
        code: `pip config set global.index-url ${index}`,
      },
    ];
  }
  if (format === "go") {
    const proxy = `${origin}/go/${repoName}`;
    return [
      { label: "GOPROXY", code: `go env -w GOPROXY=${proxy}` },
      {
        label: text("临时使用", "One-off usage"),
        code: `GOPROXY=${proxy} go mod download`,
      },
    ];
  }
  if (format === "apt") {
    const source = `${origin}/apt/${repoName}`;
    return [
      {
        label: text("APT 源地址", "APT source URL"),
        code: source,
      },
      {
        label: "sources.list",
        code: `deb ${source} <suite> <component>`,
      },
      {
        label: text("下载制品", "Download an artifact"),
        code: `curl -fsSL ${source}/pool/<component>/<path>/<package>.deb -o package.deb`,
      },
    ];
  }
  return [{ label: "Raw 仓库地址", code: `${origin}/raw/${repoName}/` }];
}

interface MavenGroupTableProps {
  groups: MavenArtifactGroup[];
  repository: PublicRepository;
  artifactParam: string;
  buildParam: string;
  expandedGroup: string | null;
  selectedVersionKey: string | null;
  copiedCoordinate: string | null;
  onExpand: (groupKey: string, versionKey: string) => void;
  onCollapse: () => void;
  onSelectVersion: (group: MavenArtifactGroup, versionKey: string) => void;
  artifactHref: (coordinate: string, buildNumber?: number) => string;
  onCopyPageLink: (href: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

interface MavenTableRow {
  key: string;
  group: MavenArtifactGroup;
  latest: ArtifactSummary;
  expanded: boolean;
  selectedKey: string;
  selectedVersion: ArtifactSummary;
  snippets: UsageSnippet[];
}

function MavenGroupTable({
  groups,
  repository,
  artifactParam,
  buildParam,
  expandedGroup,
  selectedVersionKey,
  copiedCoordinate,
  onExpand,
  onCollapse,
  onSelectVersion,
  artifactHref,
  onCopyPageLink,
  onCopyUsage,
}: MavenGroupTableProps) {
  const { locale, text } = usePreferences();
  const tableRows: MavenTableRow[] = groups.map((group) => {
    const latest = group.versions[0];
    const urlVersion = group.versions.find(
      (version) =>
        version.coordinate === artifactParam &&
        (!buildParam || String(version.buildNumber ?? 0) === buildParam),
    );
    const expanded = expandedGroup === group.key || Boolean(urlVersion);
    const preferredKey =
      selectedVersionKey &&
      group.versions.some(
        (version, index) =>
          mavenVersionKey(version, index) === selectedVersionKey,
      )
        ? selectedVersionKey
        : urlVersion
          ? mavenVersionKey(urlVersion, group.versions.indexOf(urlVersion))
          : mavenVersionKey(latest, 0);
    const selectedKey = group.versions.some(
      (version, index) => mavenVersionKey(version, index) === preferredKey,
    )
      ? preferredKey
      : mavenVersionKey(latest, 0);
    const selectedVersion =
      group.versions.find(
        (version, index) => mavenVersionKey(version, index) === selectedKey,
      ) ?? latest;
    return {
      key: group.key,
      group,
      latest,
      expanded,
      selectedKey,
      selectedVersion,
      snippets: usageFor(
        repository.format,
        repository.name,
        selectedVersion.coordinate,
        undefined,
        {
          buildNumber: selectedVersion.buildNumber,
          createdAt: selectedVersion.createdAt,
        },
      ),
    };
  });

  const columns: ColumnsType<MavenTableRow> = [
    {
      title: text("制品", "Artifact"),
      dataIndex: "key",
      key: "key",
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-100">{value}</span>
      ),
    },
    {
      title: text("最新版本", "Latest version"),
      key: "latest",
      width: 180,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {row.latest.coordinate.split(":").slice(2).join(":")}
        </span>
      ),
    },
    {
      title: text("版本数", "Versions"),
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-zinc-500">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 130,
      render: (_, row) => (
        <div className="text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() =>
              row.expanded
                ? onCollapse()
                : onExpand(row.key, mavenVersionKey(row.latest, 0))
            }
          >
            {row.expanded
              ? text("收起", "Collapse")
              : text("选择版本", "Select version")}
          </Button>
        </div>
      ),
    },
  ];

  const expandedRowRender = (row: MavenTableRow) => {
    const href = artifactHref(
      row.selectedVersion.coordinate,
      row.selectedVersion.buildNumber,
    );
    return (
      <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
        <div>
          <label className="mb-1.5 block text-xs font-medium text-zinc-500">
            {text("选择版本", "Select version")}{" "}
            <span className="font-normal text-zinc-600">
              ({row.group.versions.length})
            </span>
          </label>
          <SearchableVersionSelect
            value={row.selectedKey}
            options={row.group.versions.map((version, index) => ({
              value: mavenVersionKey(version, index),
              label: `${version.coordinate.split(":").slice(2).join(":")}${
                version.buildNumber ? ` · SNAPSHOT #${version.buildNumber}` : ""
              }`,
            }))}
            onChange={(value) => onSelectVersion(row.group, value)}
            placeholder={text("搜索并选择 Maven 版本", "Search Maven versions")}
          />
          <p className="mt-2 text-xs leading-5 text-zinc-600">
            {text(
              "在选择器中输入版本号或 SNAPSHOT 构建号即可定位，不会铺开全部版本。",
              "Search by version or SNAPSHOT build number without expanding the full list.",
            )}
          </p>
        </div>
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs text-zinc-100">
              {row.selectedVersion.coordinate}
            </span>
            {row.selectedVersion.buildNumber ? (
              <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-300">
                SNAPSHOT #{row.selectedVersion.buildNumber}
              </span>
            ) : null}
            <Button
              type="link"
              size="small"
              icon={<LinkOutlined />}
              href={href}
            >
              {text("打开版本页", "Open version")}
            </Button>
            <Button
              type="link"
              size="small"
              onClick={() => onCopyPageLink(href)}
            >
              {copiedCoordinate === href
                ? text("链接已复制", "Link copied")
                : text("复制链接", "Copy link")}
            </Button>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-zinc-600">
            <span>{formatDate(row.selectedVersion.createdAt, locale)}</span>
            <span
              className="max-w-[min(70vw,560px)] truncate font-mono text-zinc-500"
              title={row.selectedVersion.digest}
            >
              {row.selectedVersion.digest ?? "—"}
            </span>
            <span>{formatBytes(row.selectedVersion.size)}</span>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
            <MetadataItem
              label={text("发布时间", "Published")}
              value={formatDate(row.selectedVersion.createdAt, locale)}
            />
            <MetadataItem
              label={text("发布者", "Publisher")}
              value={
                row.selectedVersion.publisher ?? text("未记录", "Not recorded")
              }
              mono
            />
            <MetadataItem
              label={text("校验摘要", "Digest")}
              value={
                row.selectedVersion.digest ?? text("未记录", "Not recorded")
              }
              mono
            />
            <MetadataItem
              label={text("构建类型", "Build type")}
              value={
                row.selectedVersion.buildNumber
                  ? `SNAPSHOT #${row.selectedVersion.buildNumber}`
                  : text("发布版本", "Release")
              }
            />
          </div>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {row.snippets.map((snippet) => (
              <UsageSnippetBlock
                key={snippet.label}
                snippet={snippet}
                copied={copiedCoordinate === snippet.code}
                onCopy={() => onCopyUsage(snippet)}
              />
            ))}
          </div>
        </div>
      </div>
    );
  };

  return (
    <Table<MavenTableRow>
      className="ag-console-table"
      rowKey="key"
      size="middle"
      dataSource={tableRows}
      columns={columns}
      pagination={false}
      scroll={{ x: 720 }}
      expandable={{
        expandedRowKeys: tableRows
          .filter((row) => row.expanded)
          .map((row) => row.key),
        expandedRowRender,
        showExpandColumn: false,
      }}
    />
  );
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
  onLoadRevisions: (
    reference: string,
    query?: string,
    pageToken?: string,
  ) => void;
  onOpenArtifact: (reference: string, revision?: string) => void;
  onClearArtifactParams: () => void;
  artifactHref: (reference: string, revision?: string) => string;
  onCopyCoordinate: (value: string) => void;
  onCopyPageLink: (value: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

interface ConanTableRow {
  key: string;
  group: ConanArtifactGroup;
  latest: ArtifactSummary;
  selectedReference: string;
  expanded: boolean;
  page?: ConanRevisionPage;
  revisions: ConanRevision[];
  visibleRevisions: ConanRevision[];
  selectedRevisionValue: string;
  selectedRevisionItem?: ConanRevision;
  referenceVersion: string;
  versionHref: string;
  snippets: UsageSnippet[];
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
  const { locale, text } = usePreferences();
  const tableRows: ConanTableRow[] = groups.map((group) => {
    const latest = group.versions[0];
    const urlReference = group.versions.some(
      (version) => version.coordinate === artifactParam,
    )
      ? artifactParam
      : "";
    const selectedReference =
      urlReference || selectedReferences[group.key] || latest.coordinate;
    const expanded = expandedGroup === group.key || Boolean(urlReference);
    const page = revisionPages[selectedReference];
    const revisions = page?.items ?? [];
    const normalizedFilter = versionFilter.trim().toLowerCase();
    const visibleRevisions = normalizedFilter
      ? revisions.filter((revision) =>
          `${revision.revision} ${revision.digest ?? ""} ${revision.createdAt ?? ""}`
            .toLowerCase()
            .includes(normalizedFilter),
        )
      : revisions;
    const selectedRevision = selectedRevisions[selectedReference];
    const requestedRevision =
      artifactParam === selectedReference ? revisionParam : "";
    const preferredRevision =
      (selectedRevision &&
        revisions.some((revision) => revision.revision === selectedRevision) &&
        selectedRevision) ||
      (requestedRevision &&
        revisions.some((revision) => revision.revision === requestedRevision) &&
        requestedRevision) ||
      revisions[0]?.revision ||
      "";
    const selectedRevisionValue = visibleRevisions.some(
      (revision) => revision.revision === preferredRevision,
    )
      ? preferredRevision
      : visibleRevisions[0]?.revision || preferredRevision;
    const selectedRevisionItem = revisions.find(
      (revision) => revision.revision === selectedRevisionValue,
    );
    return {
      key: group.key,
      group,
      latest,
      selectedReference,
      expanded,
      page,
      revisions,
      visibleRevisions,
      selectedRevisionValue,
      selectedRevisionItem,
      referenceVersion: conanReferenceParts(selectedReference).version,
      versionHref: selectedRevisionValue
        ? artifactHref(selectedReference, selectedRevisionValue)
        : artifactHref(selectedReference),
      snippets: usageFor(repository.format, repository.name, selectedReference),
    };
  });

  const toggleRow = (row: ConanTableRow) => {
    if (row.expanded) {
      onCollapse();
      onClearArtifactParams();
      return;
    }
    onExpand(row.key, row.selectedReference);
    onFilterChange("");
    if (!revisionPages[row.selectedReference])
      onLoadRevisions(row.selectedReference);
    onOpenArtifact(
      row.selectedReference,
      row.selectedRevisionValue || undefined,
    );
  };

  const columns: ColumnsType<ConanTableRow> = [
    {
      title: text("Conan 包", "Conan package"),
      dataIndex: "key",
      key: "key",
      width: 260,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-100">{value}</span>
      ),
    },
    {
      title: text("最新版本", "Latest version"),
      key: "latest",
      width: 160,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {conanReferenceParts(row.latest.coordinate).version}
        </span>
      ),
    },
    {
      title: text("版本数", "Versions"),
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-zinc-500">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: text("当前 revision", "Current revision"),
      key: "revision",
      width: 240,
      render: (_, row) => (
        <span
          className="block max-w-[220px] truncate font-mono text-xs text-zinc-500"
          title={row.selectedRevisionItem?.revision}
        >
          {row.selectedRevisionItem?.revision ??
            (row.expanded
              ? text("读取中…", "Loading…")
              : text("展开后加载", "Load when expanded"))}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 270,
      render: (_, row) => (
        <div className="whitespace-nowrap text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => toggleRow(row)}
          >
            {row.expanded
              ? text("收起", "Collapse")
              : text("选择版本", "Select version")}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={row.versionHref}
          >
            {row.selectedRevisionValue
              ? text("打开版本", "Open version")
              : text("打开", "Open")}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.key
                ? text("已复制", "Copied")
                : text("复制 Conan 包标识", "Copy Conan package identifier")
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={`${text("复制", "Copy")} ${row.key}`}
              icon={
                copiedCoordinate === row.key ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )
              }
              onClick={() => onCopyCoordinate(row.key)}
            />
          </Tooltip>
        </div>
      ),
    },
  ];

  const expandedRowRender = (row: ConanTableRow) => (
    <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
      <div>
        <div className="flex items-center justify-between gap-3">
          <label className="text-xs font-medium text-zinc-500">
            {text("选择包版本", "Select package version")}{" "}
            <span className="font-normal text-zinc-600">
              ({row.group.versions.length})
            </span>
          </label>
          <span className="text-xs text-zinc-600">{row.referenceVersion}</span>
        </div>
        <SearchableVersionSelect
          className="mt-1.5"
          value={row.selectedReference}
          options={row.group.versions.map((version) => ({
            value: version.coordinate,
            label: version.coordinate,
          }))}
          onChange={(reference) => {
            onSelectReference(row.key, reference);
            onFilterChange("");
            onLoadRevisions(reference);
            onOpenArtifact(reference);
          }}
          placeholder={text(
            "搜索并选择 Conan 包版本",
            "Search Conan package versions",
          )}
        />
        <p className="mt-2 text-xs leading-5 text-zinc-600">
          {text(
            "同一 name@user/channel 下收拢不同版本；选定版本后再查看 recipe revision。",
            "Versions are grouped under the same name@user/channel; select one to inspect its recipe revision.",
          )}
        </p>
      </div>
      <div className="min-w-0">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium text-zinc-500">
            Recipe revision
          </span>
          <span className="text-xs text-zinc-600">
            {row.visibleRevisions.length}/{row.revisions.length}
          </span>
        </div>
        <div className="mt-1.5 flex gap-2">
          <Input
            className="min-w-0 flex-1 font-mono text-xs"
            placeholder={text(
              "输入 revision 或 digest",
              "Enter revision or digest",
            )}
            value={versionFilter}
            onChange={(event) => onFilterChange(event.target.value)}
            onPressEnter={() =>
              onLoadRevisions(row.selectedReference, versionFilter)
            }
          />
          <Button
            loading={row.page?.loading === true}
            onClick={() =>
              onLoadRevisions(row.selectedReference, versionFilter)
            }
          >
            {text("搜索", "Search")}
          </Button>
        </div>
        {row.page?.error && (
          <div className="mt-2 text-xs text-rose-300">{row.page.error}</div>
        )}
        <SearchableVersionSelect
          className="mt-3"
          value={row.selectedRevisionValue}
          options={row.visibleRevisions.map((revision) => ({
            value: revision.revision,
            label: `${revision.revision} · ${shortDigest(revision.digest)}`,
          }))}
          loading={row.page?.loading === true}
          notFoundContent={
            row.page?.loading && row.visibleRevisions.length === 0
              ? text("正在读取 revision…", "Loading revisions…")
              : text("没有匹配 revision", "No matching revisions")
          }
          placeholder={text(
            "搜索并选择 recipe revision",
            "Search recipe revisions",
          )}
          onChange={(revision) => {
            onSelectRevision(row.selectedReference, revision);
            onOpenArtifact(row.selectedReference, revision);
          }}
        />
        {row.page?.nextPageToken && (
          <Button
            block
            size="small"
            loading={row.page.loading}
            onClick={() =>
              onLoadRevisions(
                row.selectedReference,
                row.page?.query,
                row.page?.nextPageToken,
              )
            }
            className="mt-2"
          >
            {row.page.loading
              ? text("加载中…", "Loading…")
              : text(
                  `再加载 ${VERSION_PAGE_SIZE} 个 revision`,
                  `Load ${VERSION_PAGE_SIZE} more revisions`,
                )}
          </Button>
        )}
        {row.selectedRevisionItem ? (
          <>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-zinc-100">
                {row.selectedReference}
              </span>
              <span className="rounded bg-violet-500/10 px-1.5 py-0.5 text-xs text-violet-300">
                {row.selectedRevisionItem.revision}
              </span>
              <Button
                type="link"
                size="small"
                icon={<LinkOutlined />}
                href={row.versionHref}
              >
                {text("打开版本页", "Open version")}
              </Button>
              <Button
                type="link"
                size="small"
                onClick={() => onCopyPageLink(row.versionHref)}
              >
                {copiedCoordinate === row.versionHref
                  ? text("链接已复制", "Link copied")
                  : text("复制链接", "Copy link")}
              </Button>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
              <MetadataItem
                label="Conan reference"
                value={row.selectedReference}
                mono
              />
              <MetadataItem
                label="Recipe revision"
                value={row.selectedRevisionItem.revision}
                mono
              />
              <MetadataItem
                label={text("发布时间", "Published")}
                value={formatDate(row.selectedRevisionItem.createdAt, locale)}
              />
              <MetadataItem
                label={text("发布者", "Publisher")}
                value={row.latest.publisher ?? text("未记录", "Not recorded")}
                mono
              />
              <MetadataItem
                label={text("校验摘要", "Digest")}
                value={
                  row.selectedRevisionItem.digest ??
                  text("未记录", "Not recorded")
                }
                mono
              />
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              {row.snippets.map((snippet) => (
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
          <div className="mt-3 rounded-md border border-dashed border-zinc-800 px-4 py-6 text-sm text-zinc-600">
            {text(
              "选择一个 recipe revision 查看详情与使用方式。",
              "Select a recipe revision to view details and usage.",
            )}
          </div>
        )}
      </div>
    </div>
  );

  return (
    <Table<ConanTableRow>
      className="ag-console-table"
      rowKey="key"
      size="middle"
      dataSource={tableRows}
      columns={columns}
      pagination={false}
      scroll={{ x: 1030 }}
      expandable={{
        expandedRowKeys: tableRows
          .filter((row) => row.expanded)
          .map((row) => row.key),
        expandedRowRender,
        showExpandColumn: false,
      }}
    />
  );
}

export function PublicBrowsePage() {
  const { locale, t, text } = usePreferences();
  const [params, setParams] = useSearchParams();
  const repositoryId = params.get("repository") ?? "";
  const query = params.get("q") ?? "";
  const artifactParam = params.get("artifact") ?? "";
  const buildParam = params.get("build") ?? "";
  const tagParam = params.get("tag") ?? "";
  const revisionParam = params.get("revision") ?? "";
  const versionParam = params.get("version") ?? "";
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
  const [catalogQuery, setCatalogQuery] = useState("");
  const [catalogFormat, setCatalogFormat] = useState<
    PublicRepositoryFormat | "all"
  >("all");
  const { copiedValue: copiedCoordinate, copy } = useClipboardAction(1400);
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
  const [ociTagPages, setOciTagPages] = useState<Record<string, OciTagPage>>(
    {},
  );
  const [ociManifestDetails, setOciManifestDetails] = useState<
    Record<string, OciManifestDetail>
  >({});
  const [conanRevisionPages, setConanRevisionPages] = useState<
    Record<string, ConanRevisionPage>
  >({});
  const selectedRepository = repositories?.find(
    (repository) => repository.id === repositoryId,
  );
  const publicFormats = useMemo(
    () =>
      PUBLIC_FORMAT_ORDER.filter((format) =>
        repositories?.some((repository) => repository.format === format),
      ),
    [repositories],
  );
  const visibleRepositories = useMemo(() => {
    const normalizedQuery = catalogQuery.trim().toLocaleLowerCase();
    return (repositories ?? []).filter((repository) => {
      if (catalogFormat !== "all" && repository.format !== catalogFormat) {
        return false;
      }
      if (!normalizedQuery) return true;
      return [repository.name, repository.format, repository.type ?? "hosted"]
        .join(" ")
        .toLocaleLowerCase()
        .includes(normalizedQuery);
    });
  }, [catalogFormat, catalogQuery, repositories]);

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
    if (!repositoryId || !items) return;
    const coordinate = missingDeepLinkedArtifact(items, artifactParam);
    if (!coordinate) return;

    let cancelled = false;
    void searchRepositoryArtifacts({
      path: { repositoryId },
      query: { q: coordinate, pageSize: 100 },
    }).then(({ data, error: requestError }) => {
      if (cancelled) return;
      if (requestError) {
        setError(requestError);
        return;
      }
      const matches = (data?.items ?? []).filter(
        (item) => item.coordinate === coordinate,
      );
      if (matches.length === 0) return;
      setItems((current) => [
        ...(current ?? []),
        ...matches.filter(
          (match) =>
            !(current ?? []).some(
              (item) =>
                item.coordinate === match.coordinate &&
                item.buildNumber === match.buildNumber,
            ),
        ),
      ]);
    });
    return () => {
      cancelled = true;
    };
  }, [artifactParam, items, repositoryId]);

  useEffect(() => {
    setOciTagPages({});
    setOciManifestDetails({});
    setConanRevisionPages({});
    setExpandedConanGroup(null);
    setSelectedConanReferences({});
  }, [repositoryId]);

  const loadOciManifest = useCallback(
    async (coordinate: string, tag: string) => {
      if (selectedRepository?.format !== "oci" || !tag) return;
      const key = `${coordinate}\x00${tag}`;
      setOciManifestDetails((current) => ({
        ...current,
        [key]: { ...(current[key] ?? {}), loading: true, error: undefined },
      }));
      try {
        const detail = await readOciManifestDetail(
          selectedRepository.name,
          coordinate,
          tag,
        );
        setOciManifestDetails((current) => ({
          ...current,
          [key]: { ...detail, loading: false },
        }));
      } catch (requestError) {
        setOciManifestDetails((current) => ({
          ...current,
          [key]: {
            ...(current[key] ?? {}),
            loading: false,
            error:
              requestError instanceof Error
                ? requestError.message
                : "读取 OCI manifest 失败",
          },
        }));
      }
    },
    [selectedRepository],
  );

  const loadOciTags = useCallback(
    async (coordinate: string, after = "") => {
      if (selectedRepository?.format !== "oci") return;
      setOciTagPages((current) => ({
        ...current,
        [coordinate]: {
          items: after ? (current[coordinate]?.items ?? []) : [],
          loaded: current[coordinate]?.loaded ?? false,
          loading: true,
        },
      }));
      const imagePath = coordinate.split("/").map(encodeURIComponent).join("/");
      try {
        if (selectedRepository.type !== "group") {
          const { data, error: requestError } = await listOciManifests({
            path: { repositoryId: selectedRepository.id },
            query: {
              name: coordinate,
              pageSize: VERSION_PAGE_SIZE,
              pageToken: after || undefined,
            },
          });
          if (requestError || !data) throw new Error("读取 OCI 版本失败");
          const page = data.items.flatMap((manifest) =>
            manifest.tags.length > 0 ? manifest.tags : [manifest.digest],
          );
          setOciTagPages((current) => ({
            ...current,
            [coordinate]: {
              items: after
                ? [...new Set([...(current[coordinate]?.items ?? []), ...page])]
                : page,
              nextCursor: data.nextPageToken,
              loaded: true,
              loading: false,
            },
          }));
          const selectedVersion =
            tagParam && artifactParam === coordinate ? tagParam : page[0];
          if (!after && selectedVersion)
            void loadOciManifest(coordinate, selectedVersion);
          return;
        }
        const query = new URLSearchParams({ n: String(VERSION_PAGE_SIZE) });
        if (after) query.set("last", after);
        const response = await fetch(
          `/v2/${encodeURIComponent(selectedRepository.name)}/${imagePath}/tags/list?${query}`,
        );
        if (!response.ok)
          throw new Error(`读取 OCI 标签失败 (${response.status})`);
        const data = (await response.json()) as { tags?: string[] };
        const page = data.tags ?? [];
        setOciTagPages((current) => ({
          ...current,
          [coordinate]: {
            items: after
              ? [...new Set([...(current[coordinate]?.items ?? []), ...page])]
              : page,
            nextCursor: nextOciTagCursor(response, page),
            loaded: true,
            loading: false,
          },
        }));
        const selectedTag =
          tagParam && artifactParam === coordinate ? tagParam : page[0];
        if (!after && selectedTag)
          void loadOciManifest(coordinate, selectedTag);
      } catch (requestError) {
        setOciTagPages((current) => ({
          ...current,
          [coordinate]: {
            items: current[coordinate]?.items ?? [],
            loaded: true,
            loading: false,
            error:
              requestError instanceof Error
                ? requestError.message
                : "读取 OCI 标签失败",
          },
        }));
      }
    },
    [artifactParam, loadOciManifest, selectedRepository, tagParam],
  );

  const loadConanRevisions = useCallback(
    async (coordinate: string, query = "", pageToken = "") => {
      if (selectedRepository?.format !== "conan") return;
      const normalizedQuery = query.trim();
      setConanRevisionPages((current) => ({
        ...current,
        [coordinate]: {
          items:
            pageToken && current[coordinate]?.query === normalizedQuery
              ? (current[coordinate]?.items ?? [])
              : [],
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
          items:
            pageToken && current[coordinate]?.query === normalizedQuery
              ? [...current[coordinate].items, ...data.items]
              : data.items,
          nextPageToken: data.nextPageToken,
          query: normalizedQuery,
          loaded: true,
          loading: false,
        },
      }));
    },
    [selectedRepository],
  );

  useEffect(() => {
    if (
      !artifactParam ||
      !items?.some((item) => item.coordinate === artifactParam)
    )
      return;
    if (selectedRepository?.format === "oci" && !ociTagPages[artifactParam]) {
      void loadOciTags(artifactParam);
    }
    if (
      selectedRepository?.format === "conan" &&
      !conanRevisionPages[artifactParam]
    ) {
      if (revisionParam) setProtocolVersionFilter(revisionParam);
      void loadConanRevisions(artifactParam, revisionParam);
    }
  }, [
    artifactParam,
    conanRevisionPages,
    items,
    loadConanRevisions,
    loadOciTags,
    ociTagPages,
    revisionParam,
    selectedRepository?.format,
  ]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!repositoryId) return;
    setParams({
      repository: repositoryId,
      ...(queryDraft.trim() ? { q: queryDraft.trim() } : {}),
    });
  };

  const globalUsage = selectedRepository
    ? repositoryUsage(selectedRepository.format, selectedRepository.name, text)
    : [];
  const groupedItems =
    selectedRepository?.format === "maven"
      ? mavenArtifactGroups(items ?? [])
      : null;
  const groupedConanItems =
    selectedRepository?.format === "conan"
      ? conanArtifactGroups(items ?? [])
      : null;

  const copyCoordinate = (coordinate: string) => copy(coordinate);

  const copyUsage = (snippet: UsageSnippet) => copy(snippet.code);

  const artifactHref = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    return artifactBrowsePath(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
  };

  const openArtifact = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    const next = artifactBrowseParams(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const clearArtifactParams = () => {
    const next = clearArtifactBrowseParams(params);
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const copyPageLink = (href: string) =>
    copy(new URL(href, window.location.origin).toString(), href);

  const usageLabel =
    selectedRepository?.format === "oci"
      ? text("注册 OCI 镜像源", "Register an OCI registry")
      : selectedRepository?.format === "maven"
        ? text("注册 Maven 仓库源", "Register a Maven repository")
        : selectedRepository?.format === "conan"
          ? text("注册 Conan remote", "Register a Conan remote")
          : selectedRepository?.format === "npm"
            ? text("注册 npm 仓库源", "Register an npm registry")
            : selectedRepository?.format === "pypi"
              ? text("注册 PyPI 仓库源", "Register a PyPI repository")
              : selectedRepository?.format === "go"
                ? text("注册 Go Module Proxy", "Register a Go module proxy")
                : selectedRepository?.format === "apt"
                  ? text("注册 APT 软件源", "Register an APT source")
                  : text("注册 Raw 源地址", "Register a Raw source");

  const artifactTableRows: PublicArtifactTableRow[] = (items ?? []).map(
    (item, index) => {
      const isOci = selectedRepository?.format === "oci";
      const expanded =
        expandedCoordinate === item.coordinate ||
        artifactParam === item.coordinate;
      const page = isOci ? ociTagPages[item.coordinate] : undefined;
      const tags = isOci ? (page?.items ?? []) : [];
      const protocolVersions: ProtocolVersion[] = isOci
        ? [
            ...new Set(
              tagParam &&
                artifactParam === item.coordinate &&
                !tags.includes(tagParam)
                ? [...tags, tagParam]
                : tags,
            ),
          ].map((tag) => ({
            value: tag,
            label: tag.startsWith("sha256:")
              ? `无标签 · ${shortDigest(tag)}`
              : tag,
            searchText: tag,
            digest: tag.startsWith("sha256:") ? tag : undefined,
          }))
        : [];
      const protocolVersionsLoaded = isOci ? page?.loaded === true : true;
      const protocolVersionsLoading = isOci ? page?.loading === true : false;
      const protocolVersionsError = isOci ? page?.error : undefined;
      const nextProtocolPage = isOci ? page?.nextCursor : undefined;
      const preferredProtocolVersion =
        (selectedProtocolVersions[item.coordinate] &&
          protocolVersions.some(
            (version) =>
              version.value === selectedProtocolVersions[item.coordinate],
          ) &&
          selectedProtocolVersions[item.coordinate]) ||
        (tagParam &&
          artifactParam === item.coordinate &&
          protocolVersions.some((version) => version.value === tagParam) &&
          tagParam) ||
        protocolVersions[0]?.value ||
        "";
      const selectedProtocolVersionValue = protocolVersions.some(
        (version) => version.value === preferredProtocolVersion,
      )
        ? preferredProtocolVersion
        : protocolVersions[0]?.value || preferredProtocolVersion;
      const selectedProtocolVersionItem = protocolVersions.find(
        (version) => version.value === selectedProtocolVersionValue,
      );
      const selectedTag = isOci
        ? selectedProtocolVersionItem?.value
        : undefined;
      const ociDetail =
        isOci && selectedTag
          ? ociManifestDetails[`${item.coordinate}\x00${selectedTag}`]
          : undefined;
      return {
        key: `${item.coordinate}-${index}`,
        item,
        isOci,
        expanded,
        page,
        protocolVersions,
        protocolVersionsLoaded,
        protocolVersionsLoading,
        protocolVersionsError,
        nextProtocolPage,
        selectedProtocolVersionValue,
        selectedProtocolVersionItem,
        selectedTag,
        ociDetail,
        selectedProtocolHref: artifactHref(
          item.coordinate,
          undefined,
          selectedTag,
        ),
        snippets: selectedRepository
          ? usageFor(
              selectedRepository.format,
              selectedRepository.name,
              item.coordinate,
              selectedTag,
            )
          : [],
      };
    },
  );

  const toggleArtifactRow = (row: PublicArtifactTableRow) => {
    if (row.expanded) {
      setExpandedCoordinate(null);
      clearArtifactParams();
      return;
    }
    setExpandedCoordinate(row.item.coordinate);
    setProtocolVersionFilter("");
    if (row.isOci && !row.page) void loadOciTags(row.item.coordinate);
    if (row.selectedProtocolVersionValue) {
      setSelectedProtocolVersions((current) => ({
        ...current,
        [row.item.coordinate]: row.selectedProtocolVersionValue,
      }));
    }
  };

  const artifactColumns: ColumnsType<PublicArtifactTableRow> = [
    {
      title:
        selectedRepository?.format === "oci"
          ? text("镜像", "Image")
          : selectedRepository?.format === "apt"
            ? text("APT 路径", "APT path")
            : text("制品坐标", "Artifact coordinate"),
      key: "coordinate",
      width: 320,
      render: (_, row) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-zinc-100"
          title={row.item.coordinate}
        >
          {row.item.coordinate}
        </span>
      ),
    },
    ...(selectedRepository?.format === "oci"
      ? [
          {
            title: text("已加载版本", "Loaded versions"),
            key: "loadedVersions",
            width: 130,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-zinc-500">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? text("读取中…", "Loading…")
                  : !row.protocolVersionsLoaded
                    ? text("展开后加载", "Load when expanded")
                    : row.protocolVersions.length > 0
                      ? text(
                          `${row.protocolVersions.length}${row.nextProtocolPage ? "+" : ""} 个`,
                          `${row.protocolVersions.length}${row.nextProtocolPage ? "+" : ""}`,
                        )
                      : "—"}
              </span>
            ),
          },
          {
            title: text("当前版本", "Selected version"),
            key: "selectedVersion",
            width: 190,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="block max-w-[180px] truncate font-mono text-xs text-zinc-500">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? text("读取中…", "Loading…")
                  : !row.protocolVersionsLoaded
                    ? "—"
                    : (row.selectedProtocolVersionItem?.label ?? "—")}
              </span>
            ),
          },
          {
            title: text("镜像摘要", "Image digest"),
            key: "digest",
            width: 160,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="font-mono text-xs text-zinc-500">
                {shortDigest(
                  row.ociDetail?.digest ??
                    row.selectedProtocolVersionItem?.digest ??
                    row.item.digest,
                )}
              </span>
            ),
          },
        ]
      : [
          {
            title: text("摘要", "Digest"),
            key: "digest",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="font-mono text-xs text-zinc-500">
                {shortDigest(row.item.digest)}
              </span>
            ),
          },
          {
            title: text("大小", "Size"),
            key: "size",
            width: 120,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="text-xs text-zinc-400">
                {formatBytes(row.item.size)}
              </span>
            ),
          },
          ...(selectedRepository?.format === "apt"
            ? [
                {
                  title: text("内容类型", "Content type"),
                  key: "contentType",
                  width: 220,
                  render: (_: unknown, row: PublicArtifactTableRow) => (
                    <span className="block max-w-[210px] truncate font-mono text-xs text-zinc-500">
                      {row.item.contentType ?? "—"}
                    </span>
                  ),
                },
              ]
            : []),
          {
            title:
              selectedRepository?.format === "apt"
                ? text("首次缓存", "First cached")
                : text("创建时间", "Created"),
            key: "createdAt",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-zinc-500">
                {formatDate(row.item.createdAt, locale)}
              </span>
            ),
          },
        ]),
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 230,
      render: (_, row) => (
        <div className="whitespace-nowrap text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => toggleArtifactRow(row)}
          >
            {row.expanded
              ? text("收起", "Collapse")
              : row.isOci
                ? text("选择版本", "Select version")
                : text("使用方式", "Usage")}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={
              row.isOci
                ? row.selectedProtocolHref
                : artifactHref(
                    row.item.coordinate,
                    undefined,
                    undefined,
                    undefined,
                    selectedRepository?.format === "npm" ||
                      selectedRepository?.format === "pypi" ||
                      selectedRepository?.format === "go"
                      ? (row.item.version ?? versionParam)
                      : undefined,
                  )
            }
          >
            {row.isOci && row.selectedProtocolVersionItem
              ? text("打开版本", "Open version")
              : text("打开", "Open")}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.item.coordinate
                ? text("已复制", "Copied")
                : text("复制制品坐标", "Copy coordinate")
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={text(
                `复制 ${row.item.coordinate}`,
                `Copy ${row.item.coordinate}`,
              )}
              onClick={() => void copyCoordinate(row.item.coordinate)}
              icon={
                copiedCoordinate === row.item.coordinate ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )
              }
            />
          </Tooltip>
        </div>
      ),
    },
  ];

  const expandedArtifactRowRender = (row: PublicArtifactTableRow) => {
    if (selectedRepository?.format === "npm") {
      return (
        <NpmPackageDetail
          repoName={selectedRepository.name}
          packageName={row.item.coordinate}
          initialVersion={
            artifactParam === row.item.coordinate
              ? versionParam || row.item.version
              : row.item.version
          }
          size={row.item.size}
          publisher={row.item.publisher}
          onVersionChange={(version) =>
            openArtifact(
              row.item.coordinate,
              undefined,
              undefined,
              undefined,
              version,
            )
          }
        />
      );
    }
    if (selectedRepository?.format === "pypi") {
      return (
        <PyPIProjectDetail
          repoName={selectedRepository.name}
          project={row.item.coordinate}
          initialVersion={
            artifactParam === row.item.coordinate
              ? versionParam || row.item.version
              : row.item.version
          }
          size={row.item.size}
          publisher={row.item.publisher}
          onVersionChange={(version) =>
            openArtifact(
              row.item.coordinate,
              undefined,
              undefined,
              undefined,
              version,
            )
          }
        />
      );
    }
    if (selectedRepository?.format === "go") {
      return (
        <GoModuleDetail
          repoName={selectedRepository.name}
          modulePath={row.item.coordinate}
          initialVersion={
            artifactParam === row.item.coordinate
              ? versionParam || row.item.version
              : row.item.version
          }
          size={row.item.size}
          publisher={row.item.publisher}
          onVersionChange={(version) =>
            openArtifact(
              row.item.coordinate,
              undefined,
              undefined,
              undefined,
              version,
            )
          }
        />
      );
    }
    if (selectedRepository?.format === "apt") {
      return (
        <APTAssetDetail
          repoName={selectedRepository.name}
          meta={{
            coordinate: row.item.coordinate,
            digest: row.item.digest,
            size: row.item.size,
            contentType: row.item.contentType,
            createdAt: row.item.createdAt,
            cachedAt: row.item.cachedAt,
            sourceUrl: row.item.sourceUrl,
          }}
        />
      );
    }
    if (!row.isOci) {
      return (
        <div className="px-2 py-1">
          <div className="mb-4 grid grid-cols-2 gap-x-4 gap-y-3 border-b border-zinc-800/80 pb-4 text-xs sm:grid-cols-4">
            <MetadataItem
              label={text("仓库", "Repository")}
              value={selectedRepository?.name ?? "—"}
              mono
            />
            <MetadataItem
              label={text("制品坐标", "Artifact coordinate")}
              value={row.item.coordinate}
              mono
            />
            <MetadataItem
              label={text("发布时间", "Published")}
              value={formatDate(row.item.createdAt, locale)}
            />
            <MetadataItem
              label={text("发布者", "Publisher")}
              value={row.item.publisher ?? text("未记录", "Not recorded")}
              mono
            />
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            {row.snippets.map((snippet) => (
              <UsageSnippetBlock
                key={snippet.label}
                snippet={snippet}
                copied={copiedCoordinate === snippet.code}
                onCopy={() => void copyUsage(snippet)}
              />
            ))}
          </div>
        </div>
      );
    }
    return (
      <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
        <div>
          <div className="flex items-center justify-between gap-3">
            <span className="block text-xs font-medium text-zinc-500">
              {text("选择镜像版本", "Select image version")}
            </span>
            <span className="text-xs text-zinc-600">
              {text("已加载", "Loaded")} {row.protocolVersions.length}
            </span>
          </div>
          {row.protocolVersionsError && (
            <div className="mt-2 flex items-center justify-between gap-2 text-xs text-rose-300">
              <span>{row.protocolVersionsError}</span>
              <Button
                type="link"
                size="small"
                icon={<ReloadOutlined />}
                onClick={() => void loadOciTags(row.item.coordinate)}
              >
                {text("重试", "Retry")}
              </Button>
            </div>
          )}
          <SearchableVersionSelect
            className="mt-1.5"
            value={row.selectedProtocolVersionValue}
            options={row.protocolVersions.map((version) => ({
              value: version.value,
              label: version.label,
            }))}
            loading={row.protocolVersionsLoading}
            notFoundContent={
              row.protocolVersionsLoading && row.protocolVersions.length === 0
                ? text("正在读取版本…", "Loading versions…")
                : text("没有匹配版本", "No matching versions")
            }
            placeholder={text("搜索并选择镜像版本", "Search image versions")}
            onChange={(value) => {
              setSelectedProtocolVersions((current) => ({
                ...current,
                [row.item.coordinate]: value,
              }));
              void loadOciManifest(row.item.coordinate, value);
              openArtifact(row.item.coordinate, undefined, value);
            }}
          />
          {row.nextProtocolPage && (
            <Button
              block
              size="small"
              loading={row.protocolVersionsLoading}
              onClick={() =>
                void loadOciTags(row.item.coordinate, row.nextProtocolPage)
              }
              className="mt-2"
            >
              {row.protocolVersionsLoading
                ? text("加载中…", "Loading…")
                : text(
                    `再加载 ${VERSION_PAGE_SIZE} 个版本`,
                    `Load ${VERSION_PAGE_SIZE} more versions`,
                  )}
            </Button>
          )}
          <p className="mt-2 text-xs leading-5 text-zinc-600">
            {text(
              `每次最多读取 ${VERSION_PAGE_SIZE} 个版本；选择后可查看详情与使用方式。`,
              `Up to ${VERSION_PAGE_SIZE} versions are loaded at a time; select one to view details and usage.`,
            )}
          </p>
        </div>
        <div className="min-w-0">
          {row.selectedProtocolVersionItem ? (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs text-zinc-100">
                  {row.item.coordinate}
                </span>
                <span className="rounded bg-cyan-500/10 px-1.5 py-0.5 text-xs text-cyan-300">
                  {row.selectedProtocolVersionItem.label}
                </span>
                <Button
                  type="link"
                  size="small"
                  icon={<LinkOutlined />}
                  href={row.selectedProtocolHref}
                >
                  {text("打开版本页", "Open version")}
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => void copyPageLink(row.selectedProtocolHref)}
                >
                  {copiedCoordinate === row.selectedProtocolHref
                    ? text("链接已复制", "Link copied")
                    : text("复制链接", "Copy link")}
                </Button>
              </div>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
                <MetadataItem
                  label={text("镜像", "Image")}
                  value={row.item.coordinate}
                  mono
                />
                <MetadataItem
                  label={text("版本", "Version")}
                  value={row.selectedProtocolVersionItem.value}
                  mono
                />
                <MetadataItem
                  label={text("发布时间", "Published")}
                  value={
                    row.ociDetail?.loading
                      ? text("读取中…", "Loading…")
                      : formatDate(
                          row.ociDetail?.createdAt ?? row.item.createdAt,
                          locale,
                        )
                  }
                />
                <MetadataItem
                  label={text("发布者", "Publisher")}
                  value={
                    row.ociDetail?.publisher ??
                    row.item.publisher ??
                    text("未记录", "Not recorded")
                  }
                  mono
                />
                <MetadataItem
                  label={text("校验摘要", "Digest")}
                  value={
                    row.ociDetail?.loading
                      ? text("读取中…", "Loading…")
                      : (row.ociDetail?.digest ??
                        row.selectedProtocolVersionItem.digest ??
                        row.item.digest ??
                        text("未记录", "Not recorded"))
                  }
                  mono
                />
                <MetadataItem
                  label={text("镜像大小", "Image size")}
                  value={
                    row.ociDetail?.loading
                      ? text("读取中…", "Loading…")
                      : formatBytes(row.ociDetail?.size ?? row.item.size)
                  }
                />
              </div>
              {row.ociDetail?.error && (
                <div className="mt-2 flex items-center gap-2 text-xs text-rose-300">
                  <span>{row.ociDetail.error}</span>
                  <Button
                    type="link"
                    size="small"
                    icon={<ReloadOutlined />}
                    onClick={() =>
                      row.selectedTag &&
                      void loadOciManifest(row.item.coordinate, row.selectedTag)
                    }
                  >
                    {text("重试", "Retry")}
                  </Button>
                </div>
              )}
              <div className="mt-3 grid gap-3 sm:grid-cols-2">
                {row.snippets.map((snippet) => (
                  <UsageSnippetBlock
                    key={snippet.label}
                    snippet={snippet}
                    copied={copiedCoordinate === snippet.code}
                    onCopy={() => void copyUsage(snippet)}
                  />
                ))}
              </div>
            </>
          ) : (
            <div className="rounded-md border border-dashed border-zinc-800 px-4 py-6 text-sm text-zinc-600">
              {text(
                "版本加载完成后，可在左侧搜索并选择一个版本。",
                "Once versions load, search and select one on the left.",
              )}
            </div>
          )}
        </div>
      </div>
    );
  };

  return (
    <main className="ag-login-shell min-h-screen px-4 py-6 text-zinc-200 sm:px-6 sm:py-8">
      <div className="mx-auto max-w-7xl">
        <div className="ag-public-browse-header mb-6 flex flex-wrap items-center justify-between gap-4">
          <div className="flex min-w-0 flex-1 items-center gap-3">
            <SiteBrandMark className="flex size-10 shrink-0 items-center justify-center rounded-xl text-sm font-bold text-white" />
            <div className="min-w-0">
              <div className="break-words font-semibold tracking-tight text-zinc-100">
                <SiteName />
              </div>
              <div className="mt-0.5 text-xs text-zinc-500">
                {t("public.browseTitle")}
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-3">
            <PreferenceControls compact />
            <Link
              to="/login"
              className="text-sm text-zinc-400 hover:text-cyan-300"
            >
              {t("public.managementLogin")}
            </Link>
          </div>
        </div>
        {!repositoryId ? (
          <Card
            className="overflow-hidden border-zinc-800/90 bg-zinc-950/35"
            bodyClassName="p-6 sm:p-8 lg:p-10"
          >
            <div className="grid gap-7 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-end">
              <div className="max-w-3xl">
                <div className="inline-flex items-center gap-2 rounded-full border border-emerald-300/20 bg-emerald-300/10 px-3 py-1 text-xs font-medium text-emerald-200">
                  <SafetyCertificateOutlined />
                  {text("公开只读", "Public read-only")}
                </div>
                <h1 className="mt-4 text-2xl font-semibold tracking-tight text-zinc-50">
                  {text(
                    "查找并使用可信的公开制品",
                    "Discover and consume trusted public artifacts",
                  )}
                </h1>
                <p className="mt-3 max-w-2xl text-sm leading-6 text-zinc-400">
                  {text(
                    "从团队明确公开的仓库中检索镜像、依赖和软件包。读取无需登录，发布、授权和仓库管理始终需要身份认证。",
                    "Search images, dependencies, and packages from repositories explicitly published by your team. Reading needs no sign-in; publishing, grants, and repository administration always require authentication.",
                  )}
                </p>
                <Input
                  allowClear
                  size="large"
                  prefix={<SearchOutlined className="text-zinc-500" />}
                  className="mt-6 max-w-2xl"
                  placeholder={text(
                    "搜索仓库名称或格式",
                    "Search repository name or format",
                  )}
                  value={catalogQuery}
                  onChange={(event) => setCatalogQuery(event.target.value)}
                />
              </div>
              <div className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-white/10 bg-white/10 shadow-2xl shadow-black/20">
                <div className="bg-zinc-950/70 p-4">
                  <div className="text-2xl font-semibold text-zinc-50">
                    {repositories?.length ?? "—"}
                  </div>
                  <div className="mt-1 text-xs text-zinc-500">
                    {text("个公开来源", "public sources")}
                  </div>
                </div>
                <div className="bg-zinc-950/70 p-4">
                  <div className="text-2xl font-semibold text-zinc-50">
                    {publicFormats.length || "—"}
                  </div>
                  <div className="mt-1 text-xs text-zinc-500">
                    {text("种制品格式", "artifact formats")}
                  </div>
                </div>
                <div className="col-span-2 flex items-center gap-3 bg-zinc-950/70 p-4">
                  <div className="flex size-9 items-center justify-center rounded-lg bg-cyan-400/10 text-cyan-200">
                    <SafetyCertificateOutlined />
                  </div>
                  <div>
                    <div className="text-sm font-medium text-zinc-200">
                      {text(
                        "管理操作需要登录",
                        "Management actions require sign-in",
                      )}
                    </div>
                    <div className="mt-0.5 text-xs text-zinc-500">
                      {text(
                        "公开目录不会授予写入或管理权限",
                        "The public catalog never grants write or admin access",
                      )}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </Card>
        ) : (
          <Card bodyClassName="p-5 sm:p-6">
            <div className="flex flex-wrap items-center gap-2 text-sm text-zinc-300">
              <Button
                type="text"
                size="small"
                icon={<ArrowLeftOutlined />}
                onClick={() => setParams({})}
              >
                {text("公开仓库", "Public repositories")}
              </Button>
              <span className="text-zinc-700">/</span>
              <span className="font-medium text-zinc-100">
                {selectedRepository?.name ??
                  text("未知仓库", "Unknown repository")}
              </span>
              {selectedRepository && (
                <FormatBadge format={selectedRepository.format} />
              )}
            </div>
            <form onSubmit={submit} className="mt-5 flex items-center gap-2">
              <Input
                className="min-w-0 flex-1 font-mono text-xs"
                placeholder={text(
                  "坐标、路径或名称前缀（可选）",
                  "Coordinate, path, or name prefix (optional)",
                )}
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
              />
              <Button
                type="primary"
                htmlType="submit"
                className="shrink-0 whitespace-nowrap"
              >
                {text("搜索", "Search")}
              </Button>
            </form>
          </Card>
        )}
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
              <Loading
                label={text(
                  "正在读取公开仓库…",
                  "Loading public repositories…",
                )}
              />
            ) : anonymousEnabled === false ? (
              <EmptyState
                title={text(
                  "全局匿名读取未启用",
                  "Global anonymous reads are disabled",
                )}
                hint={text(
                  "请管理员在访问控制中启用全局匿名读取，仓库的匿名读取设置才会生效。",
                  "An administrator must enable global anonymous reads before repository settings take effect.",
                )}
                action={
                  <Link
                    to="/access"
                    className="text-sm text-cyan-300 hover:text-cyan-200"
                  >
                    {text("前往访问控制", "Open access control")}
                  </Link>
                }
              />
            ) : repositoryId && !selectedRepository ? (
              <EmptyState
                title={text(
                  "公开仓库不存在或不可见",
                  "Public repository not found or visible",
                )}
                hint={text(
                  "返回公开仓库目录，选择一个已启用匿名读取的仓库。",
                  "Return to the catalog and choose a repository with anonymous reads enabled.",
                )}
                action={
                  <Button type="primary" onClick={() => setParams({})}>
                    {text("返回目录", "Back to catalog")}
                  </Button>
                }
              />
            ) : !repositoryId ? (
              repositories.length === 0 ? (
                <EmptyState
                  title={text("暂无公开仓库", "No public repositories")}
                  hint={text(
                    "管理员需先启用全局匿名访问，并在仓库上允许匿名读取。",
                    "An administrator must enable global anonymous access and allow reads on a repository.",
                  )}
                  image={
                    <EmptyStateArtwork
                      darkSrc={emptyPublicCatalogDark}
                      lightSrc={emptyPublicCatalogLight}
                      name="public-catalog"
                    />
                  }
                  action={
                    <Link
                      to="/login"
                      className="text-sm font-medium text-cyan-300 hover:text-cyan-200"
                    >
                      {t("public.managementLogin")}
                    </Link>
                  }
                />
              ) : (
                <div>
                  <div className="mb-4 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
                    <div>
                      <h2 className="text-lg font-semibold text-zinc-100">
                        {text("选择制品来源", "Choose an artifact source")}
                      </h2>
                      <p className="mt-1 text-sm text-zinc-500">
                        {text(
                          `${repositories.length} 个公开来源 · ${publicFormats.length} 种制品格式`,
                          `${repositories.length} public sources · ${publicFormats.length} artifact formats`,
                        )}
                      </p>
                    </div>
                    <div
                      className="flex max-w-full gap-1 overflow-x-auto rounded-lg border border-zinc-800 bg-zinc-950/40 p-1"
                      aria-label={text(
                        "按制品格式筛选",
                        "Filter by artifact format",
                      )}
                    >
                      <Button
                        size="small"
                        type={catalogFormat === "all" ? "primary" : "text"}
                        aria-pressed={catalogFormat === "all"}
                        onClick={() => setCatalogFormat("all")}
                      >
                        {text("全部", "All")}
                      </Button>
                      {publicFormats.map((format) => (
                        <Button
                          key={format}
                          size="small"
                          type={catalogFormat === format ? "primary" : "text"}
                          aria-pressed={catalogFormat === format}
                          onClick={() => setCatalogFormat(format)}
                        >
                          {format.toUpperCase()}
                        </Button>
                      ))}
                    </div>
                  </div>
                  {visibleRepositories.length === 0 ? (
                    <EmptyState
                      compact
                      title={text(
                        "没有匹配的公开仓库",
                        "No matching public repositories",
                      )}
                      hint={text(
                        "调整搜索词或格式筛选后重试。",
                        "Adjust the search term or format filter and try again.",
                      )}
                      action={
                        <Button
                          onClick={() => {
                            setCatalogQuery("");
                            setCatalogFormat("all");
                          }}
                        >
                          {text("清除筛选", "Clear filters")}
                        </Button>
                      }
                      image={
                        <EmptyStateArtwork
                          darkSrc={emptyPublicCatalogDark}
                          lightSrc={emptyPublicCatalogLight}
                          name="public-catalog"
                        />
                      }
                    />
                  ) : (
                    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                      {visibleRepositories.map((repository) => (
                        <Link
                          key={repository.id}
                          to={`/browse?repository=${encodeURIComponent(repository.id)}`}
                          className="group block text-left"
                        >
                          <Card
                            className="ag-public-repository-card h-full border-zinc-800/90 bg-zinc-950/35 group-hover:border-cyan-400/35 group-hover:bg-zinc-900/80"
                            bodyClassName="flex h-full min-h-52 flex-col p-5"
                          >
                            <div className="flex items-start justify-between gap-3">
                              <div
                                className={`flex h-11 min-w-11 items-center justify-center rounded-xl border px-2 text-xs font-semibold tracking-wide ${PUBLIC_FORMAT_STYLE[repository.format].surface}`}
                              >
                                {PUBLIC_FORMAT_STYLE[repository.format].icon}
                              </div>
                              <div className="flex items-center gap-2">
                                <Badge
                                  tone={
                                    repository.type === "proxy"
                                      ? "amber"
                                      : repository.type === "group"
                                        ? "violet"
                                        : "cyan"
                                  }
                                >
                                  {(repository.type ?? "hosted").toUpperCase()}
                                </Badge>
                                <FormatBadge format={repository.format} />
                              </div>
                            </div>
                            <div className="mt-5 min-w-0 truncate text-base font-semibold text-zinc-100">
                              {repository.name}
                            </div>
                            <p className="mt-2 text-sm leading-5 text-zinc-500">
                              {repository.type === "proxy"
                                ? text(
                                    "缓存并加速经过管理员信任的上游制品。",
                                    "Caches and accelerates artifacts from an administrator-trusted upstream.",
                                  )
                                : repository.type === "group"
                                  ? text(
                                      "通过一个稳定入口聚合多个公开仓库。",
                                      "Aggregates multiple public repositories behind one stable endpoint.",
                                    )
                                  : text(
                                      "由团队直接发布和维护的公开制品。",
                                      "Public artifacts published and maintained directly by the team.",
                                    )}
                            </p>
                            <div className="mt-auto flex items-center justify-between pt-5 text-xs">
                              <span className="text-zinc-600">
                                {text("无需登录即可读取", "No sign-in to read")}
                              </span>
                              <span className="flex items-center gap-1 font-medium text-cyan-300 transition-colors group-hover:text-cyan-200">
                                {text("进入仓库", "Open repository")}
                                <ArrowRightOutlined />
                              </span>
                            </div>
                          </Card>
                        </Link>
                      ))}
                    </div>
                  )}
                </div>
              )
            ) : error ? (
              <ErrorBanner error={error} />
            ) : !items ? (
              <Loading
                label={text("正在读取公开制品…", "Loading public artifacts…")}
              />
            ) : items.length === 0 ? (
              <EmptyState
                compact
                title={text(
                  "没有匹配的公开制品",
                  "No matching public artifacts",
                )}
                hint={text(
                  "确认仓库已启用匿名读取，或调整查询条件。",
                  "Confirm anonymous reads are enabled or adjust the query.",
                )}
                image={
                  <EmptyStateArtwork
                    darkSrc={emptyPublicCatalogDark}
                    lightSrc={emptyPublicCatalogLight}
                    name="public-catalog"
                  />
                }
              />
            ) : (
              <Card>
                <div className="flex items-center justify-between border-b border-zinc-800/80 px-4 py-3">
                  <div className="text-xs text-zinc-500">
                    {text("找到", "Found")}{" "}
                    <span className="font-medium text-zinc-300">
                      {groupedItems?.length ??
                        groupedConanItems?.length ??
                        items.length}
                    </span>{" "}
                    {text("个制品", "artifacts")}
                    {(groupedItems || groupedConanItems) && (
                      <span className="ml-1 text-zinc-600">
                        {text(
                          `（${items.length} 个版本）`,
                          ` (${items.length} versions)`,
                        )}
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-zinc-600">
                    {text("匿名只读", "Anonymous read-only")}
                  </div>
                </div>
                {groupedItems && selectedRepository ? (
                  <MavenGroupTable
                    groups={groupedItems}
                    repository={selectedRepository}
                    artifactParam={artifactParam}
                    buildParam={buildParam}
                    expandedGroup={expandedMavenGroup}
                    selectedVersionKey={selectedMavenVersion}
                    copiedCoordinate={copiedCoordinate}
                    onExpand={(groupKey, versionKey) => {
                      setExpandedMavenGroup(groupKey);
                      setSelectedMavenVersion(versionKey);
                    }}
                    onCollapse={() => {
                      setExpandedMavenGroup(null);
                      clearArtifactParams();
                    }}
                    onSelectVersion={(group, versionKey) => {
                      setSelectedMavenVersion(versionKey);
                      const version = group.versions.find(
                        (item, index) =>
                          mavenVersionKey(item, index) === versionKey,
                      );
                      if (version)
                        openArtifact(version.coordinate, version.buildNumber);
                    }}
                    artifactHref={artifactHref}
                    onCopyPageLink={(href) => void copyPageLink(href)}
                    onCopyUsage={(snippet) => void copyUsage(snippet)}
                  />
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
                      setSelectedConanReferences((current) => ({
                        ...current,
                        [key]: reference,
                      }));
                    }}
                    onCollapse={() => setExpandedConanGroup(null)}
                    onSelectReference={(key, reference) =>
                      setSelectedConanReferences((current) => ({
                        ...current,
                        [key]: reference,
                      }))
                    }
                    onSelectRevision={(reference, revision) =>
                      setSelectedProtocolVersions((current) => ({
                        ...current,
                        [reference]: revision,
                      }))
                    }
                    onFilterChange={setProtocolVersionFilter}
                    onLoadRevisions={(reference, filter, pageToken) =>
                      void loadConanRevisions(reference, filter, pageToken)
                    }
                    onOpenArtifact={(reference, revision) =>
                      openArtifact(reference, undefined, undefined, revision)
                    }
                    onClearArtifactParams={clearArtifactParams}
                    artifactHref={(reference, revision) =>
                      artifactHref(reference, undefined, undefined, revision)
                    }
                    onCopyCoordinate={(value) => void copyCoordinate(value)}
                    onCopyPageLink={(value) => void copyPageLink(value)}
                    onCopyUsage={(snippet) => void copyUsage(snippet)}
                  />
                ) : (
                  <Table<PublicArtifactTableRow>
                    className="ag-console-table"
                    rowKey="key"
                    size="middle"
                    dataSource={artifactTableRows}
                    columns={artifactColumns}
                    pagination={false}
                    scroll={{
                      x: selectedRepository?.format === "oci" ? 1100 : 900,
                    }}
                    expandable={{
                      expandedRowKeys: artifactTableRows
                        .filter((row) => row.expanded)
                        .map((row) => row.key),
                      expandedRowRender: expandedArtifactRowRender,
                      showExpandColumn: false,
                    }}
                  />
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
                    {text("适用于仓库", "For repository")}{" "}
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
                  <div className="border-t border-zinc-800/80 pt-3 text-xs leading-5 text-zinc-600">
                    {text(
                      "匿名浏览无需 Token；推送、私有仓库和管理操作仍需登录。",
                      "Anonymous browsing needs no token. Publishing, private repositories, and management still require sign-in.",
                    )}
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
