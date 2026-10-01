import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CargoCrateDetail } from "./CargoCrateDetail";
import { AuthProvider } from "../../lib/auth";
import { PreferencesProvider } from "../../lib/preferences";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("CargoCrateDetail", () => {
  it("opens an exact version deep link and shows the registry yank state", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        const target = String(input);
        if (target === "/auth/session")
          return new Response(null, { status: 401 });
        if (target === "/cargo/cargo-hosted/de/mo/demo-crate")
          return new Response(
            [
              JSON.stringify({
                name: "demo-crate",
                vers: "1.2.3",
                cksum: "a".repeat(64),
                yanked: true,
              }),
              JSON.stringify({
                name: "demo-crate",
                vers: "1.1.0",
                cksum: "b".repeat(64),
                yanked: false,
              }),
            ].join("\n"),
            { status: 200 },
          );
        return new Response(null, { status: 404 });
      });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <CargoCrateDetail
            repoName="cargo-hosted"
            crateName="demo-crate"
            initialVersion="1.2.3"
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(await screen.findByText("yanked")).toBeInTheDocument();
    expect(screen.getByText("下载 .crate")).toBeInTheDocument();
    expect(screen.getByText("SHA-256", { exact: false })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/cargo/cargo-hosted/de/mo/demo-crate",
      expect.any(Object),
    );
  });
});
