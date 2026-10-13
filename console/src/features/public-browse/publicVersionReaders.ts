import { listOciManifests } from "../../client";
import { nextPublicOciTagCursor } from "../../lib/publicOci";
import { unwrap } from "../../lib/query/client";
import { VERSION_PAGE_SIZE } from "./constants";
import type { PublicRepository } from "./types";

/** A failed read keeps its HTTP status, so callers can tell refusals apart. */
function statusError(message: string, status: number) {
  return Object.assign(new Error(message), { status });
}

export type OciTagPageData = { tags: string[]; nextCursor?: string };

/**
 * One page of an image's versions: managed repositories list manifests (a
 * tag, or the digest of an untagged one); groups page the registry tag list.
 */
export async function readPublicOciTagPage(
  repository: PublicRepository | undefined,
  coordinate: string,
  cursor: string,
  signal: AbortSignal,
): Promise<OciTagPageData> {
  if (!repository) throw new Error("读取 OCI 版本失败");
  if (repository.type !== "group") {
    const page = await unwrap(
      listOciManifests({
        path: { repositoryId: repository.id },
        query: {
          name: coordinate,
          pageSize: VERSION_PAGE_SIZE,
          pageToken: cursor || undefined,
        },
        signal,
      }),
    ).catch((error: unknown) => {
      throw Object.assign(new Error("读取 OCI 版本失败"), {
        status: (error as { status?: number } | null)?.status,
      });
    });
    if (!page) throw new Error("读取 OCI 版本失败");
    return {
      tags: page.items.flatMap((manifest) =>
        manifest.tags.length > 0 ? manifest.tags : [manifest.digest],
      ),
      nextCursor: page.nextPageToken,
    };
  }
  const imagePath = coordinate.split("/").map(encodeURIComponent).join("/");
  const query = new URLSearchParams({ n: String(VERSION_PAGE_SIZE) });
  if (cursor) query.set("last", cursor);
  const response = await fetch(
    `/v2/${encodeURIComponent(repository.name)}/${imagePath}/tags/list?${query}`,
    { signal },
  );
  if (!response.ok)
    throw statusError(
      `读取 OCI 标签失败 (${response.status})`,
      response.status,
    );
  const data = (await response.json()) as { tags?: string[] };
  const tags = data.tags ?? [];
  return {
    tags,
    nextCursor: nextPublicOciTagCursor(
      response,
      tags,
      VERSION_PAGE_SIZE,
      window.location.origin,
    ),
  };
}
