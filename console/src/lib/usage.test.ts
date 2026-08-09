import { describe, expect, it } from "vitest";
import {
  npmRegistryURL,
  npmUsage,
  pypiIndexURL,
  pypiUsage,
  usageFor,
} from "./usage";

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

describe("PyPI usage", () => {
  it("builds exact pip and requirements snippets", () => {
    const snippets = pypiUsage("python", "gateway-widget", "2.0.0");
    expect(snippets[0].code).toContain(
      "pip install gateway-widget==2.0.0 --index-url",
    );
    expect(snippets[1].code).toContain("gateway-widget==2.0.0");
    expect(
      usageFor("pypi", "python", "gateway-widget", "1.0.0")[0].code,
    ).toContain("gateway-widget==1.0.0");
    expect(pypiIndexURL("python-all")).toBe(
      `${window.location.origin}/pypi/python-all/simple/`,
    );
  });
});
