import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQuery } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "../auth";
import { unwrap } from "./client";
import { ConsoleQueryProvider } from "./QueryProvider";

const read = vi.fn<() => Promise<{ data?: string; error?: unknown }>>();

function ProtectedRead() {
  const query = useQuery({
    queryKey: ["protected"],
    queryFn: () => unwrap(read()),
  });
  return (
    <>
      <p>{query.data}</p>
      {query.error && (
        <p role="alert">{(query.error as { message: string }).message}</p>
      )}
      <button onClick={() => void query.refetch()}>Refresh</button>
    </>
  );
}

function SessionPage() {
  const { identityLoading, authenticated, identity, setToken } = useAuth();
  if (identityLoading) return <p>Loading session</p>;
  if (!authenticated)
    return (
      <>
        <p>Signed out</p>
        <button onClick={() => setToken("new-login-token")}>New login</button>
      </>
    );
  return (
    <>
      <h1>{identity?.actor}</h1>
      <ProtectedRead />
    </>
  );
}

function renderSession(token: boolean) {
  if (token) localStorage.setItem("ag.console.token", "expired-token");
  let loggedOut = false;
  const fetchMock = vi.fn(async (url: string) => {
    if (url === "/auth/logout") loggedOut = true;
    return new Response(
      JSON.stringify({
        authenticated: !loggedOut,
        identity: {
          actor: "user:alice",
          kind: "local_session",
          role: "member",
          administrator: false,
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <AuthProvider>
      <ConsoleQueryProvider>
        <SessionPage />
      </ConsoleQueryProvider>
    </AuthProvider>,
  );
  return fetchMock;
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
  read.mockReset();
});

describe("query failures and the authenticated session", () => {
  it("shows a forbidden read without ending a valid session", async () => {
    const user = userEvent.setup();
    read
      .mockResolvedValueOnce({ data: "Alice's protected data" })
      .mockResolvedValue({
        error: { status: 403, message: "Repository access denied" },
      });
    const fetchMock = renderSession(true);
    expect(await screen.findByText("Alice's protected data")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Repository access denied",
    );
    expect(screen.getByRole("heading", { name: "user:alice" })).toBeVisible();
    expect(localStorage.getItem("ag.console.token")).toBe("expired-token");
    expect(fetchMock).not.toHaveBeenCalledWith(
      "/auth/logout",
      expect.anything(),
    );
    expect(read).toHaveBeenCalledTimes(2);
  });
  it("does not restore a cookie session while logout is still pending", async () => {
    const user = userEvent.setup();
    localStorage.setItem("ag.console.token", "expired-token");
    read
      .mockResolvedValueOnce({ data: "Alice's protected data" })
      .mockResolvedValueOnce({
        error: { status: 401, message: "Session expired" },
      })
      .mockResolvedValue({ data: "Cookie protected data" });
    let finishLogout!: (response: Response) => void;
    const logout = new Promise<Response>((resolve) => {
      finishLogout = resolve;
    });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === "/auth/logout") return logout;
      return new Response(
        JSON.stringify({
          authenticated: true,
          identity: {
            actor: "user:cookie-session",
            kind: "local_session",
            role: "member",
            administrator: false,
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    render(
      <AuthProvider>
        <ConsoleQueryProvider>
          <SessionPage />
        </ConsoleQueryProvider>
      </AuthProvider>,
    );
    expect(await screen.findByText("Alice's protected data")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("Signed out")).toBeVisible();
    expect(
      screen.queryByText("Alice's protected data"),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(localStorage.getItem("ag.console.token")).toBeNull();
    await act(async () => {
      finishLogout(new Response(null, { status: 204 }));
    });
    expect(screen.getByText("Signed out")).toBeVisible();
    expect(read).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole("button", { name: "New login" }));
    expect(await screen.findByText("Cookie protected data")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "user:cookie-session" }),
    ).toBeVisible();
    expect(localStorage.getItem("ag.console.token")).toBe("new-login-token");
  });

  it.each([true, false])(
    "ends an expired session and hides protected data (token=%s)",
    async (token) => {
      const user = userEvent.setup();
      read
        .mockResolvedValueOnce({ data: "Alice's protected data" })
        .mockResolvedValue({
          error: { status: 401, message: "Session expired" },
        });
      const fetchMock = renderSession(token);
      expect(await screen.findByText("Alice's protected data")).toBeVisible();
      await user.click(screen.getByRole("button", { name: "Refresh" }));
      expect(await screen.findByText("Signed out")).toBeVisible();
      expect(
        screen.queryByText("Alice's protected data"),
      ).not.toBeInTheDocument();
      expect(localStorage.getItem("ag.console.token")).toBeNull();
      expect(localStorage.getItem("ag.console.role")).toBeNull();
      expect(fetchMock).toHaveBeenCalledWith("/auth/logout", {
        method: "POST",
        credentials: "include",
      });
      expect(read).toHaveBeenCalledTimes(2);
    },
  );
});
