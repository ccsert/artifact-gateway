import {
  within,
  cleanup,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { browseGroup, browseRepository } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryBrowseTree } from "./RepositoryBrowseTree";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  browseRepository: vi.fn(),
  browseGroup: vi.fn(),
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
    expect(screen.getByText("docs/release notes.txt")).toBeInTheDocument();
    expect(
      screen.queryByText("docs/release%20notes.txt"),
    ).not.toBeInTheDocument();
    expect(screen.getAllByText("0 B")).toHaveLength(2);
    expect(screen.getByText("text/plain")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "在列表中查看" }));
    expect(onOpenInList).toHaveBeenCalledWith(
      expect.objectContaining({
        coordinate: "docs/release%20notes.txt",
        path: "docs/release%20notes.txt",
      }),
    );
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

  it("shows only the initial error until a retry succeeds", async () => {
    const user = userEvent.setup();
    mockBrowseRepository
      .mockResolvedValueOnce({
        error: { message: "initial browse failure" },
      } as never)
      .mockResolvedValueOnce({ data: { items: [] } } as never);

    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={repository} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByText("initial browse failure"),
    ).toBeInTheDocument();
    expect(screen.queryByText("制品目录")).not.toBeInTheDocument();
    expect(screen.queryByText("暂无可浏览制品")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("暂无可浏览制品")).toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenCalledTimes(2);
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

describe("Proxy directory recovery", () => {
  const root = {
    id: "root-node",
    kind: "namespace",
    name: "org.example",
    hasChildren: true,
  };
  const child = {
    id: "child-node",
    kind: "component",
    name: "widget",
    hasChildren: true,
  };
  const proxy: Repository = {
    ...repository,
    format: "maven",
    type: "proxy",
    name: "maven-proxy",
  };

  it("retries a rejected child request at the same parent without discarding the tree", async () => {
    const user = userEvent.setup();
    mockBrowseRepository
      .mockResolvedValueOnce({ data: { items: [root] } } as never)
      .mockRejectedValueOnce(new Error("child unavailable"))
      .mockResolvedValueOnce({ data: { items: [child] } } as never);
    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={proxy} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );
    await user.click(await screen.findByText("org.example"));
    expect(await screen.findByText("child unavailable")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("widget")).toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenNthCalledWith(3, {
      path: { repositoryId: proxy.id },
      query: { parent: "root-node", pageSize: 50 },
    });
    expect(screen.queryByText("child unavailable")).not.toBeInTheDocument();
  });

  it("deduplicates rapid page loads and retries the failed page", async () => {
    const user = userEvent.setup();
    let finish: ((value: never) => void) | undefined;
    mockBrowseRepository
      .mockResolvedValueOnce({
        data: { items: [root], nextPageToken: "page-two" },
      } as never)
      .mockRejectedValueOnce(new Error("page unavailable"))
      .mockImplementationOnce(
        () =>
          new Promise<never>((resolve) => {
            finish = resolve;
          }),
      );
    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={proxy} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );
    await user.click(await screen.findByText("加载更多"));
    expect(await screen.findByText("page unavailable")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /重试/ }));
    await user.dblClick(screen.getByText("加载更多"));
    expect(mockBrowseRepository).toHaveBeenCalledTimes(3);
    finish?.({
      data: { items: [{ ...root, id: "second", name: "org.second" }] },
    } as never);
    expect(await screen.findByText("org.second")).toBeInTheDocument();
    expect(screen.getAllByText("org.second")).toHaveLength(1);
  });

  it("can expand the same opaque node again after a successful root refresh", async () => {
    const user = userEvent.setup();
    mockBrowseRepository
      .mockResolvedValueOnce({ data: { items: [root] } } as never)
      .mockResolvedValueOnce({ data: { items: [child] } } as never)
      .mockResolvedValueOnce({ data: { items: [root] } } as never)
      .mockResolvedValueOnce({
        data: { items: [{ ...child, name: "refreshed-widget" }] },
      } as never);
    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={proxy} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );
    await user.click(await screen.findByText("org.example"));
    expect(await screen.findByText("widget")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /刷新/ }));
    await waitFor(() =>
      expect(screen.queryByText("widget")).not.toBeInTheDocument(),
    );
    await user.click(screen.getByText("org.example"));
    expect(await screen.findByText("refreshed-widget")).toBeInTheDocument();
  });

  it("keeps a failed branch retryable when a concurrent sibling finishes", async () => {
    const user = userEvent.setup();
    let failFirst: ((error: Error) => void) | undefined;
    let finishSecond: ((value: never) => void) | undefined;
    mockBrowseRepository
      .mockResolvedValueOnce({
        data: {
          items: [root, { ...root, id: "other-root", name: "org.other" }],
        },
      } as never)
      .mockImplementationOnce(
        () =>
          new Promise<never>((_resolve, reject) => {
            failFirst = reject;
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<never>((resolve) => {
            finishSecond = resolve;
          }),
      )
      .mockResolvedValueOnce({ data: { items: [child] } } as never);
    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={proxy} onOpenInList={vi.fn()} />
      </PreferencesProvider>,
    );
    await user.click(await screen.findByText("org.example"));
    await user.click(screen.getByText("org.other"));
    failFirst?.(new Error("first branch unavailable"));
    expect(
      await screen.findByText("first branch unavailable"),
    ).toBeInTheDocument();
    finishSecond?.({
      data: { items: [{ ...child, id: "other-child", name: "other-widget" }] },
    } as never);
    expect(await screen.findByText("other-widget")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("widget")).toBeInTheDocument();
    expect(mockBrowseRepository).toHaveBeenNthCalledWith(4, {
      path: { repositoryId: proxy.id },
      query: { parent: "root-node", pageSize: 50 },
    });
  });

  it("keeps the server-issued SNAPSHOT path, build, digest and source when opening the list", async () => {
    const user = userEvent.setup();
    const open = vi.fn();
    const asset = {
      id: "snapshot-asset",
      kind: "asset",
      name: "widget-1.0-20260907.010203-2.jar",
      hasChildren: false,
      coordinate: "org.example:widget:1.0-SNAPSHOT",
      path: "org/example/widget/1.0-SNAPSHOT/widget-1.0-20260907.010203-2.jar",
      buildNumber: 2,
      digest: `sha256:${"a".repeat(64)}`,
      sourceRepositoryName: "maven-proxy",
      cacheState: "cached",
      cachedAt: "2026-09-07T01:02:03Z",
    };
    mockBrowseRepository.mockResolvedValueOnce({
      data: { items: [asset] },
    } as never);
    render(
      <PreferencesProvider>
        <RepositoryBrowseTree repo={proxy} onOpenInList={open} />
      </PreferencesProvider>,
    );
    await user.click(await screen.findByText(asset.name));
    expect(
      within(screen.getByRole("complementary", { name: "节点详情" })).getByText(
        "maven-proxy",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("已缓存")).toBeInTheDocument();
    expect(screen.getByText(asset.path)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "在列表中查看" }));
    expect(open).toHaveBeenCalledWith(asset);
  });
});

it("uses Group browse and retains conflicting source identities and scope-aware links", async () => {
  const user = userEvent.setup();
  vi.mocked(browseGroup).mockResolvedValue({
    data: {
      items: [
        {
          id: "asset",
          kind: "asset",
          name: "release.zip",
          hasChildren: false,
          coordinate: "release.zip",
          path: "release.zip",
          sources: [
            {
              repositoryId: "hosted-id",
              repositoryName: "releases",
              type: "hosted",
              resolutionOrder: 1,
              coordinate: "release.zip",
              path: "release.zip",
              digest: `sha256:${"a".repeat(64)}`,
              size: 100,
            },
            {
              repositoryId: "proxy-id",
              repositoryName: "mirror",
              type: "proxy",
              resolutionOrder: 3,
              coordinate: "release.zip",
              path: "release.zip",
              digest: `sha256:${"b".repeat(64)}`,
              cacheRepositoryName: "group",
            },
          ],
        },
      ],
      groupId: "group-id",
      groupName: "group",
      format: "raw",
      candidates: [],
    },
  } as never);
  render(
    <PreferencesProvider>
      <RepositoryBrowseTree
        repo={{ id: "group-id", name: "group", type: "group", format: "raw" }}
        onOpenInList={vi.fn()}
      />
    </PreferencesProvider>,
  );
  await user.click(await screen.findByText("release.zip"));
  expect(browseGroup).toHaveBeenCalledWith({
    path: { groupId: "group-id" },
    query: { pageSize: 50 },
  });
  expect(screen.getByRole("link", { name: "releases" })).toHaveAttribute(
    "href",
    expect.stringContaining("artifact=release.zip"),
  );
  expect(screen.getByRole("link", { name: "mirror" })).toHaveAttribute(
    "href",
    "/repositories/proxy-id",
  );
  expect(screen.getByText("缓存范围 · group")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "在列表中查看" }),
  ).not.toBeInTheDocument();
});
