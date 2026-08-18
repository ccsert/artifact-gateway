import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

  it("presents a searchable, format-aware read-only public catalog", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          enabled: true,
          items: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              name: "platform-images",
              format: "oci",
              type: "hosted",
            },
            {
              id: "22222222-2222-4222-8222-222222222222",
              name: "maven-public",
              format: "maven",
              type: "group",
            },
            {
              id: "33333333-3333-4333-8333-333333333333",
              name: "npm-cache",
              format: "npm",
              type: "proxy",
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    render(
      <MemoryRouter initialEntries={["/browse"]}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", {
        name: "查找并使用可信的公开制品",
      }),
    ).toBeInTheDocument();
    expect(screen.getByText("公开只读")).toBeInTheDocument();
    expect(screen.getByText(/3 个公开来源/)).toBeInTheDocument();
    expect(screen.getByText(/3 种制品格式/)).toBeInTheDocument();
    expect(screen.getByText("管理操作需要登录")).toBeInTheDocument();

    const search = screen.getByPlaceholderText("搜索仓库名称或格式");
    fireEvent.change(search, { target: { value: "maven" } });
    expect(screen.getByText("maven-public")).toBeInTheDocument();
    expect(screen.queryByText("platform-images")).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "OCI" }));
    expect(screen.getByText("platform-images")).toBeInTheDocument();
    expect(screen.queryByText("maven-public")).not.toBeInTheDocument();
  });
});
