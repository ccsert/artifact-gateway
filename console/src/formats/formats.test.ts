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
