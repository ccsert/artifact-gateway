import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listRepositoryArtifactUsage } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryUsageTab } from "./RepositoryUsageTab";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  listRepositoryArtifactUsage: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "" }),
}));

const mockListRepositoryArtifactUsage = vi.mocked(listRepositoryArtifactUsage);

const repository: Repository = {
  id: "22222222-2222-4222-8222-222222222222",
  name: "npm-usage",
  format: "npm",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryUsageTab", () => {
  it("recovers a removed last page with one bounded request", async () => {
    const user = userEvent.setup();
    const data = (totalCount: number) => ({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: {
          downloadCount: totalCount,
          totalBytes: totalCount,
          resources: totalCount,
        },
        totalCount,
        items: [],
      },
    });
    mockListRepositoryArtifactUsage
      .mockResolvedValueOnce(data(21) as never)
      .mockResolvedValueOnce(data(0) as never)
      .mockResolvedValueOnce(data(0) as never);
    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("第 1-20 项，共 21 项");
    await user.click(screen.getByTitle("2"));
    await vi.waitFor(() =>
      expect(mockListRepositoryArtifactUsage).toHaveBeenCalledTimes(3),
    );
    expect(mockListRepositoryArtifactUsage.mock.calls[1][0]?.query).toEqual({
      limit: 20,
      offset: 20,
      q: undefined,
    });
    expect(mockListRepositoryArtifactUsage.mock.calls[2][0]?.query).toEqual({
      limit: 20,
      offset: 0,
      q: undefined,
    });
  });

  it("shows only the initial error and recovers on retry", async () => {
    const user = userEvent.setup();
    mockListRepositoryArtifactUsage
      .mockRejectedValueOnce(new Error("initial read failed"))
      .mockResolvedValueOnce({
        data: {
          repositoryId: repository.id,
          generatedAt: "2026-09-17T08:00:00Z",
          totals: { downloadCount: 0, totalBytes: 0, resources: 0 },
          totalCount: 0,
          items: [],
        },
      } as never);
    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("initial read failed");
    expect(screen.queryByText("正在加载…")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /重\s*试/ }));
    await screen.findByText("暂无下载记录");
  });
  it("ignores a slow old refresh after a new search succeeds", async () => {
    const user = userEvent.setup();
    const response = (path: string) => ({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 2, totalBytes: 2, resources: 2 },
        totalCount: 1,
        items: [
          {
            format: "raw",
            resource: path,
            downloadCount: 1,
            totalBytes: 1,
            firstDownloadedAt: "2026-09-17T08:00:00Z",
            lastDownloadedAt: "2026-09-17T08:00:00Z",
          },
        ],
      },
    });
    let resolveOld!: (value: never) => void;
    mockListRepositoryArtifactUsage
      .mockResolvedValueOnce(response("initial") as never)
      .mockImplementationOnce(
        () =>
          new Promise<never>((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValueOnce(response("new-search") as never);
    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("initial");
    await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
    await user.type(
      screen.getByRole("searchbox", { name: "搜索制品地址" }),
      "new-search{Enter}",
    );
    await screen.findByText("new-search");
    await act(async () => resolveOld(response("old-refresh") as never));
    expect(screen.getByText("new-search")).toBeVisible();
    expect(screen.queryByText("old-refresh")).not.toBeInTheDocument();
    expect(mockListRepositoryArtifactUsage).toHaveBeenCalledTimes(3);
  });
  it("requests one bounded page and searches on the server with a matching total", async () => {
    const user = userEvent.setup();
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 400, totalBytes: 4096, resources: 201 },
        totalCount: 201,
        items: [],
      },
    } as never);
    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("暂无下载记录");
    expect(mockListRepositoryArtifactUsage).toHaveBeenCalledTimes(1);
    expect(mockListRepositoryArtifactUsage.mock.calls[0][0]?.query).toEqual({
      limit: 20,
      offset: 0,
      q: undefined,
    });
    await user.click(screen.getByTitle("2"));
    await vi.waitFor(() =>
      expect(
        mockListRepositoryArtifactUsage.mock.calls.at(-1)?.[0]?.query,
      ).toEqual({ limit: 20, offset: 20, q: undefined }),
    );
    await user.type(
      screen.getByRole("searchbox", { name: "搜索制品地址" }),
      "widget",
    );
    await user.keyboard("{Enter}");
    await vi.waitFor(() =>
      expect(
        mockListRepositoryArtifactUsage.mock.calls.at(-1)?.[0]?.query,
      ).toEqual({ limit: 20, offset: 0, q: "widget" }),
    );
    expect(screen.getByText("第 1-20 项，共 201 项")).toBeVisible();
    expect(screen.getByText(/全仓库：201 个地址/)).toBeVisible();
  });
  it("shows per-artifact downloads without redundant summary cards", async () => {
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 12, totalBytes: 1536, resources: 2 },
        totalCount: 2,
        items: [
          {
            format: "npm",
            resource: "@scope/widget@1.2.3",
            downloadCount: 10,
            totalBytes: 1024,
            firstDownloadedAt: "2026-09-01T08:00:00Z",
            lastDownloadedAt: "2026-09-17T07:59:00Z",
            lastActor: "build-agent",
          },
          {
            format: "npm",
            resource: "@scope/widget@2.0.0",
            downloadCount: 2,
            totalBytes: 512,
            firstDownloadedAt: "2026-09-16T08:00:00Z",
            lastDownloadedAt: "2026-09-17T07:00:00Z",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("@scope/widget@1.2.3")).toBeInTheDocument();
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.queryByText("累计下载")).not.toBeInTheDocument();
    expect(screen.getByText("build-agent")).toBeInTheDocument();
    expect(screen.queryByText("暂无下载记录")).not.toBeInTheDocument();
  });

  it("shows the empty state when nothing has been downloaded", async () => {
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 0, totalBytes: 0, resources: 0 },
        totalCount: 0,
        items: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("暂无下载记录")).toBeInTheDocument();
    expect(screen.queryByText("累计下载")).not.toBeInTheDocument();
  });

  it("reloads usage when refresh is clicked", async () => {
    const user = userEvent.setup();
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 0, totalBytes: 0, resources: 0 },
        totalCount: 0,
        items: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("暂无下载记录");
    await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
    await vi.waitFor(() => {
      expect(mockListRepositoryArtifactUsage).toHaveBeenCalledTimes(2);
    });
  });

  it("keeps loaded rows visible when refresh fails", async () => {
    const user = userEvent.setup();
    mockListRepositoryArtifactUsage
      .mockResolvedValueOnce({
        data: {
          repositoryId: repository.id,
          generatedAt: "2026-09-17T08:00:00Z",
          totals: { downloadCount: 1, totalBytes: 512, resources: 1 },
          totalCount: 1,
          items: [
            {
              format: "npm",
              resource: "widget@1.0.0",
              downloadCount: 1,
              totalBytes: 512,
              firstDownloadedAt: "2026-09-17T07:00:00Z",
              lastDownloadedAt: "2026-09-17T08:00:00Z",
            },
          ],
        },
      } as never)
      .mockRejectedValueOnce(new Error("temporary failure"));
    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );
    await screen.findByText("widget@1.0.0");
    await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
    expect(await screen.findByText("temporary failure")).toBeVisible();
    expect(screen.getByText("widget@1.0.0")).toBeVisible();
  });
});
