import { describe, expect, it } from "vitest";
import type { Format } from "../client";
import { FORMAT_DESCRIPTORS } from "./descriptors";
import {
  formatDescriptor,
  hasPackageVersions,
  supportsDirectoryBrowse,
} from "./index";

const FORMATS = Object.keys(FORMAT_DESCRIPTORS) as Format[];
const pick = (predicate: (format: Format) => boolean) =>
  FORMATS.filter(predicate).sort();

describe("format registry", () => {
  it("describes each format under its own name", () => {
    for (const format of FORMATS) {
      expect(FORMAT_DESCRIPTORS[format].format).toBe(format);
    }
  });

  it("leaves formats this Console does not know to the generic presentation", () => {
    expect(formatDescriptor("nuget")).toBeUndefined();
    expect(formatDescriptor("toString")).toBeUndefined();
    expect(formatDescriptor(undefined)).toBeUndefined();
    expect(hasPackageVersions("nuget")).toBe(false);
    expect(supportsDirectoryBrowse("nuget", "hosted")).toBe(false);
  });

  // The traits replace inline format checks; these pin them to the sets those
  // checks named, so moving a page onto the registry changes no behaviour.
  it("keeps the package, path and name-listing families", () => {
    expect(pick(hasPackageVersions)).toEqual(["cargo", "go", "npm", "pypi"]);
    expect(pick((f) => FORMAT_DESCRIPTORS[f].pathAddressed)).toEqual([
      "apt",
      "raw",
    ]);
    expect(pick((f) => FORMAT_DESCRIPTORS[f].encodedPaths)).toEqual(["raw"]);
    expect(pick((f) => FORMAT_DESCRIPTORS[f].nameListing)).toEqual([
      "conan",
      "oci",
    ]);
  });

  it("shows version columns for Maven and the package formats", () => {
    expect(pick((f) => FORMAT_DESCRIPTORS[f].versionColumns)).toEqual([
      "cargo",
      "go",
      "maven",
      "npm",
      "pypi",
    ]);
  });

  it("keeps per-format table and upload traits", () => {
    expect(pick((f) => FORMAT_DESCRIPTORS[f].assetColumns)).toEqual(["apt"]);
    expect(pick((f) => FORMAT_DESCRIPTORS[f].consoleUpload)).toEqual(["raw"]);
    expect(pick((f) => Boolean(FORMAT_DESCRIPTORS[f].emptyHint.proxy))).toEqual(
      ["apt", "go", "maven", "npm", "pypi"],
    );
    expect(
      pick((f) => Boolean(FORMAT_DESCRIPTORS[f].emptyHint.hosted)),
    ).toEqual(["apt"]);
  });

  it("keeps each single-format behaviour on its format", () => {
    const only = (trait: (format: Format) => unknown) =>
      pick((format) => Boolean(trait(format)));
    const d = (format: Format) => FORMAT_DESCRIPTORS[format];
    expect(only((f) => d(f).strictPublicationSetting)).toEqual(["maven"]);
    expect(only((f) => d(f).upstreamAuthentication)).toEqual(["go"]);
    expect(only((f) => d(f).separateAssetPath)).toEqual(["maven"]);
    expect(only((f) => d(f).signedIndexes)).toEqual(["apt"]);
    expect(only((f) => d(f).hostedOnlyJobs)).toEqual(["apt"]);
    expect(only((f) => d(f).distributionSuite)).toEqual(["apt"]);
    expect(only((f) => d(f).digestReferences)).toEqual(["oci"]);
    expect(only((f) => d(f).groupKey)).toEqual(["maven"]);
    expect(only((f) => d(f).retentionModel === "snapshots")).toEqual(["maven"]);
    expect(only((f) => d(f).retentionModel === "paths")).toEqual(["raw"]);
  });

  it("describes public browse per format", () => {
    const d = (format: Format) => FORMAT_DESCRIPTORS[format];
    expect(pick((f) => d(f).publicArtifactColumn === "image")).toEqual(["oci"]);
    expect(pick((f) => d(f).publicArtifactColumn === "aptPath")).toEqual([
      "apt",
    ]);
    expect(pick((f) => d(f).publicVersionSource === "ociTags")).toEqual([
      "oci",
    ]);
    expect(pick((f) => d(f).publicVersionSource === "conanRevisions")).toEqual([
      "conan",
    ]);
    expect(pick((f) => d(f).publicGrouping === "mavenBuilds")).toEqual([
      "maven",
    ]);
    expect(pick((f) => d(f).publicGrouping === "conanRevisions")).toEqual([
      "conan",
    ]);
    expect(d("maven").groupKey?.("org.example:core:1.0")).toBe(
      "org.example:core",
    );
  });

  it("offers a directory view for Maven and Raw, and for hosted Cargo", () => {
    expect(pick((f) => supportsDirectoryBrowse(f, "hosted"))).toEqual([
      "cargo",
      "maven",
      "raw",
    ]);
    expect(pick((f) => supportsDirectoryBrowse(f, "proxy"))).toEqual([
      "maven",
      "raw",
    ]);
    expect(pick((f) => supportsDirectoryBrowse(f, "group"))).toEqual([
      "maven",
      "raw",
    ]);
  });

  it("offers publish guides for the formats that had one", () => {
    expect(pick((f) => FORMAT_DESCRIPTORS[f].publishGuide)).toEqual([
      "cargo",
      "maven",
      "npm",
      "oci",
      "pypi",
    ]);
  });

  it("opens shared artifact detail where each page showed it", () => {
    expect(pick((f) => FORMAT_DESCRIPTORS[f].searchDetail)).toEqual([
      "cargo",
      "conan",
      "go",
      "npm",
      "oci",
      "pypi",
      "raw",
    ]);
    expect(pick((f) => FORMAT_DESCRIPTORS[f].publicDetail)).toEqual([
      "apt",
      "cargo",
      "go",
      "npm",
      "pypi",
    ]);
  });

  it("asks for an upstream host allow-list except for OCI and Maven", () => {
    expect(pick((f) => FORMAT_DESCRIPTORS[f].upstreamHostAllowList)).toEqual([
      "apt",
      "cargo",
      "conan",
      "go",
      "npm",
      "pypi",
      "raw",
    ]);
  });

  it("shows security summaries where the artifact list carries them", () => {
    expect(
      pick((f) => FORMAT_DESCRIPTORS[f].securityColumn === "always"),
    ).toEqual(["go", "npm", "pypi", "raw"]);
    expect(
      pick((f) => FORMAT_DESCRIPTORS[f].securityColumn === "versionSearch"),
    ).toEqual(["maven"]);
  });
});
