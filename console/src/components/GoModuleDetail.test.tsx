import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GoModuleDetail } from "./GoModuleDetail";
import { AuthProvider } from "../lib/auth";
import { PreferencesProvider } from "../lib/preferences";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("GoModuleDetail", () => {
  it("loads versions and preserves the version selected by a deep link", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        const target = String(input);
        if (target === "/auth/session")
          return new Response(null, { status: 401 });
        if (target.endsWith("/@v/list")) {
          return new Response("v1.0.0\nv1.1.0\n", { status: 200 });
        }
        if (target.endsWith("/@v/v1.1.0.info")) {
          return new Response(
            JSON.stringify({
              Version: "v1.1.0",
              Time: "2026-08-09T09:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response(null, { status: 404 });
      });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <GoModuleDetail
            repoName="go-public"
            modulePath="example.com/Acme/widget"
            initialVersion="v1.1.0"
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(
      await screen.findByText("example.com/Acme/widget@v1.1.0"),
    ).toBeInTheDocument();
    expect(screen.getByText("v1.1.0.zip")).toBeInTheDocument();
    expect(screen.queryByText("v1.0.0.zip")).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/go/go-public/example.com/!acme/widget/@v/list",
      expect.any(Object),
    );
  });

  it("escapes uppercase characters in versions for protocol asset URLs", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        const target = String(input);
        if (target === "/auth/session")
          return new Response(null, { status: 401 });
        if (target.endsWith("/@v/list"))
          return new Response("v1.2.0-RC1\n", { status: 200 });
        if (target.endsWith("/@v/v1.2.0-!r!c1.info")) {
          return new Response(
            JSON.stringify({
              Version: "v1.2.0-RC1",
              Time: "2026-08-09T09:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response(null, { status: 404 });
      });

    render(
      <AuthProvider>
        <PreferencesProvider>
          <GoModuleDetail
            repoName="go-public"
            modulePath="example.com/team/widget"
            initialVersion="v1.2.0-RC1"
          />
        </PreferencesProvider>
      </AuthProvider>,
    );

    expect(
      await screen.findByText("example.com/team/widget@v1.2.0-RC1"),
    ).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/go/go-public/example.com/team/widget/@v/v1.2.0-!r!c1.info",
      expect.any(Object),
    );
  });
});
