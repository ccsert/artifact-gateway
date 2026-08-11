import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { searchRepositoryArtifacts } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryArtifactsTab } from "./RepositoryArtifactsTab";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  searchRepositoryArtifacts: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "" }),
}));

const mockSearchRepositoryArtifacts = vi.mocked(searchRepositoryArtifacts);

const repository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "debian",
  format: "apt",
  type: "proxy",
  endpoint: "https://deb.example.test/debian",
  allowedHosts: ["deb.example.test"],
  anonymousRead: true,
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
});
