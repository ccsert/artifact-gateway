import type { Format } from "../client";
import { goProxyURL, npmRegistryURL, pypiIndexURL } from "../lib/usage";
import type { FormatDescriptor } from "./types";

const base = {
  packageVersions: false,
  pathAddressed: false,
  encodedPaths: false,
  nameListing: false,
  directoryBrowse: [],
  upstreamHostAllowList: true,
  securityColumn: "never",
  searchDetail: true,
  publicDetail: false,
  publishGuide: false,
} as const satisfies Partial<FormatDescriptor>;

/**
 * Every format the Gateway serves. The `Record<Format, …>` type makes a new
 * server format a compile error here until it is described.
 */
export const FORMAT_DESCRIPTORS: Record<Format, FormatDescriptor> = {
  oci: {
    ...base,
    format: "oci",
    artifactColumn: ["名称", "Name"],
    searchPlaceholder: ["按镜像名前缀过滤…", "Filter by image name prefix…"],
    nameListing: true,
    upstreamHostAllowList: false,
    publishGuide: true,
  },
  maven: {
    ...base,
    format: "maven",
    artifactColumn: ["制品", "Artifact"],
    searchPlaceholder: ["搜索 GAV 坐标…", "Search GAV coordinates…"],
    directoryBrowse: ["hosted", "proxy", "group"],
    upstreamHostAllowList: false,
    securityColumn: "versionSearch",
    searchDetail: false,
    publishGuide: true,
  },
  npm: {
    ...base,
    format: "npm",
    artifactColumn: ["包", "Package"],
    searchPlaceholder: ["按包名前缀过滤…", "Filter by package name prefix…"],
    packageVersions: true,
    securityColumn: "always",
    publicDetail: true,
    groupEndpoint: (name) => ({
      value: npmRegistryURL(name),
      label: `/npm/${name}/`,
    }),
    publishGuide: true,
  },
  pypi: {
    ...base,
    format: "pypi",
    artifactColumn: ["项目", "Project"],
    searchPlaceholder: ["按项目名前缀过滤…", "Filter by project name prefix…"],
    packageVersions: true,
    securityColumn: "always",
    publicDetail: true,
    groupEndpoint: (name) => ({
      value: pypiIndexURL(name),
      label: `/pypi/${name}/simple/`,
    }),
    publishGuide: true,
  },
  go: {
    ...base,
    format: "go",
    artifactColumn: ["模块", "Module"],
    searchPlaceholder: ["按模块路径前缀过滤…", "Filter by module path prefix…"],
    packageVersions: true,
    securityColumn: "always",
    publicDetail: true,
    groupEndpoint: (name) => ({
      value: goProxyURL(name),
      label: `/go/${name}`,
    }),
  },
  cargo: {
    ...base,
    format: "cargo",
    artifactColumn: ["Crate", "Crate"],
    searchPlaceholder: ["搜索 crate 名称…", "Search crate names…"],
    packageVersions: true,
    directoryBrowse: ["hosted"],
    publicDetail: true,
    groupEndpoint: (name) => ({
      value: `sparse+${window.location.origin}/cargo/${name}/`,
      label: `/cargo/${name}/`,
    }),
    publishGuide: true,
  },
  conan: {
    ...base,
    format: "conan",
    artifactColumn: ["名称", "Name"],
    searchPlaceholder: ["按引用名过滤…", "Filter by reference…"],
    nameListing: true,
  },
  raw: {
    ...base,
    format: "raw",
    artifactColumn: ["文件路径", "File path"],
    searchPlaceholder: ["搜索路径…", "Search paths…"],
    pathAddressed: true,
    encodedPaths: true,
    directoryBrowse: ["hosted", "proxy", "group"],
    securityColumn: "always",
  },
  apt: {
    ...base,
    format: "apt",
    artifactColumn: ["APT 路径", "APT path"],
    searchPlaceholder: [
      "按 dists/ 或 pool/ 路径过滤…",
      "Filter by dists/ or pool/ path…",
    ],
    pathAddressed: true,
    publicDetail: true,
    searchDetail: false,
  },
};
