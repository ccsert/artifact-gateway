import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { browseRepository, searchRepositoryArtifacts } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryArtifactsTab } from "./RepositoryArtifactsTab";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  browseRepository: vi.fn(),
  searchRepositoryArtifacts: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "" }),
}));

const mockSearchRepositoryArtifacts = vi.mocked(searchRepositoryArtifacts);
const mockBrowseRepository = vi.mocked(browseRepository);

const repository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "debian",
  format: "apt",
  type: "proxy",
  endpoint: "https://deb.example.test/debian",
  allowedHosts: ["deb.example.test"],
  anonymousRead: true,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryArtifactsTab APT browse", () => {
  it("loads path projections and opens a read-only cache detail", async () => {
    const user = userEvent.setup();
    const path = "pool/main/h/hello/hello_2.10_amd64.deb";
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: {
        items: [
          {
            coordinate: path,
            digest:
              "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            size: 4096,
            contentType: "application/vnd.debian.binary-package",
            createdAt: "2026-08-11T04:30:00Z",
            cachedAt: "2026-08-11T05:30:00Z",
            sourceUrl: `https://deb.example.test/debian/${path}`,
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab repo={repository} canWrite />
      </PreferencesProvider>,
    );

    expect(await screen.findByText(path)).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("按 dists/ 或 pool/ 路径过滤…"),
    ).toBeInTheDocument();
    expect(mockSearchRepositoryArtifacts).toHaveBeenCalledWith({
      path: { repositoryId: repository.id },
      query: { q: undefined, pageSize: 50, pageToken: undefined },
    });

    await user.click(screen.getByText(path));
    expect(await screen.findByText("软件包对象")).toBeInTheDocument();
    expect(screen.getByText("上游地址")).toBeInTheDocument();
    expect(screen.queryByText("删除文件")).not.toBeInTheDocument();
  });

  it("presents only the current hosted signed snapshot as published content", async () => {
    const user = userEvent.setup();
    const hostedRepository: Repository = {
      ...repository,
      id: "22222222-2222-4222-8222-222222222222",
      name: "debian-release",
      type: "hosted",
      endpoint: undefined,
      allowedHosts: [],
    };
    const path = "pool/main/w/widget/widget_1.0-1_amd64.deb";
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: {
        items: [
          {
            coordinate: path,
            digest:
              "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            size: 8192,
            contentType: "application/vnd.debian.binary-package",
            createdAt: "2026-08-13T09:00:00Z",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab repo={hostedRepository} canWrite />
      </PreferencesProvider>,
    );

    expect(await screen.findByText(path)).toBeInTheDocument();
    await user.click(screen.getByText(path));
    expect((await screen.findAllByText("已发布")).length).toBeGreaterThan(0);
    expect(screen.getByText("APT 资产类型")).toBeInTheDocument();
    expect(screen.queryByText("首次缓存")).not.toBeInTheDocument();
    expect(screen.queryByText("上游地址")).not.toBeInTheDocument();
    expect(screen.queryByText("最近缓存")).not.toBeInTheDocument();
  });
});

describe("RepositoryArtifactsTab Raw browse", () => {
  it("switches between the existing list and the format-aware directory", async () => {
    const user = userEvent.setup();
    const rawRepository: Repository = {
      ...repository,
      id: "55555555-5555-4555-8555-555555555555",
      name: "raw-releases",
      format: "raw",
      type: "hosted",
      endpoint: undefined,
      allowedHosts: [],
    };
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: { items: [] },
    } as never);
    mockBrowseRepository.mockResolvedValue({
      data: {
        items: [
          {
            id: "node-docs",
            kind: "directory",
            name: "docs",
            hasChildren: true,
            path: "docs",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab repo={rawRepository} canWrite />
      </PreferencesProvider>,
    );

    await user.click(screen.getByText("目录"));
    expect(await screen.findByText("制品目录")).toBeInTheDocument();
    expect(screen.getByText("docs")).toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenCalledWith({
      path: { repositoryId: rawRepository.id },
      query: { pageSize: 50 },
    });

    await user.click(screen.getByText("列表"));
    expect(screen.getByPlaceholderText("搜索路径…")).toBeInTheDocument();
  });

  it("shows a readable file path while preserving the canonical coordinate", async () => {
    const user = userEvent.setup();
    const encodedPath =
      "ChatGPT%20Image%202026%E5%B9%B48%E6%9C%8819%E6%97%A5%2013_56_07%20%282%29.png";
    const readablePath = "ChatGPT Image 2026年8月19日 13_56_07 (2).png";
    const rawRepository: Repository = {
      ...repository,
      id: "33333333-3333-4333-8333-333333333333",
      name: "raw-releases",
      format: "raw",
      type: "hosted",
      endpoint: undefined,
      allowedHosts: [],
    };
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: {
        items: [
          {
            coordinate: encodedPath,
            digest:
              "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
            size: 1024,
            contentType: "image/png",
            createdAt: "2026-08-27T06:06:14Z",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab repo={rawRepository} canWrite />
      </PreferencesProvider>,
    );

    expect(await screen.findByText(readablePath)).toBeInTheDocument();
    expect(screen.queryByText(encodedPath)).not.toBeInTheDocument();

    await user.click(screen.getByText(readablePath));
    expect(await screen.findByText("image/png")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("搜索路径…"), readablePath);
    await user.click(screen.getByRole("button", { name: /搜\s*索/ }));
    await waitFor(() =>
      expect(mockSearchRepositoryArtifacts).toHaveBeenLastCalledWith({
        path: { repositoryId: rawRepository.id },
        query: {
          q: encodedPath,
          pageSize: 50,
          pageToken: undefined,
        },
      }),
    );
  });

  it("keeps a deep-linked Raw coordinate canonical while showing a readable search value", async () => {
    const canonical = "report%2520final.txt";
    const rawRepository: Repository = {
      ...repository,
      id: "44444444-4444-4444-8444-444444444444",
      name: "raw-releases",
      format: "raw",
      type: "hosted",
      endpoint: undefined,
      allowedHosts: [],
    };
    mockSearchRepositoryArtifacts.mockResolvedValue({
      data: {
        items: [
          {
            coordinate: canonical,
            digest: `sha256:${"d".repeat(64)}`,
            size: 8,
            contentType: "text/plain",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab
          repo={rawRepository}
          canWrite
          artifactTarget={canonical}
        />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByDisplayValue("report%20final.txt"),
    ).toBeInTheDocument();
    expect(mockSearchRepositoryArtifacts).toHaveBeenCalledWith({
      path: { repositoryId: rawRepository.id },
      query: {
        q: canonical,
        pageSize: 50,
        pageToken: undefined,
      },
    });
  });
});
