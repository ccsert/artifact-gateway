import { describe, expect, it } from "vitest";
import type { GlobalArtifactSearchHit } from "../client";
import { artifactTarget } from "./Search";

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
});
