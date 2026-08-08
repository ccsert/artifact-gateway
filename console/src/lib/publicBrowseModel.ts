import type { ArtifactSummary } from "../client";

export interface MavenArtifactGroup {
  key: string;
  versions: ArtifactSummary[];
}

export interface ConanArtifactGroup {
  key: string;
  versions: ArtifactSummary[];
}

export interface ArtifactBrowseSelection {
  coordinate: string;
  buildNumber?: number;
  tag?: string;
  revision?: string;
}

export function missingDeepLinkedArtifact(
  items: ArtifactSummary[],
  coordinate: string,
): string | undefined {
  const target = coordinate.trim();
  if (!target || items.some((item) => item.coordinate === target)) {
    return undefined;
  }
  return target;
}

export function mavenArtifactGroups(
  items: ArtifactSummary[],
): MavenArtifactGroup[] {
  const groups = new Map<string, ArtifactSummary[]>();
  for (const item of items) {
    const parts = item.coordinate.split(":");
    const key = parts.length >= 2 ? `${parts[0]}:${parts[1]}` : item.coordinate;
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }
  return [...groups.entries()]
    .map(([key, versions]) => ({
      key,
      versions: versions.sort((a, b) =>
        b.coordinate.localeCompare(a.coordinate),
      ),
    }))
    .sort((a, b) => a.key.localeCompare(b.key));
}

export function mavenVersionKey(
  version: ArtifactSummary,
  index: number,
): string {
  return `${version.coordinate}-${version.buildNumber ?? index}`;
}

export function conanReferenceParts(reference: string): {
  key: string;
  version: string;
} {
  const canonical = reference.split("/");
  if (canonical.length >= 4 && !reference.includes("@")) {
    return {
      key: `${canonical[0]}/${canonical[2]}/${canonical.slice(3).join("/")}`,
      version: canonical[1],
    };
  }
  const [nameAndVersion, channel = ""] = reference.split("@", 2);
  const separator = nameAndVersion.indexOf("/");
  if (separator < 0) return { key: reference, version: reference };
  const name = nameAndVersion.slice(0, separator);
  const version = nameAndVersion.slice(separator + 1);
  return { key: channel ? `${name}@${channel}` : name, version };
}

export function conanArtifactGroups(
  items: ArtifactSummary[],
): ConanArtifactGroup[] {
  const groups = new Map<string, ArtifactSummary[]>();
  for (const item of items) {
    const { key } = conanReferenceParts(item.coordinate);
    const current = groups.get(key) ?? [];
    if (!current.some((entry) => entry.coordinate === item.coordinate)) {
      current.push(item);
    }
    groups.set(key, current);
  }
  return [...groups.entries()]
    .map(([key, versions]) => ({
      key,
      versions: versions.sort((a, b) => {
        const left = conanReferenceParts(a.coordinate).version;
        const right = conanReferenceParts(b.coordinate).version;
        return right.localeCompare(left, undefined, {
          numeric: true,
          sensitivity: "base",
        });
      }),
    }))
    .sort((a, b) => a.key.localeCompare(b.key));
}

export function artifactBrowseParams(
  current: URLSearchParams,
  selection: ArtifactBrowseSelection,
): URLSearchParams {
  const next = new URLSearchParams(current);
  next.set("artifact", selection.coordinate);
  setOptionalParam(
    next,
    "build",
    selection.buildNumber && selection.buildNumber > 0
      ? String(selection.buildNumber)
      : undefined,
  );
  setOptionalParam(next, "tag", selection.tag);
  setOptionalParam(next, "revision", selection.revision);
  return next;
}

export function artifactBrowsePath(
  current: URLSearchParams,
  selection: ArtifactBrowseSelection,
): string {
  return `/browse?${artifactBrowseParams(current, selection).toString()}`;
}

export function clearArtifactBrowseParams(
  current: URLSearchParams,
): URLSearchParams {
  const next = new URLSearchParams(current);
  for (const key of ["artifact", "build", "tag", "revision"]) {
    next.delete(key);
  }
  return next;
}

function setOptionalParam(
  params: URLSearchParams,
  key: string,
  value?: string,
) {
  if (value) params.set(key, value);
  else params.delete(key);
}
