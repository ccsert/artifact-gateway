import { listOciManifests } from "../../client";
import type { OciManifestSummary } from "../../client";
import { shortDigest } from "../../lib/format";

export interface OciDescriptor {
  mediaType: string;
  size?: number;
  digest: string;
}

export interface OciManifest {
  schemaVersion: number;
  mediaType?: string;
  config?: OciDescriptor;
  layers?: OciDescriptor[];
}

export interface OciConfig {
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

export interface OciVersionOption {
  value: string;
  label: string;
  searchText: string;
  digest: string;
  size: number;
  kind: "tag" | "digest";
}

export const MANIFEST_ACCEPT =
  "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json";

export async function ociRegistryToken(
  token: string,
  signal?: AbortSignal,
): Promise<string> {
  const response = await fetch("/auth/token", {
    signal,
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

export async function ociFetch(
  token: string,
  path: string,
  accept?: string,
  signal?: AbortSignal,
): Promise<Response> {
  const registryToken = await ociRegistryToken(token, signal);
  const res = await fetch(`/v2/${path}`, {
    signal,
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

export async function fetchOciManifests(
  repositoryId: string,
  image: string,
  signal?: AbortSignal,
): Promise<OciManifestSummary[]> {
  const items: OciManifestSummary[] = [];
  const seenTokens = new Set<string>();
  let pageToken: string | undefined;
  do {
    const response = await listOciManifests({
      path: { repositoryId },
      query: { name: image, pageSize: 200, pageToken },
      signal,
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

export function ociVersionOptions(
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

/** One manifest and its image config; the config is optional metadata. */
export async function fetchOciManifest(
  token: string,
  name: string,
  reference: string,
  signal?: AbortSignal,
): Promise<{ manifest: OciManifest; config: OciConfig | null }> {
  const res = await ociFetch(
    token,
    `${name}/manifests/${reference}`,
    MANIFEST_ACCEPT,
    signal,
  );
  const manifest = (await res.json()) as OciManifest;
  let config: OciConfig | null = null;
  if (manifest.config?.digest) {
    try {
      const cfgRes = await ociFetch(
        token,
        `${name}/blobs/${manifest.config.digest}`,
        undefined,
        signal,
      );
      config = (await cfgRes.json()) as OciConfig;
    } catch {
      config = null;
    }
  }
  return { manifest, config };
}
