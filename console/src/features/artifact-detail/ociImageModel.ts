import { LocalizedError } from "../../messages/translate";
import type { Translate } from "../../lib/preferences";
import { listOciManifests } from "../../client";
import type { OciManifestSummary } from "../../client";
import { shortDigest } from "../../lib/format";
import { unwrap } from "../../lib/query/client";

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

async function registryError(response: Response) {
  return Object.assign(
    new Error(`${response.status}: ${(await response.text()).slice(0, 120)}`),
    { status: response.status },
  );
}

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
    throw await registryError(response);
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
    throw await registryError(res);
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
    const page = await unwrap(
      listOciManifests({
        path: { repositoryId },
        query: { name: image, pageSize: 200, pageToken },
        signal,
      }),
    );
    if (!page)
      throw new LocalizedError("artifactDetail.failedToReadOciManifestList");
    items.push(...page.items);
    pageToken = page.nextPageToken;
    if (pageToken && seenTokens.has(pageToken))
      throw new LocalizedError("artifactDetail.ociManifestPageCursorRepeated");
    if (pageToken) seenTokens.add(pageToken);
  } while (pageToken);
  return items;
}

export function ociVersionOptions(
  manifests: OciManifestSummary[],
  t: Translate,
): OciVersionOption[] {
  return manifests.flatMap<OciVersionOption>((manifest) => {
    if (manifest.tags.length === 0) {
      return [
        {
          value: manifest.digest,
          label: t("artifactDetail.untaggedDigest", {
            digest: shortDigest(manifest.digest),
          }),
          searchText: `${t("artifactDetail.untagged")} ${manifest.digest}`,
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
    } catch (error) {
      // Optional config metadata must not hide an expired session.
      if (error instanceof Error && "status" in error && error.status === 401)
        throw error;
      config = null;
    }
  }
  return { manifest, config };
}
