import { useQuery } from "@tanstack/react-query";
import { useAuth } from "../../lib/auth";
import { protocolKeys } from "../../lib/query/keys";
import type { MessageKey } from "../../messages";
import { LocalizedError } from "../../messages/translate";

/**
 * Package detail pages read the format's own client protocol, the same
 * endpoints npm, pip, go and cargo use. These reads:
 *
 * - send the signed-in token when there is one (the pages also render in
 *   anonymous public browse);
 * - report a refusal on the page and never end a session
 *   (`authFailure: "ignore"`), as before the move onto queries;
 * - re-read on every opening (`staleTime: 0`);
 * - fail with a `LocalizedError` that keeps the HTTP status, so the shared
 *   retry policy retries a 5xx once and stops on anything else.
 */
const protocolQuery = {
  meta: { authFailure: "ignore" as const },
  staleTime: 0,
};

async function protocolRead(
  url: string,
  token: string,
  failure: MessageKey,
  signal: AbortSignal,
  init: { accept?: string; method?: "HEAD" } = {},
): Promise<Response> {
  const response = await fetch(url, {
    signal,
    method: init.method,
    credentials: "include",
    headers: {
      ...(init.accept ? { Accept: init.accept } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  });
  if (!response.ok)
    throw new LocalizedError(
      failure,
      { status: response.status },
      { status: response.status },
    );
  return response;
}

// ------------------------------------------------------------------ npm

export interface NpmVersionManifest {
  name: string;
  version: string;
  description?: string;
  deprecated?: string;
  license?: string | { type?: string };
  dependencies?: Record<string, string>;
  dist?: {
    tarball?: string;
    integrity?: string;
    shasum?: string;
  };
  _artifactGateway?: {
    digest?: string;
    publisher?: string;
    size?: number;
    source?: "hosted" | "proxy";
    cacheStatus?: "metadata" | "cached";
    cachedAt?: string;
  };
}

export interface NpmPackument {
  _id?: string;
  _rev?: string;
  name: string;
  "dist-tags": Record<string, string>;
  versions: Record<string, NpmVersionManifest>;
  time?: Record<string, string>;
}

/** The package document (`GET /npm/<repo>/<package>`). */
export function useNpmPackument(repoName: string, packageName: string) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.npmPackument(repoName, packageName),
    queryFn: async ({ signal }) => {
      const response = await protocolRead(
        `/npm/${encodeURIComponent(repoName)}/${encodeURIComponent(packageName)}`,
        token,
        "artifactDetail.failedToLoadNpmPackage",
        signal,
      );
      return (await response.json()) as NpmPackument;
    },
  });
}

// ----------------------------------------------------------------- PyPI

export interface PyPIFile {
  filename: string;
  url: string;
  hashes: { sha256?: string };
  yanked?: boolean | string;
  "requires-python"?: string;
  "_artifact-gateway"?: {
    version?: string;
    size?: number;
    publisher?: string;
    "created-at"?: string;
    "cached-at"?: string;
    cached?: boolean;
    "file-type"?: string;
    "python-version"?: string;
  };
}

export interface PyPISimpleProject {
  name: string;
  files: PyPIFile[];
}

/** The project's Simple API JSON (PEP 691). */
export function usePyPIProject(repoName: string, project: string) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.pypiProject(repoName, project),
    queryFn: async ({ signal }) => {
      const response = await protocolRead(
        `/pypi/${encodeURIComponent(repoName)}/simple/${encodeURIComponent(project)}/`,
        token,
        "artifactDetail.failedToLoadPyPIProject",
        signal,
        { accept: "application/vnd.pypi.simple.v1+json" },
      );
      return (await response.json()) as PyPISimpleProject;
    },
  });
}

// ------------------------------------------------------------------- Go

export interface GoModuleInfo {
  Version: string;
  Time: string;
}

/**
 * Go proxy paths escape each uppercase letter as `!` plus its lowercase form.
 */
export function escapeGoValue(value: string): string {
  return value
    .split("")
    .map((character) =>
      character >= "A" && character <= "Z"
        ? `!${character.toLowerCase()}`
        : character,
    )
    .join("");
}

export function goModuleBase(repoName: string, modulePath: string): string {
  const escaped = escapeGoValue(modulePath)
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");
  return `/go/${encodeURIComponent(repoName)}/${escaped}`;
}

function goVersionPath(base: string, version: string, kind: string) {
  return `${base}/@v/${encodeURIComponent(escapeGoValue(version))}.${kind}`;
}

/** The module's versions (`@v/list`), newest first. */
export function useGoModuleVersions(repoName: string, modulePath: string) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.goVersions(repoName, modulePath),
    queryFn: async ({ signal }) => {
      const response = await protocolRead(
        `${goModuleBase(repoName, modulePath)}/@v/list`,
        token,
        "artifactDetail.failedToLoadGoModuleVersions",
        signal,
      );
      return (await response.text())
        .split(/\s+/)
        .map((version) => version.trim())
        .filter(Boolean)
        .reverse();
    },
  });
}

/** One version's `.info` document. */
export function useGoModuleInfo(
  repoName: string,
  modulePath: string,
  version: string,
) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.goInfo(repoName, modulePath, version),
    queryFn: async ({ signal }) => {
      const response = await protocolRead(
        goVersionPath(goModuleBase(repoName, modulePath), version, "info"),
        token,
        "artifactDetail.failedToLoadVersionInfo",
        signal,
      );
      return (await response.json()) as GoModuleInfo;
    },
    enabled: Boolean(version),
  });
}

/**
 * The version zip's SHA-256 from its `ETag`, read with HEAD. It is optional
 * evidence: any failure, or an ETag that is not a SHA-256, reads as unknown.
 */
export function useGoModuleDigest(
  repoName: string,
  modulePath: string,
  version: string,
) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.goDigest(repoName, modulePath, version),
    queryFn: async ({ signal }) => {
      try {
        const response = await fetch(
          goVersionPath(goModuleBase(repoName, modulePath), version, "zip"),
          {
            signal,
            method: "HEAD",
            credentials: "include",
            headers: token ? { Authorization: `Bearer ${token}` } : undefined,
          },
        );
        if (!response.ok) return null;
        const etag = response.headers.get("ETag")?.replaceAll('"', "") ?? "";
        return /^[a-f0-9]{64}$/.test(etag) ? `sha256:${etag}` : null;
      } catch (error) {
        if (signal.aborted) throw error;
        return null;
      }
    },
    enabled: Boolean(version),
    retry: false,
  });
}

// ---------------------------------------------------------------- Cargo

export interface CargoIndexEntry {
  name: string;
  vers: string;
  cksum: string;
  yanked: boolean;
}

/** A crate's sparse index path (`1/a`, `3/a/abc`, `ab/cd/abcd…`). */
export function sparsePath(name: string): string {
  const normalized = name.toLowerCase();
  if (normalized.length === 1) return `1/${normalized}`;
  if (normalized.length === 2) return `2/${normalized}`;
  if (normalized.length === 3) return `3/${normalized[0]}/${normalized}`;
  return `${normalized.slice(0, 2)}/${normalized.slice(2, 4)}/${normalized}`;
}

/** The crate's sparse index entries, newest version first. */
export function useCargoIndex(repoName: string, crateName: string) {
  const { token } = useAuth();
  return useQuery({
    ...protocolQuery,
    queryKey: protocolKeys.cargoIndex(repoName, crateName),
    queryFn: async ({ signal }) => {
      const response = await protocolRead(
        `/cargo/${encodeURIComponent(repoName)}/${sparsePath(crateName)}`,
        token,
        "artifactDetail.failedToLoadCargoVersions",
        signal,
      );
      return (await response.text())
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line) as CargoIndexEntry)
        .filter((entry) => entry.name.toLowerCase() === crateName.toLowerCase())
        .sort((left, right) =>
          right.vers.localeCompare(left.vers, undefined, { numeric: true }),
        );
    },
  });
}
