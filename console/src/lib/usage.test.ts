import { describe, expect, it } from "vitest";
import {
  aptRepositoryURL,
  aptUsage,
  goProxyURL,
  goUsage,
  npmRegistryURL,
  npmUsage,
  pypiIndexURL,
  pypiUsage,
  usageFor,
} from "./usage";

describe("APT usage", () => {
  it("builds repository registration and exact asset download snippets", () => {
    const path = "pool/main/h/hello/hello_2.10_amd64.deb";
    const snippets = aptUsage("debian", path);

    expect(snippets[0].code).toBe(
      `deb ${window.location.origin}/apt/debian <suite> <component>`,
    );
    expect(snippets[1].code).toBe(
      `curl -fsSL ${window.location.origin}/apt/debian/${path} -o package`,
    );
    expect(usageFor("apt", "debian", path)).toEqual(snippets);
    expect(aptRepositoryURL("debian")).toBe(
      `${window.location.origin}/apt/debian`,
    );
  });
});

describe("Go usage", () => {
  it("builds GOPROXY and exact module-version snippets", () => {
    const snippets = goUsage("go-all", "example.com/Acme/widget", "v1.2.3");
    expect(snippets[0].code).toBe(
      `go env -w GOPROXY=${window.location.origin}/go/go-all`,
    );
    expect(snippets[1].code).toContain(
      "go mod download example.com/Acme/widget@v1.2.3",
    );
    expect(
      usageFor("go", "go-all", "example.com/Acme/widget", "v1.2.3")[2].code,
    ).toContain("go get example.com/Acme/widget@v1.2.3");
    expect(goProxyURL("go-all")).toBe(`${window.location.origin}/go/go-all`);
  });
});

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
