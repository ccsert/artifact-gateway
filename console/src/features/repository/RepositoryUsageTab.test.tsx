import { cleanup, render, screen } from "@testing-library/react";
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
  it("renders lifetime totals and per-artifact download counts", async () => {
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 12, totalBytes: 1536, resources: 2 },
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
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText("build-agent")).toBeInTheDocument();
    expect(screen.queryByText("暂无下载记录")).not.toBeInTheDocument();
  });

  it("shows the empty state when nothing has been downloaded", async () => {
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 0, totalBytes: 0, resources: 0 },
        items: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryUsageTab repo={repository} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("暂无下载记录")).toBeInTheDocument();
    expect(screen.getAllByText("0").length).toBeGreaterThanOrEqual(1);
  });

  it("reloads usage when refresh is clicked", async () => {
    const user = userEvent.setup();
    mockListRepositoryArtifactUsage.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        generatedAt: "2026-09-17T08:00:00Z",
        totals: { downloadCount: 0, totalBytes: 0, resources: 0 },
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
});
