import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider, useAuth } from "../../lib/auth";
import { ConsoleQueryProvider } from "../../lib/query/QueryProvider";
import {
  getMigrationSource,
  listMigrationSources,
  updateMigrationSource,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { migrationSource } from "../../test/migrationFixtures";
import { MigrationSourcesPanel } from "./MigrationSourcesPanel";
import { useMigrationSources } from "./migrationQueries";
import { useMigrationAccess } from "./migrationAccess";

vi.mock("../../client", () => ({
  listMigrationSources: vi.fn(),
  getMigrationSource: vi.fn(),
  updateMigrationSource: vi.fn(),
  createMigrationSource: vi.fn(),
  deleteMigrationSource: vi.fn(),
  preflightMigrationSource: vi.fn(),
  discoverMigrationSource: vi.fn(),
  previewMigrationSourceMapping: vi.fn(),
}));
const bobSource = {
  ...migrationSource,
  name: "Bob source",
  baseUrl: "https://bob.example.test",
};
function Panel({ fullSource = false }: { fullSource?: boolean }) {
  const { identityLoading, identity, setToken } = useAuth();
  const access = useMigrationAccess();
  const sources = useMigrationSources();
  return (
    <>
      <button onClick={() => setToken("bob-token")}>Login Bob</button>
      <p>{identityLoading ? "loading" : identity?.actor}</p>
      <p data-testid="source-access">
        {access.error ? "forbidden" : "allowed"}
      </p>
      {fullSource ? (
        <MigrationSourcesPanel />
      ) : (
        <p>
          {sources.data?.pages
            .flatMap((p) => p.items)
            .map((s) => s.name)
            .join(",")}
        </p>
      )}
    </>
  );
}
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.resetAllMocks();
});
it("does not retain Alice refusal when Bob signs in with a new token", async () => {
  localStorage.setItem("ag.console.token", "alice-token");
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (_url: string, init?: RequestInit) =>
        new Response(
          JSON.stringify({
            authenticated: true,
            identity: {
              actor:
                (init?.headers as Record<string, string>)?.Authorization ===
                "Bearer bob-token"
                  ? "user:bob"
                  : "user:alice",
              kind: "local_session",
              role: "admin",
              administrator: true,
            },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  vi.mocked(listMigrationSources).mockImplementation(
    async () =>
      (localStorage.getItem("ag.console.token") === "alice-token"
        ? { error: { status: 403 } }
        : { data: { items: [bobSource], nextCursor: "" } }) as never,
  );
  render(
    <AuthProvider>
      <ConsoleQueryProvider>
        <Panel />
      </ConsoleQueryProvider>
    </AuthProvider>,
  );
  await screen.findByText("user:alice");
  await screen.findByText("forbidden");
  await userEvent.click(screen.getByRole("button", { name: "Login Bob" }));
  await screen.findByText("user:bob");
  await waitFor(() =>
    expect(vi.mocked(listMigrationSources).mock.calls.length).toBeGreaterThan(
      1,
    ),
  );
  expect(await screen.findByText("Bob source")).toBeVisible();
});

function identity(actor: string) {
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
}

it("starts a fresh source read when the resolved actor changes under the same token", async () => {
  localStorage.setItem("ag.console.token", "shared-token");
  let finish!: (response: Response) => void;
  let resolved = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    ),
  );
  vi.mocked(listMigrationSources).mockImplementation(
    async () =>
      (resolved
        ? { data: { items: [bobSource], nextCursor: "" } }
        : { error: { status: 403 } }) as never,
  );
  render(
    <AuthProvider>
      <ConsoleQueryProvider>
        <Panel />
      </ConsoleQueryProvider>
    </AuthProvider>,
  );
  await screen.findByText("forbidden");
  const before = vi.mocked(listMigrationSources).mock.calls.length;
  await act(async () => {
    resolved = true;
    finish(identity("user:bob"));
  });
  await screen.findByText("user:bob");
  expect(await screen.findByText("Bob source")).toBeVisible();
  expect(vi.mocked(listMigrationSources).mock.calls.length).toBeGreaterThan(
    before,
  );
  expect(localStorage.getItem("ag.console.token")).toBe("shared-token");
});

for (const operation of ["GET", "POST"] as const)
  for (const result of ["success", "unauthorized", "forbidden"] as const)
    it(`discards Alice's late ${operation} ${result} after Bob has fresh source facts`, async () => {
      localStorage.setItem("ag.console.token", "alice-token");
      vi.stubGlobal(
        "fetch",
        vi.fn(async (_url: string, init?: RequestInit) =>
          identity(
            (init?.headers as Record<string, string>)?.Authorization ===
              "Bearer bob-token"
              ? "user:bob"
              : "user:alice",
          ),
        ),
      );
      vi.mocked(listMigrationSources).mockImplementation(
        async () =>
          ({
            data: {
              items: [
                localStorage.getItem("ag.console.token") === "bob-token"
                  ? bobSource
                  : migrationSource,
              ],
              nextCursor: "",
            },
          }) as never,
      );
      vi.mocked(getMigrationSource).mockImplementation(
        async () =>
          ({
            data:
              localStorage.getItem("ag.console.token") === "bob-token"
                ? bobSource
                : migrationSource,
          }) as never,
      );
      render(
        <AuthProvider>
          <ConsoleQueryProvider>
            <PreferencesProvider>
              <AntdProvider>
                <MemoryRouter
                  initialEntries={[`/operations?source=${migrationSource.id}`]}
                >
                  <Panel fullSource />
                </MemoryRouter>
              </AntdProvider>
            </PreferencesProvider>
          </ConsoleQueryProvider>
        </AuthProvider>,
      );
      await screen.findByText("user:alice");
      await screen.findByRole("heading", { name: migrationSource.name });
      let finish!: (value: unknown) => void;
      const pending = () =>
        new Promise((resolve) => {
          finish = resolve;
        }) as never;
      if (operation === "GET") {
        vi.mocked(getMigrationSource).mockImplementationOnce(pending);
        await userEvent.click(
          screen.getAllByRole("button", { name: "刷新源" })[1],
        );
        await waitFor(() => expect(finish).toBeDefined());
      } else {
        vi.mocked(updateMigrationSource).mockImplementationOnce(pending);
        await userEvent.click(screen.getByRole("button", { name: "编辑源" }));
        await userEvent.click(
          screen.getByRole("button", { name: "保存迁移源" }),
        );
        await waitFor(() =>
          expect(updateMigrationSource).toHaveBeenCalledTimes(1),
        );
      }
      await userEvent.click(screen.getByRole("button", { name: "Login Bob" }));
      await screen.findByText("user:bob");
      await screen.findByRole("heading", { name: bobSource.name });
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      await act(async () => {
        finish(
          result === "success"
            ? {
                data: {
                  ...migrationSource,
                  name: "Alice late private source",
                  version: "99",
                },
              }
            : {
                error: {
                  status: result === "forbidden" ? 403 : 401,
                  message: "Alice private failure",
                },
              },
        );
      });
      await waitFor(() =>
        expect(screen.getByTestId("source-access")).toHaveTextContent(
          "allowed",
        ),
      );
      expect(
        screen.getByRole("heading", { name: bobSource.name }),
      ).toBeInTheDocument();
      expect(document.body).not.toHaveTextContent("Alice late private source");
      expect(document.body).not.toHaveTextContent("Alice private failure");
      expect(document.body).not.toHaveTextContent(migrationSource.baseUrl);
      expect(localStorage.getItem("ag.console.token")).toBe("bob-token");
      expect(
        screen.queryByText(/没有读取或管理此迁移源的权限/),
      ).not.toBeInTheDocument();
      if (operation === "POST")
        expect(updateMigrationSource).toHaveBeenCalledTimes(1);
    });
