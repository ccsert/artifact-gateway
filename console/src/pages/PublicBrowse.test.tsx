import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { searchRepositoryArtifacts } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { PublicBrowsePage } from "./PublicBrowse";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  searchRepositoryArtifacts: vi.fn(),
}));

const auth = vi.hoisted(() => ({
  authenticated: false,
  identityLoading: false,
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => auth,
}));

const mockSearchRepositoryArtifacts = vi.mocked(searchRepositoryArtifacts);

beforeEach(() => {
  Object.assign(auth, {
    authenticated: false,
    identityLoading: false,
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("PublicBrowsePage APT browse", () => {
  it("keeps the zero-data catalog on the same full-width surface as populated states", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ enabled: true, items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    render(
      <MemoryRouter initialEntries={["/browse"]}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    const title = await screen.findByText("暂无公开仓库", { exact: true });
    expect(document.querySelector(".ag-public-browse-page")).toHaveClass(
      "w-full",
      "max-w-[1440px]",
    );
    expect(title.closest(".ag-public-state-surface")).toHaveClass(
      "ag-public-catalog-empty-surface",
    );
    expect(title.closest(".ant-empty")).toHaveClass("ag-empty-state");
    expect(title.closest(".ant-empty")?.querySelector("img")).toBeNull();
    expect(
      screen.queryByPlaceholderText("搜索仓库名称或格式"),
    ).not.toBeInTheDocument();
    expect(screen.getAllByText("0", { selector: ".text-2xl" })).toHaveLength(2);
  });

  it("sends an authenticated operator directly back to the management console", async () => {
    Object.assign(auth, { authenticated: true });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ enabled: true, items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    render(
      <MemoryRouter initialEntries={["/browse"]}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("link", { name: "进入管理端" }),
    ).toHaveAttribute("href", "/");
    expect(
      screen.queryByRole("link", { name: "管理登录" }),
    ).not.toBeInTheDocument();
  });

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

  it("shows and searches Raw files by their readable path", async () => {
    const repositoryId = "33333333-3333-4333-8333-333333333333";
    const readable = "ChatGPT Image 2026年8月19日 (2).png";
    const canonical =
      "ChatGPT%20Image%202026%E5%B9%B48%E6%9C%8819%E6%97%A5%20%282%29.png";
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          enabled: true,
          items: [
            {
              id: repositoryId,
              name: "raw-public",
              format: "raw",
              type: "hosted",
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
            coordinate: canonical,
            digest: `sha256:${"c".repeat(64)}`,
            size: 1024,
            contentType: "image/png",
            createdAt: "2026-08-27T06:06:14Z",
          },
        ],
      },
    } as never);
    const params = new URLSearchParams({
      repository: repositoryId,
      q: readable,
    });

    render(
      <MemoryRouter initialEntries={[`/browse?${params.toString()}`]}>
        <PreferencesProvider>
          <PublicBrowsePage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText(readable)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: `复制 ${readable}` }),
    ).toBeInTheDocument();
    expect(screen.queryByText(canonical)).not.toBeInTheDocument();
    expect(mockSearchRepositoryArtifacts).toHaveBeenLastCalledWith({
      path: { repositoryId },
      query: { q: canonical, pageSize: 100 },
    });
  });
});
