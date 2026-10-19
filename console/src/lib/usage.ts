import type { Format } from "../client";
import type { Translate } from "./preferences";
import { decodeRawPathForDisplay, rawResourceURL } from "./rawPath";

// Per-format "how to use" snippets an artifact page offers for copying.

export interface UsageSnippet {
  label: string;
  code: string;
}

function gatewayHost(): string {
  return window.location.host;
}
function gatewayBase(): string {
  return window.location.origin;
}

export function npmRegistryURL(repoName: string): string {
  return `${gatewayBase()}/npm/${repoName}/`;
}

export function pypiIndexURL(repoName: string): string {
  return `${gatewayBase()}/pypi/${repoName}/simple/`;
}

export function goProxyURL(repoName: string): string {
  return `${gatewayBase()}/go/${repoName}`;
}

export function aptRepositoryURL(repoName: string): string {
  return `${gatewayBase()}/apt/${repoName}`;
}

export function aptUsage(
  repoName: string,
  coordinate = "<path>",
): UsageSnippet[] {
  const source = aptRepositoryURL(repoName);
  return [
    { label: "APT source", code: `deb ${source} <suite> <component>` },
    { label: "curl", code: `curl -fsSL ${source}/${coordinate} -o package` },
  ];
}

// Maven GAV coordinate -> its usage snippets.
export interface MavenUsageOptions {
  buildNumber?: number;
  createdAt?: string;
}

function mavenSnapshotBuildVersion(
  version: string,
  options: MavenUsageOptions,
): string | null {
  if (
    !version.endsWith("-SNAPSHOT") ||
    !options.buildNumber ||
    !options.createdAt
  )
    return null;
  const date = new Date(options.createdAt);
  if (Number.isNaN(date.getTime())) return null;
  const timestamp = date
    .toISOString()
    .slice(0, 19)
    .replace(/[-:T]/g, "")
    .replace(/(\d{8})(\d{6})/, "$1.$2");
  return `${version.slice(0, -"-SNAPSHOT".length)}-${timestamp}-${options.buildNumber}`;
}

export function mavenUsage(
  t: Translate,
  repoName: string,
  coordinate: string,
  options: MavenUsageOptions = {},
): UsageSnippet[] {
  const parts = coordinate.split(":");
  const out: UsageSnippet[] = [];
  if (parts.length >= 3) {
    const [g, a, v] = parts;
    const path = `${g.replaceAll(".", "/")}/${a}/${v}`;
    const dynamicPom = `<dependency>\n  <groupId>${g}</groupId>\n  <artifactId>${a}</artifactId>\n  <version>${v}</version>\n</dependency>`;
    const fixedVersion = mavenSnapshotBuildVersion(v, options);
    if (fixedVersion) {
      out.push({
        label: t("lib.mavenDependencyDynamicSnapshot"),
        code: dynamicPom,
      });
      out.push({
        label: t("lib.mavenDependencyPinnedBuild"),
        code: `<dependency>\n  <groupId>${g}</groupId>\n  <artifactId>${a}</artifactId>\n  <version>${fixedVersion}</version>\n</dependency>`,
      });
      out.push({
        label: t("lib.buildPom"),
        code: `${gatewayBase()}/maven/${repoName}/${path}/${a}-${fixedVersion}.pom`,
      });
      out.push({
        label: t("lib.gradlePinnedBuild"),
        code: `implementation '${g}:${a}:${fixedVersion}'`,
      });
    } else {
      out.push({
        label: t("lib.mavenDependencyPomXml"),
        code: dynamicPom,
      });
      out.push({
        label: t("lib.gradleDependency"),
        code: `implementation '${g}:${a}:${v}'`,
      });
    }
    out.push({
      label: t("lib.repositoryPath"),
      code: `${gatewayBase()}/maven/${repoName}/${path}`,
    });
  }
  return out;
}

// OCI image -> docker commands.
export function ociUsage(
  t: Translate,
  repoName: string,
  image: string,
  tag?: string,
): UsageSnippet[] {
  const ref = tag
    ? `${image}${tag.startsWith("sha256:") ? "@" : ":"}${tag}`
    : image;
  const host = gatewayHost();
  return [
    {
      label: t("lib.dockerPull"),
      code: `docker pull ${host}/${repoName}/${ref}`,
    },
    {
      label: t("lib.dockerRun"),
      code: `docker run --rm ${host}/${repoName}/${ref}`,
    },
    { label: t("lib.imageReference"), code: `${host}/${repoName}/${ref}` },
  ];
}

// Conan reference -> conan commands.
export function conanUsage(
  t: Translate,
  repoName: string,
  reference: string,
): UsageSnippet[] {
  const base = gatewayBase();
  return [
    {
      label: t("lib.addRemote"),
      code: `conan remote add ${repoName} ${base}/conan/${repoName}`,
    },
    {
      label: t("lib.installPackage"),
      code: `conan install --requires=${reference} -r ${repoName}`,
    },
  ];
}

// Raw file -> download.
export function rawUsage(
  t: Translate,
  repoName: string,
  path: string,
): UsageSnippet[] {
  const url = `${gatewayBase()}${rawResourceURL(repoName, path)}`;
  const readablePath = decodeRawPathForDisplay(path);
  const filename = readablePath.slice(readablePath.lastIndexOf("/") + 1);
  const shellQuote = (value: string) => `'${value.replaceAll("'", `'"'"'`)}'`;
  return [
    { label: t("lib.downloadUrl"), code: url },
    {
      label: t("lib.curlDownload"),
      code: `curl --fail --location --output ${shellQuote(filename)} ${shellQuote(url)}`,
    },
  ];
}

export function npmUsage(
  repoName: string,
  packageName: string,
  version: string,
): UsageSnippet[] {
  const registry = npmRegistryURL(repoName);
  const scope = packageName.startsWith("@")
    ? packageName.slice(0, packageName.indexOf("/"))
    : "";
  return [
    {
      label: "npm install",
      code: `npm install ${packageName}@${version} --registry ${registry}`,
    },
    {
      label: ".npmrc",
      code: scope ? `${scope}:registry=${registry}` : `registry=${registry}`,
    },
  ];
}

export function pypiUsage(
  repoName: string,
  project: string,
  version: string,
): UsageSnippet[] {
  const index = pypiIndexURL(repoName);
  return [
    {
      label: "pip install",
      code: `pip install ${project}==${version} --index-url ${index}`,
    },
    {
      label: "requirements.txt",
      code: `--index-url ${index}\n${project}==${version}`,
    },
  ];
}

export function goUsage(
  repoName: string,
  modulePath: string,
  version: string,
): UsageSnippet[] {
  const proxy = goProxyURL(repoName);
  return [
    {
      label: "GOPROXY",
      code: `go env -w GOPROXY=${proxy}`,
    },
    {
      label: "go mod download",
      code: `GOPROXY=${proxy} go mod download ${modulePath}@${version}`,
    },
    {
      label: "go get",
      code: `GOPROXY=${proxy} go get ${modulePath}@${version}`,
    },
  ];
}

export function usageFor(
  t: Translate,
  format: Format | string,
  repoName: string,
  coordinate: string,
  tag?: string,
  mavenOptions?: MavenUsageOptions,
): UsageSnippet[] {
  switch (format) {
    case "maven":
      return mavenUsage(t, repoName, coordinate, mavenOptions);
    case "oci":
      return ociUsage(t, repoName, coordinate, tag);
    case "conan":
      return conanUsage(t, repoName, coordinate);
    case "raw":
      return rawUsage(t, repoName, coordinate);
    case "npm":
      return npmUsage(repoName, coordinate, tag ?? "latest");
    case "pypi":
      return pypiUsage(repoName, coordinate, tag ?? "latest");
    case "go":
      return goUsage(repoName, coordinate, tag ?? "latest");
    case "apt":
      return aptUsage(repoName, coordinate);
    default:
      return [];
  }
}

// Maven coordinate -> group:artifact, used to group versions.
export function mavenGA(coordinate: string): string | null {
  const parts = coordinate.split(":");
  return parts.length >= 3 ? `${parts[0]}:${parts[1]}` : null;
}

// Maven 坐标 → version
export function mavenVersion(coordinate: string): string | null {
  const parts = coordinate.split(":");
  return parts.length >= 3 ? parts[2] : null;
}
