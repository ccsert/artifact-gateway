import { formatDescriptor, hasPackageVersions } from "../../formats";
import type { GlobalArtifactSearchHit } from "../../client";
import { formatBytes } from "../../lib/format";
import { mavenVersion } from "../../lib/usage";

export const SEARCH_PAGE_SIZE = 100;

export type MavenGroup = {
  key: string;
  repositoryId: string;
  repositoryName: string;
  format: "maven";
  coordinate: string;
  hits: GlobalArtifactSearchHit[];
};

export type SearchRow = GlobalArtifactSearchHit | MavenGroup;

export type SearchTableRow = {
  key: string;
  row: SearchRow;
  representative: GlobalArtifactSearchHit;
};

export function isMavenGroup(row: SearchRow): row is MavenGroup {
  return "hits" in row;
}

export function artifactTarget(hit: GlobalArtifactSearchHit): string {
  const params = new URLSearchParams({ artifact: hit.coordinate });
  if (hit.buildNumber && hit.buildNumber > 0)
    params.set("build", String(hit.buildNumber));
  if (formatDescriptor(hit.format)?.digestReferences && hit.digest)
    params.set("reference", hit.digest);
  if (hasPackageVersions(hit.format) && hit.version)
    params.set("version", hit.version);
  return `/repositories/${hit.repositoryId}?${params.toString()}`;
}

export function artifactVersionSizeLabel(hit: GlobalArtifactSearchHit): string {
  const size = formatBytes(hit.size);
  return hasPackageVersions(hit.format) && hit.version
    ? `${hit.version} · ${size}`
    : size;
}

export function buildRows(hits: GlobalArtifactSearchHit[]): SearchRow[] {
  const rows: SearchRow[] = [];
  const groups = new Map<string, MavenGroup>();
  for (const hit of hits) {
    const ga = formatDescriptor(hit.format)?.groupKey?.(hit.coordinate) ?? null;
    if (!ga) {
      rows.push(hit);
      continue;
    }
    const key = `${hit.repositoryId}:${ga}`;
    const existing = groups.get(key);
    if (existing) existing.hits.push(hit);
    else {
      const group: MavenGroup = {
        key,
        repositoryId: hit.repositoryId,
        repositoryName: hit.repositoryName,
        format: "maven",
        coordinate: ga,
        hits: [hit],
      };
      groups.set(key, group);
      rows.push(group);
    }
  }
  for (const row of rows) {
    if (isMavenGroup(row)) {
      row.hits.sort(
        (a, b) =>
          (b.createdAt ?? "").localeCompare(a.createdAt ?? "") ||
          (b.buildNumber ?? 0) - (a.buildNumber ?? 0),
      );
    }
  }
  return rows;
}

export function mavenVersionLabel(hit: GlobalArtifactSearchHit): string {
  const version = mavenVersion(hit.coordinate) ?? hit.coordinate;
  return hit.buildNumber && hit.buildNumber > 0
    ? `${version} · build #${hit.buildNumber}`
    : version;
}

export function mavenIntelligence(hits: GlobalArtifactSearchHit[]) {
  const summaries = hits.map((hit) => hit.intelligence).filter(Boolean);
  if (summaries.some((summary) => summary?.vulnerabilityStatus === "affected"))
    return summaries.find(
      (summary) => summary?.vulnerabilityStatus === "affected",
    );
  return summaries[0];
}
