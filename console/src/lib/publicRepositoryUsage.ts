import type { UsageSnippet } from "./usage";

export type PublicRepositoryFormat =
  "oci" | "maven" | "conan" | "raw" | "npm" | "pypi" | "go" | "apt";

export interface PublicRepositoryUsageContext {
  format: PublicRepositoryFormat;
  repositoryName: string;
  origin: string;
  host: string;
  text: (chinese: string, english: string) => string;
}

export function publicRepositoryUsage({
  format,
  repositoryName,
  origin,
  host,
  text,
}: PublicRepositoryUsageContext): UsageSnippet[] {
  if (format === "maven") {
    const url = `${origin}/maven/${repositoryName}`;
    return [
      { label: text("Maven 仓库 URL", "Maven repository URL"), code: url },
      {
        label: "settings.xml",
        code: `<repository>\n  <id>${repositoryName}</id>\n  <url>${url}</url>\n</repository>`,
      },
      { label: "Gradle repositories", code: `maven { url = uri("${url}") }` },
    ];
  }
  if (format === "oci") {
    return [
      {
        label: text("OCI Registry 地址", "OCI registry address"),
        code: `${host}/${repositoryName}`,
      },
      {
        label: text("Docker Registry 配置", "Docker registry setup"),
        code: text(
          `docker login ${host}\n# 镜像前缀：${host}/${repositoryName}/`,
          `docker login ${host}\n# Image prefix: ${host}/${repositoryName}/`,
        ),
      },
    ];
  }
  if (format === "conan") {
    return [
      {
        label: text("Conan remote 地址", "Conan remote address"),
        code: `conan remote add ${repositoryName} ${origin}/conan/v2/${repositoryName}`,
      },
    ];
  }
  if (format === "npm") {
    const registry = `${origin}/npm/${repositoryName}/`;
    return [
      { label: text("npm Registry 地址", "npm registry URL"), code: registry },
      { label: ".npmrc", code: `registry=${registry}` },
    ];
  }
  if (format === "pypi") {
    const index = `${origin}/pypi/${repositoryName}/simple/`;
    return [
      { label: "PyPI Simple API", code: index },
      { label: "pip", code: `pip config set global.index-url ${index}` },
    ];
  }
  if (format === "go") {
    const proxy = `${origin}/go/${repositoryName}`;
    return [
      { label: "GOPROXY", code: `go env -w GOPROXY=${proxy}` },
      {
        label: text("临时使用", "One-off usage"),
        code: `GOPROXY=${proxy} go mod download`,
      },
    ];
  }
  if (format === "apt") {
    const source = `${origin}/apt/${repositoryName}`;
    return [
      { label: text("APT 源地址", "APT source URL"), code: source },
      { label: "sources.list", code: `deb ${source} <suite> <component>` },
      {
        label: text("下载制品", "Download an artifact"),
        code: `curl -fsSL ${source}/pool/<component>/<path>/<package>.deb -o package.deb`,
      },
    ];
  }
  return [
    {
      label: "Raw 仓库地址",
      code: `${origin}/raw/${repositoryName}/`,
    },
  ];
}
