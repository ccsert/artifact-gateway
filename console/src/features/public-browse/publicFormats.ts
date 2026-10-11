import type { PublicRepositoryFormat } from "../../lib/publicRepositoryUsage";

export const PUBLIC_FORMAT_ORDER: PublicRepositoryFormat[] = [
  "oci",
  "maven",
  "npm",
  "pypi",
  "go",
  "cargo",
  "apt",
  "conan",
  "raw",
];

export const PUBLIC_FORMAT_STYLE: Record<
  PublicRepositoryFormat,
  { icon: string }
> = {
  oci: {
    icon: "OCI",
  },
  maven: {
    icon: "MVN",
  },
  npm: {
    icon: "NPM",
  },
  pypi: {
    icon: "PY",
  },
  go: {
    icon: "GO",
  },
  cargo: {
    icon: "CRG",
  },
  apt: {
    icon: "APT",
  },
  conan: {
    icon: "C++",
  },
  raw: {
    icon: "RAW",
  },
};

type Localize = (chinese: string, english: string) => string;

const USAGE_LABELS: Record<PublicRepositoryFormat, [string, string]> = {
  oci: ["注册 OCI 镜像源", "Register an OCI registry"],
  maven: ["注册 Maven 仓库源", "Register a Maven repository"],
  conan: ["注册 Conan remote", "Register a Conan remote"],
  npm: ["注册 npm 仓库源", "Register an npm registry"],
  pypi: ["注册 PyPI 仓库源", "Register a PyPI repository"],
  go: ["注册 Go Module Proxy", "Register a Go module proxy"],
  cargo: ["注册 Cargo 仓库源", "Register a Cargo registry"],
  apt: ["注册 APT 软件源", "Register an APT source"],
  raw: ["注册 Raw 源地址", "Register a Raw source"],
};

/** The heading of the repository-level usage card. */
export function publicUsageLabel(
  format: PublicRepositoryFormat | undefined,
  text: Localize,
) {
  const [chinese, english] = USAGE_LABELS[format ?? "raw"];
  return text(chinese, english);
}
