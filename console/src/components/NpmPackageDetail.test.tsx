import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NpmPackageDetail } from "./NpmPackageDetail";
import { PreferencesProvider } from "../lib/preferences";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("NpmPackageDetail", () => {
  it("opens an exact version and changes versions without refetching the packument", async () => {
    const onVersionChange = vi.fn();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
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
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    render(
      <PreferencesProvider>
        <NpmPackageDetail
          repoName="npm-releases"
          packageName="@scope/widget"
          initialVersion="2.0.0-beta.1"
          onVersionChange={onVersionChange}
        />
      </PreferencesProvider>,
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
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
