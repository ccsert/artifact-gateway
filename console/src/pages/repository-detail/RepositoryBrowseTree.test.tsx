import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { browseRepository } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryBrowseTree } from "./RepositoryBrowseTree";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  browseRepository: vi.fn(),
}));

const mockBrowseRepository = vi.mocked(browseRepository);

const repository: Repository = {
  id: "33333333-3333-4333-8333-333333333333",
  name: "raw-releases",
  format: "raw",
  type: "hosted",
  allowedHosts: [],
  anonymousRead: true,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryBrowseTree", () => {
  it("lazy-loads a Raw directory and opens the selected asset in list view", async () => {
    const user = userEvent.setup();
    const onOpenInList = vi.fn();
    mockBrowseRepository
      .mockResolvedValueOnce({
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
      } as never)
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              id: "node-release-notes",
              kind: "asset",
              name: "release notes.txt",
              hasChildren: false,
              path: "docs/release%20notes.txt",
              coordinate: "docs/release%20notes.txt",
              digest: `sha256:${"a".repeat(64)}`,
              size: 0,
              contentType: "text/plain",
              createdAt: "2026-08-27T08:00:00Z",
            },
          ],
        },
      } as never);

    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={repository} onOpenInList={onOpenInList} />
      </PreferencesProvider>,
    );

    await user.click(await screen.findByText("docs"));
    expect(await screen.findByText("release notes.txt")).toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenNthCalledWith(2, {
      path: { repositoryId: repository.id },
      query: { parent: "node-docs", pageSize: 50 },
    });

    await user.click(screen.getByText("release notes.txt"));
    expect(screen.getByText("docs/release%20notes.txt")).toBeInTheDocument();
    expect(screen.getAllByText("0 B")).toHaveLength(2);
    expect(screen.getByText("text/plain")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "在列表中查看" }));
    expect(onOpenInList).toHaveBeenCalledWith("docs/release%20notes.txt");
  });

  it("keeps the directory visible when a refresh fails", async () => {
    const user = userEvent.setup();
    mockBrowseRepository
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              id: "node-packages",
              kind: "directory",
              name: "packages",
              hasChildren: true,
              path: "packages",
            },
          ],
        },
      } as never)
      .mockResolvedValueOnce({
        error: { message: "temporary browse failure" },
      } as never);

    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={repository} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("packages")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /刷新/ }));

    expect(
      await screen.findByText("temporary browse failure"),
    ).toBeInTheDocument();
    expect(screen.getByText("packages")).toBeInTheDocument();
    await waitFor(() => expect(mockBrowseRepository).toHaveBeenCalledTimes(2));
  });

  it("appends a bounded root page without discarding earlier nodes", async () => {
    const user = userEvent.setup();
    mockBrowseRepository
      .mockResolvedValueOnce({
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
          nextPageToken: "root-next",
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              id: "node-packages",
              kind: "directory",
              name: "packages",
              hasChildren: true,
              path: "packages",
            },
          ],
        },
      } as never);

    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={repository} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("docs")).toBeInTheDocument();
    await user.click(screen.getByText("加载更多"));

    expect(await screen.findByText("packages")).toBeInTheDocument();
    expect(screen.getByText("docs")).toBeInTheDocument();
    expect(screen.queryByText("加载更多")).not.toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenNthCalledWith(2, {
      path: { repositoryId: repository.id },
      query: {
        parent: undefined,
        pageSize: 50,
        pageToken: "root-next",
      },
    });
  });
});
