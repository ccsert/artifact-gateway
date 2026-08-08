import { describe, expect, it } from "vitest";
import {
  artifactBrowsePath,
  conanArtifactGroups,
  conanReferenceParts,
  mavenArtifactGroups,
  mavenVersionKey,
} from "./publicBrowseModel";

describe("public artifact version model", () => {
  it("groups Maven versions while keeping snapshot builds individually addressable", () => {
    const groups = mavenArtifactGroups([
      {
        coordinate: "com.example:demo:1.1-SNAPSHOT",
        buildNumber: 42,
      },
      { coordinate: "com.example:other:3.0.0" },
      { coordinate: "com.example:demo:1.0.0" },
      {
        coordinate: "com.example:demo:1.1-SNAPSHOT",
        buildNumber: 41,
      },
    ]);

    expect(groups.map((group) => group.key)).toEqual([
      "com.example:demo",
      "com.example:other",
    ]);
    expect(groups[0].versions).toHaveLength(3);
    expect(mavenVersionKey(groups[0].versions[0], 0)).not.toBe(
      mavenVersionKey(groups[0].versions[1], 1),
    );
  });

  it("groups Conan references by package identity instead of version", () => {
    expect(conanReferenceParts("fmt/11.0.2@acme/stable")).toEqual({
      key: "fmt@acme/stable",
      version: "11.0.2",
    });
    expect(
      conanArtifactGroups([
        { coordinate: "fmt/10.2.1@acme/stable" },
        { coordinate: "fmt/11.0.2@acme/stable" },
        { coordinate: "zlib/1.3.1@acme/stable" },
      ]).map((group) => [
        group.key,
        group.versions.map((version) => version.coordinate),
      ]),
    ).toEqual([
      ["fmt@acme/stable", ["fmt/11.0.2@acme/stable", "fmt/10.2.1@acme/stable"]],
      ["zlib@acme/stable", ["zlib/1.3.1@acme/stable"]],
    ]);
  });

  it("builds an exact version URL without losing repository search state", () => {
    const current = new URLSearchParams({
      repository: "repo-1",
      q: "demo",
      artifact: "old/artifact",
      revision: "old-revision",
    });

    expect(
      artifactBrowsePath(current, {
        coordinate: "com.example:demo:1.1-SNAPSHOT",
        buildNumber: 42,
      }),
    ).toBe(
      "/browse?repository=repo-1&q=demo&artifact=com.example%3Ademo%3A1.1-SNAPSHOT&build=42",
    );
    expect(
      artifactBrowsePath(current, {
        coordinate: "library/postgres",
        tag: "17.2",
      }),
    ).toBe(
      "/browse?repository=repo-1&q=demo&artifact=library%2Fpostgres&tag=17.2",
    );
  });
});
