import { useCallback, useEffect, useState } from 'react';
import { DeleteOutlined } from '@ant-design/icons';
import { Button, Popconfirm, Select } from 'antd';
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
  repository,
  image,
  onDeleted,
}: {
  repository: string;
  image: string;
  onDeleted?: () => void;
}) {
  const { token } = useAuth();
  const [tags, setTags] = useState<string[] | null>(null);
  const [selectedTag, setSelectedTag] = useState<string | null>(null);
  const [manifest, setManifest] = useState<OciManifest | null>(null);
  const [config, setConfig] = useState<OciConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [manifestLoading, setManifestLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  const name = `${repository}/${image}`;

  // 加载标签列表
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    ociFetch(token, `${name}/tags/list`)
      .then((r) => r.json())
      .then((d: { tags?: string[] }) => {
        if (cancelled) return;
        const t = d.tags ?? [];
        setTags(t);
        setSelectedTag(t[0] ?? null);
        setLoading(false);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token, name]);

  // 加载选中标签的 manifest + config
  const loadManifest = useCallback(
    async (tag: string) => {
      setManifestLoading(true);
      setError(null);
      setManifest(null);
      setConfig(null);
      try {
        const res = await ociFetch(token, `${name}/manifests/${tag}`, MANIFEST_ACCEPT);
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
    if (selectedTag) void loadManifest(selectedTag);
  }, [selectedTag, loadManifest]);

  if (loading) return <Loading label="加载镜像详情…" />;
  if (!tags) return <ErrorBanner error={error ?? new Error('读取镜像标签失败')} />;
  if (tags.length === 0)
    return <p className="py-6 text-center text-sm text-zinc-500">该镜像暂无标签</p>;

  const totalSize = (manifest?.layers ?? []).reduce((n, l) => n + l.size, 0) + (manifest?.config?.size ?? 0);

  const deleteTag = async () => {
    if (!selectedTag) return;
    setDeleting(true);
    try {
      const registryToken = await ociRegistryToken(token);
      const res = await fetch(`/v2/${name}/manifests/${encodeURIComponent(selectedTag)}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${registryToken}` },
      });
      if (!res.ok) throw new Error(`${res.status}: ${(await res.text()).slice(0, 120)}`);
      const remaining = (tags ?? []).filter((t) => t !== selectedTag);
      setTags(remaining);
      setSelectedTag(remaining[0] ?? null);
      setManifest(null);
      setConfig(null);
      if (remaining.length === 0) onDeleted?.();
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
            if (selectedTag) void loadManifest(selectedTag);
          }}
        />
      )}
      {/* 标签选择 */}
      <div className="flex items-center gap-3">
        <label className="shrink-0 text-xs text-zinc-500" htmlFor="oci-tag-select">标签</label>
        <Select
          id="oci-tag-select"
          className="min-w-0 flex-1 font-mono text-xs"
          showSearch={{ optionFilterProp: 'label' }}
          value={selectedTag ?? undefined}
          options={tags.map((tag) => ({ value: tag, label: tag }))}
          onChange={setSelectedTag}
          placeholder="搜索并选择标签"
          listHeight={280}
        />
        {selectedTag && (
          <Popconfirm
            title="删除当前镜像标签？"
            description={`将删除 ${selectedTag} 的 manifest 引用。`}
            okText="删除"
            cancelText="取消"
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteTag()}
          >
            <Button danger size="small" icon={<DeleteOutlined />} loading={deleting}>
              删除标签
            </Button>
          </Popconfirm>
        )}
      </div>

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
