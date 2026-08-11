import { describe, expect, it } from "vitest";
import {
  RESOURCE_PREFIX_EXAMPLES,
  buildResourcePrefix,
  inferResourcePrefixFormat,
  parseResourcePrefix,
} from "./ResourcePrefixEditor";

describe("resource prefix editor", () => {
  it("builds canonical prefixes for structured formats", () => {
    expect(buildResourcePrefix("maven", ["com.example", "gateway"])).toBe(
      "com.example:gateway",
    );
    expect(buildResourcePrefix("oci", ["team", "backend"])).toBe(
      "team/backend",
    );
    expect(buildResourcePrefix("conan", ["pkg", "1.0"])).toBe("pkg/1.0/");
    expect(buildResourcePrefix("npm", ["team", "client"])).toBe("@team/client");
  });

  it("round-trips the common structured prefixes", () => {
    expect(parseResourcePrefix("maven", "com.example:gateway")).toEqual([
      "com.example",
      "gateway",
    ]);
    expect(parseResourcePrefix("oci", "team/backend")).toEqual([
      "team",
      "backend",
    ]);
    expect(parseResourcePrefix("npm", "@team/client")).toEqual([
      "@team",
      "client",
    ]);
  });

  it("offers examples for every supported format and infers distinct syntax", () => {
    for (const examples of Object.values(RESOURCE_PREFIX_EXAMPLES)) {
      expect(examples.length).toBeGreaterThan(0);
    }
    expect(inferResourcePrefixFormat("@team/", ["maven", "npm"])).toBe("npm");
    expect(inferResourcePrefixFormat("dists/bookworm/", ["apt", "raw"])).toBe(
      "apt",
    );
  });
});
