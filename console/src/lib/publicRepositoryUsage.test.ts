import { describe, expect, it } from "vitest";
import {
  publicRepositoryUsage,
  type PublicRepositoryFormat,
} from "./publicRepositoryUsage";

const english = (_chinese: string, value: string) => value;

function usage(format: PublicRepositoryFormat) {
  return publicRepositoryUsage({
    format,
    repositoryName: "releases",
    origin: "https://gateway.test",
    host: "gateway.test",
    text: english,
  });
}

describe("public repository usage", () => {
  it("builds complete Maven and OCI setup without reading browser globals", () => {
    expect(usage("maven")).toEqual([
      {
        label: "Maven repository URL",
        code: "https://gateway.test/maven/releases",
      },
      {
        label: "settings.xml",
        code: "<repository>\n  <id>releases</id>\n  <url>https://gateway.test/maven/releases</url>\n</repository>",
      },
      {
        label: "Gradle repositories",
        code: 'maven { url = uri("https://gateway.test/maven/releases") }',
      },
    ]);
    expect(usage("oci")).toEqual([
      { label: "OCI registry address", code: "gateway.test/releases" },
      {
        label: "Docker registry setup",
        code: "docker login gateway.test\n# Image prefix: gateway.test/releases/",
      },
    ]);
  });

  it.each([
    [
      "conan",
      "conan remote add releases https://gateway.test/conan/v2/releases",
    ],
    ["npm", "https://gateway.test/npm/releases/"],
    ["pypi", "https://gateway.test/pypi/releases/simple/"],
    ["go", "go env -w GOPROXY=https://gateway.test/go/releases"],
    ["apt", "https://gateway.test/apt/releases"],
    ["raw", "https://gateway.test/raw/releases/"],
  ] as const)("builds the %s repository entry point", (format, expected) => {
    expect(usage(format)[0].code).toBe(expected);
  });

  it("uses the supplied localizer for localized labels", () => {
    expect(
      publicRepositoryUsage({
        format: "go",
        repositoryName: "releases",
        origin: "https://gateway.test",
        host: "gateway.test",
        text: (chinese) => chinese,
      })[1].label,
    ).toBe("临时使用");
  });
});
