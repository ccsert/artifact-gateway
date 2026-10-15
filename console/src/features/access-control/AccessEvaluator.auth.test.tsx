import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import {
  getRepositoryEffectiveAccess,
  type Repository,
  type RepositoryEffectiveAccess,
} from "../../client";
import { AuthProvider, useAuth } from "../../lib/auth";
import { PreferencesProvider } from "../../lib/preferences";
import { ConsoleQueryProvider } from "../../lib/query/QueryProvider";
import { AccessEvaluator } from "./AccessEvaluator";

vi.mock("../../client", async (original) => ({
  ...(await original<typeof import("../../client")>()),
  getRepositoryEffectiveAccess: vi.fn(),
}));
const repository = {
  id: "repo-a",
  name: "Releases",
  type: "hosted",
  format: "raw",
  state: "active",
  version: "1",
  anonymousRead: false,
  mavenStrictPublication: false,
} satisfies Repository;
const decision = {
  allowed: true,
  source: "administrator",
  reason: "administrator",
};
function answer(actor = "alice-evaluation"): RepositoryEffectiveAccess {
  return {
    actor,
    simulated: false,
    resource: "",
    repository,
    identity: {
      kind: "local_session",
      actor,
      role: "admin",
      administrator: true,
    },
    anonymousRead: decision,
    permissions: {
      read: decision,
      write: decision,
      admin: decision,
      intelligence: decision,
    },
  };
}
function Session() {
  const auth = useAuth();
  if (auth.identityLoading) return <p>Loading</p>;
  if (!auth.authenticated) return <p>Signed out</p>;
  return (
    <>
      <h1>{auth.identity?.actor}</h1>
      <button onClick={() => auth.setToken("bob-token")}>
        Switch identity
      </button>
      <AccessEvaluator
        users={[]}
        apiKeys={[]}
        serviceAccounts={[]}
        repositories={[repository]}
        evaluatorOptionsLoading={false}
        evaluatorOptionsError={null}
        serviceAccountsError={null}
        onRetryDirectory={() => {}}
      />
    </>
  );
}
function mount() {
  localStorage.setItem("ag.console.token", "alice-token");
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/auth/logout") return new Response(null, { status: 204 });
    const actor =
      (init?.headers as Record<string, string>)?.Authorization ===
      "Bearer bob-token"
        ? "user:bob"
        : "user:alice";
    return new Response(
      JSON.stringify({
        authenticated: true,
        identity: {
          actor,
          kind: "local_session",
          role: "admin",
          administrator: true,
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <AuthProvider>
      <ConsoleQueryProvider>
        <PreferencesProvider>
          <Session />
        </PreferencesProvider>
      </ConsoleQueryProvider>
    </AuthProvider>,
  );
  return fetchMock;
}
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.resetAllMocks();
});

it.each([401, 403])(
  "ends only a 401 session and hides the previous decision after a %s recheck",
  async (status) => {
    vi.mocked(getRepositoryEffectiveAccess)
      .mockResolvedValueOnce({ data: answer() } as never)
      .mockResolvedValue({
        error: { status, message: "Evaluation denied" },
      } as never);
    const fetchMock = mount();
    await userEvent.click(
      await screen.findByRole("button", { name: /检\s*查/ }),
    );
    await screen.findByText("alice-evaluation");
    await userEvent.click(screen.getByRole("button", { name: /检\s*查/ }));
    if (status === 401) {
      await screen.findByText("Signed out");
      expect(localStorage.getItem("ag.console.token")).toBeNull();
      expect(fetchMock).toHaveBeenCalledWith("/auth/logout", expect.anything());
    } else {
      await screen.findByText("Evaluation denied");
      expect(screen.getByRole("heading", { name: "user:alice" })).toBeVisible();
      expect(localStorage.getItem("ag.console.token")).toBe("alice-token");
      expect(fetchMock).not.toHaveBeenCalledWith(
        "/auth/logout",
        expect.anything(),
      );
    }
    expect(screen.queryByText("alice-evaluation")).not.toBeInTheDocument();
  },
);

it("cancels an evaluation and drops its late answer when the identity changes", async () => {
  let finish!: (value: never) => void;
  let signal!: AbortSignal;
  vi.mocked(getRepositoryEffectiveAccess).mockImplementationOnce((options) => {
    signal = options!.signal!;
    return new Promise<never>((resolve) => {
      finish = resolve;
    });
  });
  mount();
  await userEvent.click(await screen.findByRole("button", { name: /检\s*查/ }));
  await waitFor(() =>
    expect(getRepositoryEffectiveAccess).toHaveBeenCalledTimes(1),
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Switch identity" }),
  );
  await screen.findByRole("heading", { name: "user:bob" });
  expect(signal.aborted).toBe(true);
  await act(async () => {
    finish({ data: answer() } as never);
  });
  expect(screen.queryByText("alice-evaluation")).not.toBeInTheDocument();
});
