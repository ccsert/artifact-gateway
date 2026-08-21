export interface PublicOciManifestDetail {
  digest?: string;
  size?: number;
  createdAt?: string;
  publisher?: string;
}

export interface PublicOciManifestRequest {
  repositoryName: string;
  imageName: string;
  reference: string;
}

export type PublicOciFetch = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

const manifestAccept = [
  "application/vnd.oci.image.manifest.v1+json",
  "application/vnd.docker.distribution.manifest.v2+json",
  "application/vnd.oci.image.index.v1+json",
  "application/vnd.docker.distribution.manifest.list.v2+json",
].join(", ");

const maximumIndexDepth = 2;

type ManifestEnvelope = {
  config?: { digest?: string; size?: number };
  layers?: Array<{ size?: number }>;
  manifests?: Array<{ digest?: string }>;
};

type ConfigEnvelope = {
  created?: string;
  author?: string;
  config?: { Labels?: Record<string, string> };
};

export function nextPublicOciTagCursor(
  response: Response,
  tags: string[],
  pageSize: number,
  origin: string,
): string | undefined {
  const link = response.headers.get("Link");
  const target = link?.match(/<([^>]+)>;\s*rel="next"/i)?.[1];
  if (target) {
    try {
      return new URL(target, origin).searchParams.get("last") ?? undefined;
    } catch {
      // Fall through to the page-size heuristic for non-standard registries.
    }
  }
  return tags.length === pageSize ? tags.at(-1) : undefined;
}

export async function readPublicOciManifestDetail(
  request: PublicOciManifestRequest,
  fetchAdapter: PublicOciFetch = fetch,
): Promise<PublicOciManifestDetail> {
  const imagePath = request.imageName
    .split("/")
    .map(encodeURIComponent)
    .join("/");
  return readManifest(
    request.repositoryName,
    imagePath,
    request.reference,
    fetchAdapter,
    0,
  );
}

async function readManifest(
  repositoryName: string,
  imagePath: string,
  reference: string,
  fetchAdapter: PublicOciFetch,
  depth: number,
): Promise<PublicOciManifestDetail> {
  const response = await fetchAdapter(
    `/v2/${encodeURIComponent(repositoryName)}/${imagePath}/manifests/${encodeURIComponent(reference)}`,
    { headers: { Accept: manifestAccept } },
  );
  if (!response.ok) {
    throw new Error(`读取 OCI manifest 失败 (${response.status})`);
  }

  const envelope = (await response.json()) as ManifestEnvelope;
  const digest = response.headers.get("Docker-Content-Digest") ?? undefined;
  const layerSize = (envelope.layers ?? []).reduce(
    (total, layer) => total + (layer.size ?? 0),
    envelope.config?.size ?? 0,
  );

  // Multi-platform indexes point to a child manifest. Resolve the child to
  // retain useful config metadata while keeping the digest selected by the tag.
  if (!envelope.config && depth < maximumIndexDepth) {
    const child = envelope.manifests?.find((entry) => entry.digest);
    if (child?.digest) {
      const nested = await readManifest(
        repositoryName,
        imagePath,
        child.digest,
        fetchAdapter,
        depth + 1,
      );
      return {
        ...nested,
        digest: digest ?? nested.digest,
        size: (nested.size ?? layerSize) || undefined,
      };
    }
  }

  const config = envelope.config?.digest
    ? await readConfig(
        repositoryName,
        imagePath,
        envelope.config.digest,
        fetchAdapter,
      )
    : undefined;
  const labels = config?.config?.Labels ?? {};
  return {
    digest,
    size: layerSize || undefined,
    createdAt: config?.created ?? labels["org.opencontainers.image.created"],
    publisher:
      config?.author ||
      labels["org.opencontainers.image.authors"] ||
      labels["org.opencontainers.image.vendor"],
  };
}

async function readConfig(
  repositoryName: string,
  imagePath: string,
  digest: string,
  fetchAdapter: PublicOciFetch,
): Promise<ConfigEnvelope | undefined> {
  const response = await fetchAdapter(
    `/v2/${encodeURIComponent(repositoryName)}/${imagePath}/blobs/${encodeURIComponent(digest)}`,
  );
  if (!response.ok) return undefined;
  return (await response.json()) as ConfigEnvelope;
}
