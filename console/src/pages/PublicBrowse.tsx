import { useCallback, useEffect, useState } from "react";
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
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge, Badge } from "../components/Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usageFor, type UsageSnippet } from "../lib/usage";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../components/PublicBrowsePrimitives";

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

function conanReferenceParts(reference: string): {
  key: string;
  version: string;
} {
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
    if (!current.some((entry) => entry.coordinate === item.coordinate))
      current.push(item);
    groups.set(key, current);
  }
  return [...groups.entries()]
    .map(([key, versions]) => ({
      key,
      versions: versions.sort((a, b) => {
        const left = conanReferenceParts(a.coordinate).version;
        const right = conanReferenceParts(b.coordinate).version;
        return right.localeCompare(left, undefined, {
          numeric: true,
          sensitivity: "base",
        });
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
      title: "制品",
      dataIndex: "key",
      key: "key",
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-100">{value}</span>
      ),
    },
    {
      title: "最新版本",
      key: "latest",
      width: 180,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {row.latest.coordinate.split(":").slice(2).join(":")}
        </span>
      ),
    },
    {
      title: "版本数",
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-zinc-500">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: "",
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
            {row.expanded ? "收起" : "选择版本"}
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
          <label className="mb-1.5 block text-[11px] font-medium text-zinc-500">
            选择版本{" "}
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
            placeholder="搜索并选择 Maven 版本"
          />
          <p className="mt-2 text-[11px] leading-5 text-zinc-600">
            在选择器中输入版本号或 SNAPSHOT 构建号即可定位，不会铺开全部版本。
          </p>
        </div>
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs text-zinc-100">
              {row.selectedVersion.coordinate}
            </span>
            {row.selectedVersion.buildNumber ? (
              <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-300">
                SNAPSHOT #{row.selectedVersion.buildNumber}
              </span>
            ) : null}
            <Button
              type="link"
              size="small"
              icon={<LinkOutlined />}
              href={href}
            >
              打开版本页
            </Button>
            <Button
              type="link"
              size="small"
              onClick={() => onCopyPageLink(href)}
            >
              {copiedCoordinate === href ? "链接已复制" : "复制链接"}
            </Button>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 text-[11px] text-zinc-600">
            <span>{formatDate(row.selectedVersion.createdAt)}</span>
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
              label="发布时间"
              value={formatDate(row.selectedVersion.createdAt)}
            />
            <MetadataItem
              label="发布者"
              value={row.selectedVersion.publisher ?? "未记录"}
              mono
            />
            <MetadataItem
              label="校验摘要"
              value={row.selectedVersion.digest ?? "未记录"}
              mono
            />
            <MetadataItem
              label="构建类型"
              value={
                row.selectedVersion.buildNumber
                  ? `SNAPSHOT #${row.selectedVersion.buildNumber}`
                  : "Release"
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
      title: "Conan 包",
      dataIndex: "key",
      key: "key",
      width: 260,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-100">{value}</span>
      ),
    },
    {
      title: "最新版本",
      key: "latest",
      width: 160,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {conanReferenceParts(row.latest.coordinate).version}
        </span>
      ),
    },
    {
      title: "版本数",
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-zinc-500">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: "当前 revision",
      key: "revision",
      width: 240,
      render: (_, row) => (
        <span
          className="block max-w-[220px] truncate font-mono text-xs text-zinc-500"
          title={row.selectedRevisionItem?.revision}
        >
          {row.selectedRevisionItem?.revision ??
            (row.expanded ? "读取中…" : "展开后加载")}
        </span>
      ),
    },
    {
      title: "",
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
            {row.expanded ? "收起" : "选择版本"}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={row.versionHref}
          >
            {row.selectedRevisionValue ? "打开版本" : "打开"}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.key ? "已复制" : "复制 Conan 包标识"
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={`复制 ${row.key}`}
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
          <label className="text-[11px] font-medium text-zinc-500">
            选择包版本{" "}
            <span className="font-normal text-zinc-600">
              ({row.group.versions.length})
            </span>
          </label>
          <span className="text-[11px] text-zinc-600">
            {row.referenceVersion}
          </span>
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
          placeholder="搜索并选择 Conan 包版本"
        />
        <p className="mt-2 text-[11px] leading-5 text-zinc-600">
          同一 name@user/channel 下收拢不同版本；选定版本后再查看 recipe
          revision。
        </p>
      </div>
      <div className="min-w-0">
        <div className="flex items-center justify-between gap-3">
          <label className="text-[11px] font-medium text-zinc-500">
            Recipe revision
          </label>
          <span className="text-[11px] text-zinc-600">
            {row.visibleRevisions.length}/{row.revisions.length}
          </span>
        </div>
        <div className="mt-1.5 flex gap-2">
          <Input
            className="min-w-0 flex-1 font-mono text-xs"
            placeholder="输入 revision 或 digest"
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
            搜索
          </Button>
        </div>
        {row.page?.error && (
          <div className="mt-2 text-[11px] text-rose-300">{row.page.error}</div>
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
              ? "正在读取 revision…"
              : "没有匹配 revision"
          }
          placeholder="搜索并选择 recipe revision"
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
              ? "加载中…"
              : `再加载 ${VERSION_PAGE_SIZE} 个 revision`}
          </Button>
        )}
        {row.selectedRevisionItem ? (
          <>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-zinc-100">
                {row.selectedReference}
              </span>
              <span className="rounded bg-violet-500/10 px-1.5 py-0.5 text-[10px] text-violet-300">
                {row.selectedRevisionItem.revision}
              </span>
              <Button
                type="link"
                size="small"
                icon={<LinkOutlined />}
                href={row.versionHref}
              >
                打开版本页
              </Button>
              <Button
                type="link"
                size="small"
                onClick={() => onCopyPageLink(row.versionHref)}
              >
                {copiedCoordinate === row.versionHref
                  ? "链接已复制"
                  : "复制链接"}
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
                label="发布时间"
                value={formatDate(row.selectedRevisionItem.createdAt)}
              />
              <MetadataItem
                label="发布者"
                value={row.latest.publisher ?? "未记录"}
                mono
              />
              <MetadataItem
                label="校验摘要"
                value={row.selectedRevisionItem.digest ?? "未记录"}
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
            选择一个 recipe revision 查看详情与使用方式。
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
      title: selectedRepository?.format === "oci" ? "镜像" : "制品坐标",
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
            title: "已加载版本",
            key: "loadedVersions",
            width: 130,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-zinc-500">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? "读取中…"
                  : !row.protocolVersionsLoaded
                    ? "展开后加载"
                    : row.protocolVersions.length > 0
                      ? `${row.protocolVersions.length}${row.nextProtocolPage ? "+" : ""} 个`
                      : "—"}
              </span>
            ),
          },
          {
            title: "当前版本",
            key: "selectedVersion",
            width: 190,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="block max-w-[180px] truncate font-mono text-xs text-zinc-500">
                {row.protocolVersionsLoading && !row.protocolVersionsLoaded
                  ? "读取中…"
                  : !row.protocolVersionsLoaded
                    ? "—"
                    : (row.selectedProtocolVersionItem?.label ?? "—")}
              </span>
            ),
          },
          {
            title: "镜像摘要",
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
            title: "摘要",
            key: "digest",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="font-mono text-xs text-zinc-500">
                {shortDigest(row.item.digest)}
              </span>
            ),
          },
          {
            title: "大小",
            key: "size",
            width: 120,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="text-xs text-zinc-400">
                {formatBytes(row.item.size)}
              </span>
            ),
          },
          {
            title: "创建时间",
            key: "createdAt",
            width: 180,
            render: (_: unknown, row: PublicArtifactTableRow) => (
              <span className="whitespace-nowrap text-xs text-zinc-500">
                {formatDate(row.item.createdAt)}
              </span>
            ),
          },
        ]),
    {
      title: "",
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
            {row.expanded ? "收起" : row.isOci ? "选择版本" : "使用方式"}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={
              row.isOci
                ? row.selectedProtocolHref
                : artifactHref(row.item.coordinate)
            }
          >
            {row.isOci && row.selectedProtocolVersionItem ? "打开版本" : "打开"}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.item.coordinate
                ? "已复制"
                : "复制制品坐标"
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={`复制 ${row.item.coordinate}`}
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
    if (!row.isOci) {
      return (
        <div className="px-2 py-1">
          <div className="mb-4 grid grid-cols-2 gap-x-4 gap-y-3 border-b border-zinc-800/80 pb-4 text-xs sm:grid-cols-4">
            <MetadataItem
              label="仓库"
              value={selectedRepository?.name ?? "—"}
              mono
            />
            <MetadataItem label="制品坐标" value={row.item.coordinate} mono />
            <MetadataItem
              label="发布时间"
              value={formatDate(row.item.createdAt)}
            />
            <MetadataItem
              label="发布者"
              value={row.item.publisher ?? "未记录"}
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
            <label className="block text-[11px] font-medium text-zinc-500">
              选择镜像版本
            </label>
            <span className="text-[11px] text-zinc-600">
              已加载 {row.protocolVersions.length}
            </span>
          </div>
          {row.protocolVersionsError && (
            <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-rose-300">
              <span>{row.protocolVersionsError}</span>
              <Button
                type="link"
                size="small"
                icon={<ReloadOutlined />}
                onClick={() => void loadOciTags(row.item.coordinate)}
              >
                重试
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
                ? "正在读取版本…"
                : "没有匹配版本"
            }
            placeholder="搜索并选择镜像版本"
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
                ? "加载中…"
                : `再加载 ${VERSION_PAGE_SIZE} 个版本`}
            </Button>
          )}
          <p className="mt-2 text-[11px] leading-5 text-zinc-600">
            每次最多读取 {VERSION_PAGE_SIZE}{" "}
            个版本；选择后可查看详情与使用方式。
          </p>
        </div>
        <div className="min-w-0">
          {row.selectedProtocolVersionItem ? (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs text-zinc-100">
                  {row.item.coordinate}
                </span>
                <span className="rounded bg-cyan-500/10 px-1.5 py-0.5 text-[10px] text-cyan-300">
                  {row.selectedProtocolVersionItem.label}
                </span>
                <Button
                  type="link"
                  size="small"
                  icon={<LinkOutlined />}
                  href={row.selectedProtocolHref}
                >
                  打开版本页
                </Button>
                <Button
                  type="link"
                  size="small"
                  onClick={() => void copyPageLink(row.selectedProtocolHref)}
                >
                  {copiedCoordinate === row.selectedProtocolHref
                    ? "链接已复制"
                    : "复制链接"}
                </Button>
              </div>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
                <MetadataItem label="镜像" value={row.item.coordinate} mono />
                <MetadataItem
                  label="版本"
                  value={row.selectedProtocolVersionItem.value}
                  mono
                />
                <MetadataItem
                  label="发布时间"
                  value={
                    row.ociDetail?.loading
                      ? "读取中…"
                      : formatDate(
                          row.ociDetail?.createdAt ?? row.item.createdAt,
                        )
                  }
                />
                <MetadataItem
                  label="发布者"
                  value={
                    row.ociDetail?.publisher ?? row.item.publisher ?? "未记录"
                  }
                  mono
                />
                <MetadataItem
                  label="校验摘要"
                  value={
                    row.ociDetail?.loading
                      ? "读取中…"
                      : (row.ociDetail?.digest ??
                        row.selectedProtocolVersionItem.digest ??
                        row.item.digest ??
                        "未记录")
                  }
                  mono
                />
                <MetadataItem
                  label="镜像大小"
                  value={
                    row.ociDetail?.loading
                      ? "读取中…"
                      : formatBytes(row.ociDetail?.size ?? row.item.size)
                  }
                />
              </div>
              {row.ociDetail?.error && (
                <div className="mt-2 flex items-center gap-2 text-[11px] text-rose-300">
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
                    重试
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
              版本加载完成后，可在左侧搜索并选择一个版本。
            </div>
          )}
        </div>
      </div>
    );
  };

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
        <Card bodyClassName="p-5 sm:p-6">
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
              <Button
                type="link"
                size="small"
                icon={<ArrowLeftOutlined />}
                className="ml-auto"
                onClick={() => setParams({})}
              >
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
              <Button
                type="primary"
                htmlType="submit"
                className="shrink-0 whitespace-nowrap"
              >
                搜索
              </Button>
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
                      <Card
                        className="h-full border-zinc-800 transition-colors hover:border-cyan-500/50 hover:bg-zinc-900"
                        bodyClassName="px-4 py-4"
                      >
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
                      {groupedItems?.length ??
                        groupedConanItems?.length ??
                        items.length}
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
