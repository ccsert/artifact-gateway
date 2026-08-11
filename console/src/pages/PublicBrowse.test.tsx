import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { searchRepositoryArtifacts } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { PublicBrowsePage } from "./PublicBrowse";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  searchRepositoryArtifacts: vi.fn(),
}));

const mockSearchRepositoryArtifacts = vi.mocked(searchRepositoryArtifacts);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("PublicBrowsePage APT browse", () => {
  it("deep-links to an anonymously readable cached APT asset", async () => {
    const repositoryId = "11111111-1111-4111-8111-111111111111";
    const path = "dists/bookworm/InRelease";
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          enabled: true,
          items: [
            {
              id: repositoryId,
              name: "debian-public",
              format: "apt",
              type: "proxy",
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: {
        items: [
          {
            coordinate: path,
            digest:
              "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            size: 1024,
            contentType: "application/octet-stream",
            createdAt: "2026-08-11T04:30:00Z",
            cachedAt: "2026-08-11T05:30:00Z",
            sourceUrl: `https://deb.example.test/debian/${path}`,
          },
        ],
      },
    } as never);
    const query = new URLSearchParams({
      repository: repositoryId,
      artifact: path,
    });

    render(
      <MemoryRouter initialEntries={[`/browse?${query.toString()}`]}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect((await screen.findAllByText("APT 路径")).length).toBeGreaterThan(0);
    expect(await screen.findByText("仓库元数据")).toBeInTheDocument();
    expect(screen.getAllByText("首次缓存").length).toBeGreaterThan(0);
    expect(screen.getByText("上游地址")).toBeInTheDocument();
    expect(screen.getByText("注册 APT 软件源")).toBeInTheDocument();
    expect(mockSearchRepositoryArtifacts).toHaveBeenCalledWith({
      path: { repositoryId },
      query: { q: undefined, pageSize: 100 },
    });
  });
});
