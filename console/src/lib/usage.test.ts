import { describe, expect, it } from "vitest";
import { npmRegistryURL, npmUsage, usageFor } from "./usage";

describe("npm usage", () => {
  it("builds exact install and scoped registry snippets", () => {
    const snippets = npmUsage("npm-releases", "@scope/widget", "2.0.0-beta.1");

    expect(snippets).toEqual([
      {
        label: "npm install",
        code: expect.stringContaining(
          "npm install @scope/widget@2.0.0-beta.1 --registry",
        ),
      },
      {
        label: ".npmrc",
        code: expect.stringContaining("@scope:registry="),
      },
    ]);
    expect(
      usageFor("npm", "npm-releases", "widget", "1.4.0")[0].code,
    ).toContain("widget@1.4.0");
    expect(npmRegistryURL("all-packages")).toBe(
      `${window.location.origin}/npm/all-packages/`,
    );
  });
});
