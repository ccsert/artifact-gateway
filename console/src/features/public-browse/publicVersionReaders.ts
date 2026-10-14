import { LocalizedError } from "../../messages/translate";
import { listOciManifests } from "../../client";
import { nextPublicOciTagCursor } from "../../lib/publicOci";
import { unwrap } from "../../lib/query/client";
import { VERSION_PAGE_SIZE } from "./constants";
import type { PublicRepository } from "./types";

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
  if (!repository)
    throw new LocalizedError("publicBrowse.failedToReadOciVersions");
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
      throw new LocalizedError(
        "publicBrowse.failedToReadOciVersions",
        undefined,
        {
          status: (error as { status?: number } | null)?.status,
        },
      );
    });
    if (!page) throw new LocalizedError("publicBrowse.failedToReadOciVersions");
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
    throw new LocalizedError(
      "publicBrowse.failedToReadOciTagsStatus",
      { status: response.status },
      { status: response.status },
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
