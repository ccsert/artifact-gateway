import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "./auth";

function IdentityProbe() {
  const { token, role, identity, identityLoading } = useAuth();
  if (!token) return <span>signed-out</span>;
  if (identityLoading) return <span>loading</span>;
  return <span>{`${identity?.kind ?? "unknown"}:${role}`}</span>;
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("AuthProvider identity resolution", () => {
  it("replaces a stale persisted role with the server identity", async () => {
    localStorage.setItem("ag.console.token", "agk_test");
    localStorage.setItem("ag.console.role", "admin");
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          actor: "api-key:key-id",
          kind: "api_key",
          role: "reader",
          administrator: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AuthProvider>
        <IdentityProbe />
      </AuthProvider>,
    );

    expect(await screen.findByText("api_key:reader")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/v2/identity", {
      headers: { Authorization: "Bearer agk_test" },
    });
    expect(localStorage.getItem("ag.console.role")).toBe("reader");
  });

  it("clears an expired token after identity validation returns 401", async () => {
    localStorage.setItem("ag.console.token", "expired");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(null, { status: 401 })),
    );

    render(
      <AuthProvider>
        <IdentityProbe />
      </AuthProvider>,
    );

    expect(await screen.findByText("signed-out")).toBeInTheDocument();
    expect(localStorage.getItem("ag.console.token")).toBeNull();
  });
});
