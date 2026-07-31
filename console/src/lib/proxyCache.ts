import type { ProxyFormat } from './v1proxy';

export type ProxyCacheGroupBy = 'version' | 'component' | 'asset';
export type ProxyCacheAssetFilter = 'primary' | 'all' | 'jar' | 'pom';

export interface ProxyCacheAsset {
  path: string;
  name: string;
  digest?: string;
  size?: number;
  contentType?: string;
  member?: string;
  sidecar: boolean;
}

export interface ProxyCacheBrowseItem {
  key: string;
  coordinate: string;
  groupId?: string;
  artifactId?: string;
  version?: string;
  path?: string;
  digest?: string;
  size?: number;
  contentType?: string;
  member?: string;
  assetCount?: number;
  primaryAssetCount?: number;
  sidecarCount?: number;
  extensions?: string[];
  assets?: ProxyCacheAsset[];
}

export interface ProxyCacheBrowsePage {
  items: ProxyCacheBrowseItem[];
  nextPageToken?: string;
  totalEstimate: number;
  groupBy: ProxyCacheGroupBy;
}

async function parseError(res: Response): Promise<Error> {
  const text = await res.text();
  try {
    const problem = JSON.parse(text);
    return new Error(`${res.status}: ${problem?.message ?? problem?.error?.message ?? text}`);
  } catch {
    return new Error(`${res.status}: ${text.slice(0, 120)}`);
  }
}

export async function listProxyCacheEntries(
  token: string,
  repositoryId: string,
  params: {
    format: ProxyFormat;
    groupBy?: ProxyCacheGroupBy;
    assetFilter?: ProxyCacheAssetFilter;
    q?: string;
    pageSize?: number;
    pageToken?: string;
  },
): Promise<ProxyCacheBrowsePage> {
  const query = new URLSearchParams();
  query.set('format', params.format);
  query.set('groupBy', params.groupBy ?? 'version');
  query.set('assetFilter', params.assetFilter ?? 'primary');
  if (params.q) query.set('q', params.q);
  if (params.pageSize) query.set('pageSize', String(params.pageSize));
  if (params.pageToken) query.set('pageToken', params.pageToken);
  const res = await fetch(`/api/v2/repositories/${encodeURIComponent(repositoryId)}/cache/entries?${query}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as ProxyCacheBrowsePage;
}
