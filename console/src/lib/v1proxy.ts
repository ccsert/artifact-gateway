// v1 legacy proxy-group API（/api/v1/{format}/groups）。
// 这套 API 不在 OpenAPI 管理契约里，用 fetch 直接封装。

export type ProxyFormat = 'oci' | 'maven' | 'raw' | 'conan';

export interface V1Member {
  name: string;
  type: 'hosted' | 'proxy';
  endpoint: string;
  position: number;
  anonymous: boolean;
  allowedHosts?: string[];
  repositoryId?: string;
}

export interface V1Group {
  name: string;
  enabled: boolean;
  anonymous: boolean;
  cacheQuotaBytes?: number;
  members: V1Member[];
  createdAt: string;
}

export const V1_FORMATS: ProxyFormat[] = ['oci', 'maven', 'raw', 'conan'];

async function v1Fetch(token: string, path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`/api/v1/${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  });
  return res;
}

async function parseError(res: Response): Promise<Error> {
  const text = await res.text();
  try {
    const j = JSON.parse(text);
    const msg = j?.error?.message ?? j?.message ?? text;
    return new Error(`${res.status}: ${msg}`);
  } catch {
    return new Error(`${res.status}: ${text.slice(0, 120)}`);
  }
}

export async function getV1Group(token: string, format: ProxyFormat, name: string): Promise<V1Group | null> {
  const res = await v1Fetch(token, `${format}/groups/${encodeURIComponent(name)}`);
  if (res.status === 404) return null;
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as V1Group;
}

export async function createV1Group(token: string, format: ProxyFormat, group: Partial<V1Group>): Promise<V1Group> {
  const res = await v1Fetch(token, `${format}/groups`, {
    method: 'POST',
    body: JSON.stringify(group),
  });
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as V1Group;
}

export async function disableV1Group(token: string, format: ProxyFormat, name: string): Promise<void> {
  const res = await v1Fetch(token, `${format}/groups/${encodeURIComponent(name)}/disable`, { method: 'POST' });
  if (!res.ok) throw await parseError(res);
}

// 列出某格式全部 group（v1 list 端点）
export async function listV1Groups(token: string, format: ProxyFormat): Promise<V1Group[]> {
  const res = await v1Fetch(token, `${format}/groups`);
  if (!res.ok) throw await parseError(res);
  const data = await res.json();
  const items = Array.isArray(data) ? data : (data?.items ?? []);
  return items as V1Group[];
}

// ---- 运营指标（/api/v1/operations/repositories?repository=<group>） ----

export interface GroupMetrics {
  requests: number;
  upstream_errors: number;
  cache_hits: number;
  cache_misses: number;
}

export interface GroupOperations {
  repository: string;
  metrics: GroupMetrics;
  hit_rate: number;
}

export async function getGroupOperations(token: string, name: string): Promise<GroupOperations | null> {
  const res = await v1Fetch(token, `operations/repositories?repository=${encodeURIComponent(name)}`);
  if (res.status === 404 || res.status === 400) return null;
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as GroupOperations;
}

// ---- 通过代理组拉取一个制品（协议路径） ----

export async function fetchViaProxy(
  token: string,
  format: ProxyFormat,
  group: string,
  path: string,
): Promise<{ status: number; bytes: number; contentType: string }> {
  const url =
    format === 'oci'
      ? `/v2/${group}/${path}`
      : format === 'maven'
        ? `/maven/${group}/${path}`
        : format === 'raw'
          ? `/raw/${group}/${path}`
          : `/conan/v2/${group}/${path}`;
  const res = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
  const buf = await res.arrayBuffer();
  return {
    status: res.status,
    bytes: buf.byteLength,
    contentType: res.headers.get('Content-Type') ?? '',
  };
}

// ---- 缓存制品清单（/api/v1/operations/cache/entries） ----

export interface CacheEntry {
  repository: string;
  digest?: string;
  size?: number;
  contentType?: string;
  member?: string;
  endpoint?: string;
  format?: string;
}

export async function listCacheEntries(token: string, format: ProxyFormat, group: string): Promise<CacheEntry[]> {
  const res = await v1Fetch(
    token,
    `operations/cache/entries?repository=${encodeURIComponent(group)}&format=${encodeURIComponent(format)}`,
  );
  if (res.status === 404 || res.status === 400) return [];
  if (!res.ok) throw await parseError(res);
  const data = await res.json();
  return Array.isArray(data) ? data : [];
}

export async function listKnownGroups(token: string): Promise<{ format: ProxyFormat; group: V1Group }[]> {
  const out: { format: ProxyFormat; group: V1Group }[] = [];
  for (const format of V1_FORMATS) {
    try {
      const groups = await listV1Groups(token, format);
      for (const group of groups) out.push({ format, group });
    } catch {
      /* 单格式失败不阻塞其他格式 */
    }
  }
  return out;
}
