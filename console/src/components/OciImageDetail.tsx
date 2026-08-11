import { useCallback, useEffect, useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm, Select, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listOciManifests } from "../client";
import type { OciManifestSummary } from "../client";
import { useAuth } from "../lib/auth";
import { Loading, ErrorBanner } from "./Feedback";
import { Badge } from "./Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { UsageSnippetBlock } from "./PublicBrowsePrimitives";
import { ociUsage, type UsageSnippet } from "../lib/usage";
import { usePreferences } from "../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";

interface OciDescriptor {
  mediaType: string;
  size?: number;
  digest: string;
}

interface OciManifest {
  schemaVersion: number;
  mediaType?: string;
  config?: OciDescriptor;
  layers?: OciDescriptor[];
}

interface OciConfig {
  architecture?: string;
  os?: string;
  created?: string;
  config?: {
    Entrypoint?: string[];
    Cmd?: string[];
    Env?: string[];
    User?: string;
    WorkingDir?: string;
  };
}

interface OciVersionOption {
  value: string;
  label: string;
  searchText: string;
  digest: string;
  size: number;
  kind: "tag" | "digest";
}

const MANIFEST_ACCEPT =
  "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json";

async function ociRegistryToken(token: string): Promise<string> {
  const response = await fetch("/auth/token", {
    credentials: "include",
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  if (!response.ok) {
    throw new Error(
      `${response.status}: ${(await response.text()).slice(0, 120)}`,
    );
  }
  const body = (await response.json()) as {
    token?: string;
    access_token?: string;
  };
  const registryToken = body.token ?? body.access_token;
  if (!registryToken) throw new Error("Registry token missing");
  return registryToken;
}

async function ociFetch(
  token: string,
  path: string,
  accept?: string,
): Promise<Response> {
  const registryToken = await ociRegistryToken(token);
  const res = await fetch(`/v2/${path}`, {
    headers: {
      Authorization: `Bearer ${registryToken}`,
      ...(accept ? { Accept: accept } : {}),
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${res.status}: ${text.slice(0, 120)}`);
  }
  return res;
}

async function fetchOciManifests(
  repositoryId: string,
  image: string,
): Promise<OciManifestSummary[]> {
  const items: OciManifestSummary[] = [];
  const seenTokens = new Set<string>();
  let pageToken: string | undefined;
  do {
    const response = await listOciManifests({
      path: { repositoryId },
      query: { name: image, pageSize: 200, pageToken },
    });
    if (response.error || !response.data)
      throw new Error("读取 OCI Manifest 列表失败");
    items.push(...response.data.items);
    pageToken = response.data.nextPageToken;
    if (pageToken && seenTokens.has(pageToken))
      throw new Error("OCI Manifest 分页游标重复");
    if (pageToken) seenTokens.add(pageToken);
  } while (pageToken);
  return items;
}

function ociVersionOptions(
  manifests: OciManifestSummary[],
): OciVersionOption[] {
  return manifests.flatMap<OciVersionOption>((manifest) => {
    if (manifest.tags.length === 0) {
      return [
        {
          value: manifest.digest,
          label: `无标签 · ${shortDigest(manifest.digest)}`,
          searchText: `无标签 ${manifest.digest}`,
          digest: manifest.digest,
          size: manifest.size,
          kind: "digest" as const,
        },
      ];
    }
    return manifest.tags.map((tag) => ({
      value: tag,
      label: `${tag} · ${shortDigest(manifest.digest)}`,
      searchText: `${tag} ${manifest.digest}`,
      digest: manifest.digest,
      size: manifest.size,
      kind: "tag" as const,
    }));
  });
}

export function OciImageDetail({
  repositoryId,
  repository,
  image,
  initialReference,
  onDeleted,
}: {
  repositoryId: string;
  repository: string;
  image: string;
  initialReference?: string;
  onDeleted?: () => void;
}) {
  const { token } = useAuth();
  const { locale, text } = usePreferences();
  const [manifests, setManifests] = useState<OciManifestSummary[] | null>(null);
  const [selectedReference, setSelectedReference] = useState<string | null>(
    null,
  );
  const [manifest, setManifest] = useState<OciManifest | null>(null);
  const [config, setConfig] = useState<OciConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [manifestLoading, setManifestLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const [copiedUsage, setCopiedUsage] = useState<string | null>(null);

  const name = `${repository}/${image}`;
  const versions = ociVersionOptions(manifests ?? []);
  const selectedVersion = versions.find(
    (version) => version.value === selectedReference,
  );

  const loadVersions = useCallback(async (): Promise<
    OciManifestSummary[] | null
  > => {
    setLoading(true);
    setError(null);
    try {
      const nextManifests = await fetchOciManifests(repositoryId, image);
      const nextVersions = ociVersionOptions(nextManifests);
      const initialVersion = nextVersions.find(
        (version) =>
          version.value === initialReference ||
          version.digest === initialReference,
      );
      setManifests(nextManifests);
      setSelectedReference((current) =>
        current && nextVersions.some((version) => version.value === current)
          ? current
          : (initialVersion?.value ?? nextVersions[0]?.value ?? null),
      );
      return nextManifests;
    } catch (requestError) {
      setError(requestError);
      return null;
    } finally {
      setLoading(false);
    }
  }, [image, initialReference, repositoryId]);

  useEffect(() => {
    void loadVersions();
  }, [loadVersions]);

  // A deep link may change while the expanded row stays mounted. Apply the
  // requested digest/tag without resetting later manual version selections.
  useEffect(() => {
    if (!initialReference || !manifests) return;
    const target = ociVersionOptions(manifests).find(
      (version) =>
        version.value === initialReference ||
        version.digest === initialReference,
    );
    if (target) setSelectedReference(target.value);
  }, [initialReference, manifests]);

  // 加载选中标签的 manifest + config
  const loadManifest = useCallback(
    async (reference: string) => {
      setManifestLoading(true);
      setError(null);
      setManifest(null);
      setConfig(null);
      try {
        const res = await ociFetch(
          token,
          `${name}/manifests/${reference}`,
          MANIFEST_ACCEPT,
        );
        const m = (await res.json()) as OciManifest;
        setManifest(m);
        if (m.config?.digest) {
          try {
            const cfgRes = await ociFetch(
              token,
              `${name}/blobs/${m.config.digest}`,
            );
            setConfig((await cfgRes.json()) as OciConfig);
          } catch {
            setConfig(null);
          }
        }
      } catch (e) {
        setError(e);
      } finally {
        setManifestLoading(false);
      }
    },
    [token, name],
  );

  useEffect(() => {
    if (selectedReference) void loadManifest(selectedReference);
  }, [selectedReference, loadManifest]);

  if (loading)
    return <Loading label={text("加载镜像详情…", "Loading image details…")} />;
  if (!manifests)
    return (
      <ErrorBanner error={error ?? new Error("读取 OCI Manifest 列表失败")} />
    );
  if (manifests.length === 0)
    return (
      <p className="py-6 text-center text-sm text-zinc-500">
        {text("该镜像没有可见 Manifest", "This image has no visible manifests")}
      </p>
    );

  const totalSize =
    (manifest?.layers ?? []).reduce((n, l) => n + (l.size ?? 0), 0) +
    (manifest?.config?.size ?? 0);
  const hasImageDescriptors =
    manifest?.config !== undefined || manifest?.layers !== undefined;

  const deleteReference = async () => {
    if (!selectedVersion) return;
    setDeleting(true);
    try {
      const registryToken = await ociRegistryToken(token);
      const res = await fetch(
        `/v2/${name}/manifests/${encodeURIComponent(selectedVersion.value)}`,
        {
          method: "DELETE",
          headers: { Authorization: `Bearer ${registryToken}` },
        },
      );
      if (!res.ok)
        throw new Error(`${res.status}: ${(await res.text()).slice(0, 120)}`);
      setManifest(null);
      setConfig(null);
      const remaining = await loadVersions();
      if (remaining?.length === 0) onDeleted?.();
    } catch (e) {
      setError(e);
    } finally {
      setDeleting(false);
    }
  };

  const copyUsage = async (snippet: UsageSnippet) => {
    try {
      await navigator.clipboard.writeText(snippet.code);
      setCopiedUsage(snippet.code);
      window.setTimeout(
        () =>
          setCopiedUsage((current) =>
            current === snippet.code ? null : current,
          ),
        1400,
      );
    } catch {
      setCopiedUsage(null);
    }
  };

  return (
    <div className="space-y-4">
      {error !== null && (
        <ErrorBanner
          error={error}
          onRetry={() => {
            if (selectedReference) void loadManifest(selectedReference);
            else void loadVersions();
          }}
        />
      )}
      {/* 版本选择 */}
      <div className="flex items-center gap-3">
        <label
          className="shrink-0 text-xs text-zinc-500"
          htmlFor="oci-version-select"
        >
          {text("版本", "Version")}
        </label>
        <Select
          id="oci-version-select"
          className="min-w-0 flex-1 font-mono text-xs"
          showSearch={{
            optionFilterProp: "label",
            filterOption: (input, option) =>
              String(option?.searchText ?? option?.label ?? "")
                .toLowerCase()
                .includes(input.toLowerCase()),
          }}
          value={selectedReference ?? undefined}
          options={versions}
          onChange={setSelectedReference}
          placeholder={text("搜索标签或 Digest", "Search tags or digests")}
          listHeight={280}
        />
        {selectedVersion && (
          <Popconfirm
            title={
              selectedVersion.kind === "tag"
                ? text("解绑当前镜像标签？", "Unlink this image tag?")
                : text(
                    "删除无标签 Manifest？",
                    "Delete this untagged manifest?",
                  )
            }
            description={
              selectedVersion.kind === "tag"
                ? text(
                    "只移除该标签；没有其他标签时，Manifest 会保留为可按 Digest 管理的版本。",
                    "Only the tag is removed. The manifest remains manageable by digest when no other tags exist.",
                  )
                : text(
                    "Manifest 将进入墓碑，可在墓碑页恢复。",
                    "The manifest moves to tombstones and can be restored there.",
                  )
            }
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteReference()}
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              loading={deleting}
            >
              {selectedVersion.kind === "tag"
                ? text("解绑标签", "Unlink tag")
                : text("删除 Manifest", "Delete manifest")}
            </Button>
          </Popconfirm>
        )}
      </div>

      {selectedVersion && (
        <div className="flex min-w-0 items-center gap-2 rounded border border-zinc-800 px-3 py-2 text-xs">
          <span className="shrink-0 text-zinc-500">Digest</span>
          <code
            className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap text-zinc-300"
            title={selectedVersion.digest}
          >
            {selectedVersion.digest}
          </code>
          {selectedVersion.kind === "digest" && (
            <Badge tone="amber">无标签</Badge>
          )}
        </div>
      )}
      {selectedVersion && (
        <ArtifactScanStatus
          repositoryId={repositoryId}
          format="oci"
          coordinate={image}
          digest={selectedVersion.digest}
        />
      )}
      {selectedVersion && (
        <ArtifactIntelligencePanel
          repositoryId={repositoryId}
          format="oci"
          coordinate={image}
          digest={selectedVersion.digest}
        />
      )}

      {selectedVersion && (
        <div className="rounded-lg border border-zinc-800/90 bg-zinc-950/30 p-3">
          <div className="mb-2">
            <div className="text-xs font-medium text-zinc-300">
              {text("使用方式", "Usage")}
            </div>
            <div className="mt-1 text-[11px] text-zinc-500">
              {text(
                "使用当前选中的标签或 Digest 访问镜像。",
                "Use the selected tag or digest to pull this image.",
              )}
            </div>
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            {ociUsage(repository, image, selectedReference ?? undefined).map(
              (snippet: UsageSnippet) => (
                <UsageSnippetBlock
                  key={snippet.label}
                  snippet={snippet}
                  copied={copiedUsage === snippet.code}
                  onCopy={() => void copyUsage(snippet)}
                />
              ),
            )}
          </div>
        </div>
      )}

      {manifestLoading ? (
        <Loading label={text("加载清单…", "Loading manifest…")} />
      ) : !manifest ? null : (
        <>
          {/* 概要 */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                {text("镜像大小", "Image size")}
              </div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {hasImageDescriptors
                  ? formatBytes(totalSize)
                  : text("无层数据", "No layer data")}
              </div>
              {!hasImageDescriptors && (
                <div className="mt-1 text-[10px] leading-4 text-zinc-600">
                  {text("仅包含 Manifest 元数据", "Manifest metadata only")}
                </div>
              )}
              <div className="mt-1 text-[10px] leading-4 text-zinc-600">
                Manifest JSON {formatBytes(selectedVersion?.size)}
              </div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                {text("层数", "Layers")}
              </div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {manifest.layers?.length ?? 0}
              </div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                {text("架构 / 系统", "Architecture / OS")}
              </div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {config?.architecture ?? "—"} / {config?.os ?? "—"}
              </div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                {text("创建时间", "Created")}
              </div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {config?.created ? formatDate(config.created, locale) : "—"}
              </div>
            </div>
          </div>

          {/* 启动配置 */}
          {config?.config && (
            <div className="rounded-lg border border-zinc-800 px-3 py-2.5 text-xs">
              <div className="mb-1.5 text-[10px] uppercase tracking-wider text-zinc-500">
                {text("启动配置", "Runtime configuration")}
              </div>
              <div className="space-y-1 font-mono">
                {config.config.Entrypoint && (
                  <div className="flex gap-2">
                    <span className="w-20 shrink-0 text-zinc-600">
                      Entrypoint
                    </span>
                    <span className="text-zinc-300">
                      {config.config.Entrypoint.join(" ")}
                    </span>
                  </div>
                )}
                {config.config.Cmd && (
                  <div className="flex gap-2">
                    <span className="w-20 shrink-0 text-zinc-600">Cmd</span>
                    <span className="text-zinc-300">
                      {config.config.Cmd.join(" ")}
                    </span>
                  </div>
                )}
                {config.config.User && (
                  <div className="flex gap-2">
                    <span className="w-20 shrink-0 text-zinc-600">User</span>
                    <span className="text-zinc-300">{config.config.User}</span>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 层列表 */}
          <div>
            <div className="mb-1.5 flex items-center gap-2 text-[10px] uppercase tracking-wider text-zinc-500">
              {text("文件层", "Layers")} ({manifest.layers?.length ?? 0})
              {manifest.mediaType && (
                <Badge tone="zinc">{manifest.mediaType.split(".").pop()}</Badge>
              )}
            </div>
            <Table<OciDescriptor>
              className="ag-console-table"
              rowKey="digest"
              size="small"
              pagination={false}
              scroll={{ x: 620 }}
              dataSource={manifest.layers ?? []}
              columns={
                [
                  {
                    title: "#",
                    key: "index",
                    width: 64,
                    render: (_, __, index) => (
                      <span className="font-mono text-xs text-zinc-500">
                        #{index + 1}
                      </span>
                    ),
                  },
                  {
                    title: "Digest",
                    dataIndex: "digest",
                    key: "digest",
                    ellipsis: true,
                    render: (value: string) => (
                      <span
                        className="font-mono text-xs text-zinc-300"
                        title={value}
                      >
                        {shortDigest(value)}
                      </span>
                    ),
                  },
                  {
                    title: text("类型", "Type"),
                    dataIndex: "mediaType",
                    key: "mediaType",
                    width: 160,
                    render: (value: string) => {
                      const kind = value.includes("gzip")
                        ? "tar+gzip"
                        : value.includes("tar")
                          ? "tar"
                          : (value.split(".").pop() ?? value);
                      return (
                        <span className="text-xs text-zinc-400">{kind}</span>
                      );
                    },
                  },
                  {
                    title: text("大小", "Size"),
                    dataIndex: "size",
                    key: "size",
                    width: 120,
                    align: "right",
                    render: (value: number) => (
                      <span className="text-xs text-zinc-300">
                        {formatBytes(value)}
                      </span>
                    ),
                  },
                ] satisfies ColumnsType<OciDescriptor>
              }
            />
          </div>
        </>
      )}
    </div>
  );
}
