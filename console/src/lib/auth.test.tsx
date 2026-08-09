import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "./auth";

function IdentityProbe() {
  const { authenticated, role, identity, identityLoading } = useAuth();
  if (identityLoading) return <span>loading</span>;
  if (!authenticated) return <span>signed-out</span>;
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
          authenticated: true,
          identity: {
            actor: "api-key:key-id",
            kind: "api_key",
            role: "reader",
            administrator: false,
          },
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
    expect(fetchMock).toHaveBeenCalledWith("/auth/session", {
      credentials: "include",
      headers: { Authorization: "Bearer agk_test" },
    });
    expect(localStorage.getItem("ag.console.role")).toBe("reader");
  });

  it("clears an expired token after identity validation returns 401", async () => {
    localStorage.setItem("ag.console.token", "expired");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ authenticated: false }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    render(
      <AuthProvider>
        <IdentityProbe />
      </AuthProvider>,
    );

    expect(await screen.findByText("signed-out")).toBeInTheDocument();
    expect(localStorage.getItem("ag.console.token")).toBeNull();
  });

  it("accepts an HttpOnly cookie-backed OIDC session without local storage", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          authenticated: true,
          identity: {
            actor: "gitlab-user",
            kind: "oidc",
            role: "reader",
            administrator: false,
          },
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

    expect(await screen.findByText("oidc:reader")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/auth/session", {
      credentials: "include",
      headers: undefined,
    });
    expect(localStorage.getItem("ag.console.token")).toBeNull();
  });
});
