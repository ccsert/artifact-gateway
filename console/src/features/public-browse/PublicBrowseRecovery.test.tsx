import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { searchRepositoryArtifacts } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { createQueryClient } from "../../lib/query/client";
import { publicKeys } from "../../lib/query/keys";
import { PublicBrowsePage } from "./PublicBrowsePage";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  searchRepositoryArtifacts: vi.fn(),
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ authenticated: false, identityLoading: false }),
}));

const repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "public-source",
  format: "raw",
  type: "hosted",
};
const artifact = {
  coordinate: "lib.tgz",
  digest: `sha256:${"a".repeat(64)}`,
  size: 1024,
  createdAt: "2026-10-01T00:00:00Z",
};
const search = vi.mocked(searchRepositoryArtifacts);
const clients: ReturnType<typeof createQueryClient>[] = [];

function catalog(format = "raw") {
  return new Response(
    JSON.stringify({ enabled: true, items: [{ ...repository, format }] }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

function browse(url = "/browse") {
  const client = createQueryClient();
  client.setDefaultOptions({ queries: { retry: false, staleTime: 30_000 } });
  clients.push(client);
  render(
    <MemoryRouter initialEntries={[url]}>
      <QueryClientProvider client={client}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return client;
}

beforeEach(() => {
  // Broken server answers are deliberate fixtures in the containment tests.
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.restoreAllMocks();
  vi.resetAllMocks();
});

describe("public browse section recovery", () => {
  it("keeps a malformed public repository answer inside the catalog error state", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ enabled: true, items: [null] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(catalog());
    browse();
    expect(
      await screen.findByText("Invalid public repository catalog response"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "管理登录" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("public-source")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keeps a non-list artifact answer inside the query error state", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => catalog());
    search
      .mockResolvedValueOnce({ data: { items: {} } } as never)
      .mockResolvedValueOnce({ data: { items: [artifact] } } as never);
    browse(`/browse?repository=${repository.id}`);
    expect(
      await screen.findByText("Invalid public artifact search response"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "管理登录" })).toBeInTheDocument();
    expect(screen.getByText("注册 Raw 源地址")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("lib.tgz")).toBeInTheDocument();
    expect(search).toHaveBeenCalledTimes(2);
  });

  it("refetches a broken catalog section before retrying its render", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(catalog("future-format"))
      .mockResolvedValueOnce(catalog());
    browse();

    expect(await screen.findByText("此区域暂时无法显示")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "管理登录" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("public-source")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("此区域暂时无法显示")).not.toBeInTheDocument();
  });

  it("contains a broken artifact answer and retries the actual source", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => catalog());
    search
      .mockResolvedValueOnce({ data: { items: [null] } } as never)
      .mockResolvedValueOnce({ data: { items: [artifact] } } as never);
    browse(`/browse?repository=${repository.id}`);

    expect(await screen.findByText("此区域暂时无法显示")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "管理登录" })).toBeInTheDocument();
    expect(screen.getByText("注册 Raw 源地址")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("lib.tgz")).toBeInTheDocument();
    expect(search).toHaveBeenCalledTimes(2);
  });

  it("keeps a loaded catalog visible after a failed refresh and offers retry", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(catalog())
      .mockResolvedValueOnce(new Response("offline", { status: 503 }))
      .mockResolvedValueOnce(catalog());
    const client = browse();
    expect(await screen.findByText("public-source")).toBeInTheDocument();

    await act(() => client.refetchQueries({ queryKey: publicKeys.catalog() }));
    expect(
      await screen.findByText("读取公开仓库失败 (503)"),
    ).toBeInTheDocument();
    expect(screen.getByText("public-source")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("public-source")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(3);
    await waitFor(() =>
      expect(
        screen.queryByText("读取公开仓库失败 (503)"),
      ).not.toBeInTheDocument(),
    );
  });

  it("keeps loaded artifacts visible after a failed refresh and offers retry", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => catalog());
    search
      .mockResolvedValueOnce({ data: { items: [artifact] } } as never)
      .mockResolvedValueOnce({
        error: { status: 503, message: "offline" },
      } as never)
      .mockResolvedValueOnce({ data: { items: [artifact] } } as never);
    const client = browse(`/browse?repository=${repository.id}`);
    expect(await screen.findByText("lib.tgz")).toBeInTheDocument();

    await act(() =>
      client.refetchQueries({
        queryKey: publicKeys.artifacts(repository.id, ""),
      }),
    );
    expect(await screen.findByText("offline")).toBeInTheDocument();
    expect(screen.getByText("lib.tgz")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("lib.tgz")).toBeInTheDocument();
    expect(search).toHaveBeenCalledTimes(3);
    await waitFor(() =>
      expect(screen.queryByText("offline")).not.toBeInTheDocument(),
    );
  });
});
