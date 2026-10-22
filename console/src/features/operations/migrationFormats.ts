import type { MigrationSourceMapping } from "../../client";

export type MigrationTargetFormat = MigrationSourceMapping["format"];

/** Source storage identities are translated explicitly; unknown formats stay absent. */
export function migrationTargetFormat(
  sourceFormat: string,
): MigrationTargetFormat | undefined {
  switch (sourceFormat) {
    case "maven2":
      return "maven";
    case "docker":
      return "oci";
    case "conan":
    case "npm":
    case "raw":
      return sourceFormat;
  }
}

export function isMigrationTargetFormat(
  format: string,
): format is MigrationTargetFormat {
  return ["maven", "raw", "oci", "conan", "npm"].includes(format);
}

/** Report URLs are protocol roots, independent of the commands shown in the UI. */
export function migrationTargetURL(
  format: MigrationTargetFormat,
  repositoryName: string,
  origin: string,
): string {
  const name = encodeURIComponent(repositoryName);
  switch (format) {
    case "maven":
      return `${origin}/maven/${name}`;
    case "raw":
      return `${origin}/raw/${name}/`;
    case "oci":
      return `${origin}/v2/${name}/`;
    case "conan":
      return `${origin}/conan/${name}`;
    case "npm":
      return `${origin}/npm/${name}/`;
  }
}
