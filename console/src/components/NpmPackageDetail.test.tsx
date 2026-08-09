import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NpmPackageDetail } from "./NpmPackageDetail";
import { AuthProvider } from "../lib/auth";
import { PreferencesProvider } from "../lib/preferences";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("NpmPackageDetail", () => {
  it("opens an exact version and changes versions without refetching the packument", async () => {
    const onVersionChange = vi.fn();
    const packageDocument = {
      name: "@scope/widget",
      "dist-tags": { latest: "1.2.3", next: "2.0.0-beta.1" },
      versions: {
        "1.2.3": {
          name: "@scope/widget",
          version: "1.2.3",
          dist: { integrity: "sha512-release", shasum: "release-sha1" },
          _artifactGateway: {
            digest: "sha256:release",
            publisher: "release-bot",
            size: 1024,
          },
        },
        "2.0.0-beta.1": {
          name: "@scope/widget",
          version: "2.0.0-beta.1",
          dist: { integrity: "sha512-next", shasum: "next-sha1" },
          _artifactGateway: {
            digest: "sha256:next",
            publisher: "next-bot",
            size: 2048,
          },
        },
      },
      time: {
        "1.2.3": "2026-08-01T00:00:00Z",
        "2.0.0-beta.1": "2026-08-02T00:00:00Z",
      },
    };
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        if (input === "/auth/session")
          return new Response(null, { status: 401 });
        return new Response(JSON.stringify(packageDocument), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <NpmPackageDetail
            repoName="npm-releases"
            packageName="@scope/widget"
            initialVersion="2.0.0-beta.1"
            onVersionChange={onVersionChange}
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(
      await screen.findByText("@scope/widget@2.0.0-beta.1"),
    ).toBeInTheDocument();
    expect(screen.getByText("next-bot")).toBeInTheDocument();
    expect(screen.getByText("2.0 KiB")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByText("1.2.3 · latest"));

    await waitFor(() => expect(onVersionChange).toHaveBeenCalledWith("1.2.3"));
    expect(screen.getByText("@scope/widget@1.2.3")).toBeInTheDocument();
    expect(
      fetchMock.mock.calls.filter(([url]) => url !== "/auth/session"),
    ).toHaveLength(1);
  });

  it("distinguishes proxy metadata from a cached tarball", async () => {
    const packageDocument = {
      name: "proxy-widget",
      "dist-tags": { latest: "1.0.0" },
      versions: {
        "1.0.0": {
          name: "proxy-widget",
          version: "1.0.0",
          dist: { integrity: "sha512-release", shasum: "release-sha1" },
          _artifactGateway: {
            source: "proxy",
            cacheStatus: "metadata",
            publisher: "upstream:registry.npmjs.org",
          },
        },
      },
    };
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (input === "/auth/session") return new Response(null, { status: 401 });
      return new Response(JSON.stringify(packageDocument), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <NpmPackageDetail repoName="npm-proxy" packageName="proxy-widget" />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(await screen.findByText("Proxy")).toBeInTheDocument();
    expect(screen.getByText("仅元数据")).toBeInTheDocument();
    expect(screen.getByText("下载后统计")).toBeInTheDocument();
    expect(screen.getByText("下载后计算")).toBeInTheDocument();
  });

  it("sends the active bearer token when reading a private package", async () => {
    localStorage.setItem("ag.console.token", "operator-token");
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        if (input === "/auth/session")
          return new Response(null, { status: 401 });
        return new Response(
          JSON.stringify({
            name: "private-widget",
            "dist-tags": { latest: "1.0.0" },
            versions: { "1.0.0": { name: "private-widget", version: "1.0.0" } },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <NpmPackageDetail
            repoName="npm-private"
            packageName="private-widget"
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    await screen.findByText("private-widget@1.0.0");
    expect(fetchMock).toHaveBeenCalledWith(
      "/npm/npm-private/private-widget",
      expect.objectContaining({
        credentials: "include",
        headers: { Authorization: "Bearer operator-token" },
      }),
    );
  });
});
