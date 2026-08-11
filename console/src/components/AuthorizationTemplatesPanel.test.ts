import { describe, expect, it } from "vitest";
import {
  AUTHORIZATION_TEMPLATE_PRESETS,
  RESOURCE_PREFIX_EXAMPLES,
} from "./AuthorizationTemplatesPanel";

describe("authorization template presets", () => {
  it("provides least-privilege references without choosing a real principal", () => {
    expect(AUTHORIZATION_TEMPLATE_PRESETS.length).toBeGreaterThanOrEqual(4);
    expect(
      AUTHORIZATION_TEMPLATE_PRESETS.map((preset) => preset.grants[0]?.scopes),
    ).toEqual(
      expect.arrayContaining([
        ["repositories:read"],
        ["repositories:write"],
        ["repositories:intelligence"],
        ["repositories:admin"],
      ]),
    );
    expect(
      AUTHORIZATION_TEMPLATE_PRESETS.every((preset) =>
        preset.grants.every((grant) => grant.principal === ""),
      ),
    ).toBe(true);
  });

  it("offers resource prefix examples for every supported artifact format", () => {
    for (const format of [
      "maven",
      "oci",
      "conan",
      "raw",
      "npm",
      "pypi",
      "go",
      "apt",
    ] as const) {
      expect(RESOURCE_PREFIX_EXAMPLES[format]?.length).toBeGreaterThan(0);
    }
  });
});
