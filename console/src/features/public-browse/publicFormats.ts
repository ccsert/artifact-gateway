import type { Translate } from "../../lib/preferences";
import type { PublicBrowseMessageKey } from "../../messages/public-browse.zh-CN";
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

const USAGE_LABELS: Record<PublicRepositoryFormat, PublicBrowseMessageKey> = {
  oci: "publicBrowse.registerAnOciRegistry",
  maven: "publicBrowse.registerAMavenRepository",
  conan: "publicBrowse.registerAConanRemote",
  npm: "publicBrowse.registerAnNpmRegistry",
  pypi: "publicBrowse.registerAPypiRepository",
  go: "publicBrowse.registerAGoModuleProxy",
  cargo: "publicBrowse.registerACargoRegistry",
  apt: "publicBrowse.registerAnAptSource",
  raw: "publicBrowse.registerARawSource",
};

/** The heading of the repository-level usage card. */
export function publicUsageLabel(
  format: PublicRepositoryFormat | undefined,
  t: Translate,
) {
  return t(USAGE_LABELS[format ?? "raw"]);
}
