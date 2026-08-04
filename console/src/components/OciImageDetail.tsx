import { useCallback, useEffect, useState } from 'react';
import { DeleteOutlined } from '@ant-design/icons';
import { Button, Popconfirm, Select } from 'antd';
import { listOciManifests } from '../client';
import type { OciManifestSummary } from '../client';
import { useAuth } from '../lib/auth';
import { Loading, ErrorBanner } from './Feedback';
import { Badge } from './Badge';
import { formatBytes, formatDate, shortDigest } from '../lib/format';

interface OciDescriptor {
  mediaType: string;
  size: number;
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
  kind: 'tag' | 'digest';
}

const MANIFEST_ACCEPT =
  'application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json';

async function ociRegistryToken(token: string): Promise<string> {
  const response = await fetch('/auth/token', { headers: { Authorization: `Bearer ${token}` } });
  if (!response.ok) {
    throw new Error(`${response.status}: ${(await response.text()).slice(0, 120)}`);
  }
  const body = (await response.json()) as { token?: string; access_token?: string };
  const registryToken = body.token ?? body.access_token;
  if (!registryToken) throw new Error('Registry token missing');
  return registryToken;
}

async function ociFetch(token: string, path: string, accept?: string): Promise<Response> {
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

async function fetchOciManifests(repositoryId: string, image: string): Promise<OciManifestSummary[]> {
  const items: OciManifestSummary[] = [];
  const seenTokens = new Set<string>();
  let pageToken: string | undefined;
  do {
    const response = await listOciManifests({
      path: { repositoryId },
      query: { name: image, pageSize: 200, pageToken },
    });
    if (response.error || !response.data) throw new Error('读取 OCI Manifest 列表失败');
    items.push(...response.data.items);
    pageToken = response.data.nextPageToken;
    if (pageToken && seenTokens.has(pageToken)) throw new Error('OCI Manifest 分页游标重复');
    if (pageToken) seenTokens.add(pageToken);
  } while (pageToken);
  return items;
}

function ociVersionOptions(manifests: OciManifestSummary[]): OciVersionOption[] {
  return manifests.flatMap<OciVersionOption>((manifest) => {
    if (manifest.tags.length === 0) {
      return [{
        value: manifest.digest,
        label: `无标签 · ${shortDigest(manifest.digest)}`,
        searchText: `无标签 ${manifest.digest}`,
        digest: manifest.digest,
        kind: 'digest' as const,
      }];
    }
    return manifest.tags.map((tag) => ({
      value: tag,
      label: `${tag} · ${shortDigest(manifest.digest)}`,
      searchText: `${tag} ${manifest.digest}`,
      digest: manifest.digest,
      kind: 'tag' as const,
    }));
  });
}

function LayerRow({ index, layer }: { index: number; layer: OciDescriptor }) {
  const kind = layer.mediaType.includes('gzip')
    ? 'tar+gzip'
    : layer.mediaType.includes('tar')
      ? 'tar'
      : layer.mediaType.split('.').pop() ?? layer.mediaType;
  return (
    <tr className="hover:bg-zinc-800/30">
      <td className="px-3 py-2 font-mono text-xs text-zinc-500">#{index + 1}</td>
      <td className="px-3 py-2 font-mono text-xs text-zinc-300" title={layer.digest}>
        {shortDigest(layer.digest)}
      </td>
      <td className="px-3 py-2 text-xs text-zinc-400">{kind}</td>
      <td className="px-3 py-2 text-right text-xs text-zinc-300">{formatBytes(layer.size)}</td>
    </tr>
  );
}

export function OciImageDetail({
  repositoryId,
  repository,
  image,
  onDeleted,
}: {
  repositoryId: string;
  repository: string;
  image: string;
  onDeleted?: () => void;
}) {
  const { token } = useAuth();
  const [manifests, setManifests] = useState<OciManifestSummary[] | null>(null);
  const [selectedReference, setSelectedReference] = useState<string | null>(null);
  const [manifest, setManifest] = useState<OciManifest | null>(null);
  const [config, setConfig] = useState<OciConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [manifestLoading, setManifestLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  const name = `${repository}/${image}`;
  const versions = ociVersionOptions(manifests ?? []);
  const selectedVersion = versions.find((version) => version.value === selectedReference);

  const loadVersions = useCallback(async (): Promise<OciManifestSummary[] | null> => {
    setLoading(true);
    setError(null);
    try {
      const nextManifests = await fetchOciManifests(repositoryId, image);
      const nextVersions = ociVersionOptions(nextManifests);
      setManifests(nextManifests);
      setSelectedReference((current) =>
        current && nextVersions.some((version) => version.value === current)
          ? current
          : (nextVersions[0]?.value ?? null),
      );
      return nextManifests;
    } catch (requestError) {
      setError(requestError);
      return null;
    } finally {
      setLoading(false);
    }
  }, [image, repositoryId]);

  useEffect(() => {
    void loadVersions();
  }, [loadVersions]);

  // 加载选中标签的 manifest + config
  const loadManifest = useCallback(
    async (reference: string) => {
      setManifestLoading(true);
      setError(null);
      setManifest(null);
      setConfig(null);
      try {
        const res = await ociFetch(token, `${name}/manifests/${reference}`, MANIFEST_ACCEPT);
        const m = (await res.json()) as OciManifest;
        setManifest(m);
        if (m.config?.digest) {
          try {
            const cfgRes = await ociFetch(token, `${name}/blobs/${m.config.digest}`);
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

  if (loading) return <Loading label="加载镜像详情…" />;
  if (!manifests) return <ErrorBanner error={error ?? new Error('读取 OCI Manifest 列表失败')} />;
  if (manifests.length === 0)
    return <p className="py-6 text-center text-sm text-zinc-500">该镜像没有可见 Manifest</p>;

  const totalSize = (manifest?.layers ?? []).reduce((n, l) => n + l.size, 0) + (manifest?.config?.size ?? 0);

  const deleteReference = async () => {
    if (!selectedVersion) return;
    setDeleting(true);
    try {
      const registryToken = await ociRegistryToken(token);
      const res = await fetch(`/v2/${name}/manifests/${encodeURIComponent(selectedVersion.value)}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${registryToken}` },
      });
      if (!res.ok) throw new Error(`${res.status}: ${(await res.text()).slice(0, 120)}`);
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
        <label className="shrink-0 text-xs text-zinc-500" htmlFor="oci-version-select">版本</label>
        <Select
          id="oci-version-select"
          className="min-w-0 flex-1 font-mono text-xs"
          showSearch={{
            optionFilterProp: 'label',
            filterOption: (input, option) =>
              String(option?.searchText ?? option?.label ?? '').toLowerCase().includes(input.toLowerCase()),
          }}
          value={selectedReference ?? undefined}
          options={versions}
          onChange={setSelectedReference}
          placeholder="搜索标签或 Digest"
          listHeight={280}
        />
        {selectedVersion && (
          <Popconfirm
            title={selectedVersion.kind === 'tag' ? '解绑当前镜像标签？' : '删除无标签 Manifest？'}
            description={
              selectedVersion.kind === 'tag'
                ? '只移除该标签；没有其他标签时，Manifest 会保留为可按 Digest 管理的版本。'
                : 'Manifest 将进入墓碑，可在墓碑页恢复。'
            }
            okText="删除"
            cancelText="取消"
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteReference()}
          >
            <Button danger size="small" icon={<DeleteOutlined />} loading={deleting}>
              {selectedVersion.kind === 'tag' ? '解绑标签' : '删除 Manifest'}
            </Button>
          </Popconfirm>
        )}
      </div>

      {selectedVersion && (
        <div className="flex min-w-0 items-center gap-2 rounded border border-zinc-800 px-3 py-2 text-xs">
          <span className="shrink-0 text-zinc-500">Digest</span>
          <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap text-zinc-300" title={selectedVersion.digest}>
            {selectedVersion.digest}
          </code>
          {selectedVersion.kind === 'digest' && <Badge tone="amber">无标签</Badge>}
        </div>
      )}

      {manifestLoading ? (
        <Loading label="加载清单…" />
      ) : !manifest ? null : (
        <>
          {/* 概要 */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">总大小</div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">{formatBytes(totalSize)}</div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">层数</div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">{manifest.layers?.length ?? 0}</div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">架构 / 系统</div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {config?.architecture ?? '—'} / {config?.os ?? '—'}
              </div>
            </div>
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-wider text-zinc-500">创建时间</div>
              <div className="mt-0.5 text-sm font-semibold text-zinc-100">
                {config?.created ? formatDate(config.created) : '—'}
              </div>
            </div>
          </div>

          {/* 启动配置 */}
          {config?.config && (
            <div className="rounded-lg border border-zinc-800 px-3 py-2.5 text-xs">
              <div className="mb-1.5 text-[10px] uppercase tracking-wider text-zinc-500">启动配置</div>
              <div className="space-y-1 font-mono">
                {config.config.Entrypoint && (
                  <div className="flex gap-2">
                    <span className="w-20 shrink-0 text-zinc-600">Entrypoint</span>
                    <span className="text-zinc-300">{config.config.Entrypoint.join(' ')}</span>
                  </div>
                )}
                {config.config.Cmd && (
                  <div className="flex gap-2">
                    <span className="w-20 shrink-0 text-zinc-600">Cmd</span>
                    <span className="text-zinc-300">{config.config.Cmd.join(' ')}</span>
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
              文件层 ({manifest.layers?.length ?? 0})
              {manifest.mediaType && <Badge tone="zinc">{manifest.mediaType.split('.').pop()}</Badge>}
            </div>
            <div className="overflow-hidden rounded-lg border border-zinc-800">
              <table className="w-full text-left">
                <thead>
                  <tr className="border-b border-zinc-800 bg-zinc-900/40 text-[10px] uppercase tracking-wider text-zinc-500">
                    <th className="px-3 py-2 font-medium">#</th>
                    <th className="px-3 py-2 font-medium">Digest</th>
                    <th className="px-3 py-2 font-medium">类型</th>
                    <th className="px-3 py-2 text-right font-medium">大小</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800/60">
                  {(manifest.layers ?? []).map((l, i) => (
                    <LayerRow key={l.digest} index={i} layer={l} />
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
