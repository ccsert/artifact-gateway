import { describe, expect, it } from "vitest";
import type { GlobalArtifactSearchHit } from "../client";
import { artifactTarget, artifactVersionSizeLabel } from "./Search";

function hit(
  overrides: Partial<GlobalArtifactSearchHit>,
): GlobalArtifactSearchHit {
  return {
    repositoryId: "00000000-0000-0000-0000-000000000001",
    repositoryName: "releases",
    format: "raw",
    matchKind: "coordinate",
    coordinate: "packages/example.tar.gz",
    ...overrides,
  };
}

describe("artifactTarget", () => {
  it("deep-links an OCI digest match to the exact manifest", () => {
    const digest = `sha256:${"a".repeat(64)}`;

    expect(
      artifactTarget(
        hit({
          format: "oci",
          matchKind: "digest",
          coordinate: "library/postgres",
          digest,
        }),
      ),
    ).toBe(
      `/repositories/00000000-0000-0000-0000-000000000001?artifact=library%2Fpostgres&reference=${encodeURIComponent(digest)}`,
    );
  });

  it("preserves a Maven snapshot build in the repository link", () => {
    expect(
      artifactTarget(
        hit({
          format: "maven",
          coordinate: "org.example:demo:1.0-SNAPSHOT",
          buildNumber: 42,
        }),
      ),
    ).toBe(
      "/repositories/00000000-0000-0000-0000-000000000001?artifact=org.example%3Ademo%3A1.0-SNAPSHOT&build=42",
    );
  });

  it("deep-links an npm hit to the exact package version", () => {
    expect(
      artifactTarget(
        hit({
          format: "npm",
          repositoryName: "npm-releases",
          coordinate: "@scope/widget",
          version: "2.0.0-beta.1",
        }),
      ),
    ).toBe(
      "/repositories/00000000-0000-0000-0000-000000000001?artifact=%40scope%2Fwidget&version=2.0.0-beta.1",
    );
  });
});

describe("artifactVersionSizeLabel", () => {
  it("shows the exact npm version together with its archive size", () => {
    expect(
      artifactVersionSizeLabel(
        hit({ format: "npm", version: "1.1.0", size: 294 }),
      ),
    ).toBe("1.1.0 · 294 B");
  });

  it("keeps the compact size-only label for other formats", () => {
    expect(artifactVersionSizeLabel(hit({ size: 1024 }))).toBe("1.0 KiB");
  });
});
