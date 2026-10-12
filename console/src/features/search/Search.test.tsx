import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { searchArtifacts } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { SearchPage } from "./Search";
import userEvent from "@testing-library/user-event";
import { TestQueryProvider } from "../../test/queryClient";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  searchArtifacts: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "operator-token" }),
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("cancels an old query, ignores its late answer, and retries an identical query", async () => {
  let answerOld!: (value: never) => void;
  vi.mocked(searchArtifacts)
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answerOld = resolve;
        }),
    )
    .mockResolvedValueOnce({
      data: { searchedRepositories: 1, items: [] },
    } as never)
    .mockRejectedValueOnce(new Error("fixture unavailable"));
  render(
    <MemoryRouter initialEntries={["/search?q=alpha"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await waitFor(() => expect(searchArtifacts).toHaveBeenCalledTimes(1));
  const signal = vi.mocked(searchArtifacts).mock.calls[0][0]?.signal;
  fireEvent.click(screen.getByRole("radio", { name: "前缀" }));
  await waitFor(() => expect(searchArtifacts).toHaveBeenCalledTimes(2));
  expect(signal?.aborted).toBe(true);
  await act(async () =>
    answerOld({
      data: {
        searchedRepositories: 1,
        items: [
          {
            repositoryId: "old",
            repositoryName: "stale",
            format: "raw",
            matchKind: "coordinate",
            coordinate: "old-alpha.bin",
          },
        ],
      },
    } as never),
  );
  expect(screen.queryByText("old-alpha.bin")).not.toBeInTheDocument();
  fireEvent.keyDown(screen.getByRole("searchbox"), {
    key: "Enter",
    code: "Enter",
  });
  await waitFor(() => expect(searchArtifacts).toHaveBeenCalledTimes(3));
  expect(await screen.findByText("fixture unavailable")).toBeInTheDocument();
  expect(vi.mocked(searchArtifacts).mock.calls[2][0]?.query).toEqual({
    q: "alpha",
    match: "prefix",
    pageSize: 100,
  });
});

it("retains the first page after a rejected continuation and releases its loading guard", async () => {
  const first = {
    repositoryId: "first",
    repositoryName: "raw",
    format: "raw",
    matchKind: "coordinate",
    coordinate: "alpha.bin",
  };
  vi.mocked(searchArtifacts)
    .mockResolvedValueOnce({
      data: { searchedRepositories: 1, items: [first], nextPageToken: "next" },
    } as never)
    .mockRejectedValueOnce(new Error("continuation unavailable"))
    .mockResolvedValueOnce({
      data: {
        searchedRepositories: 1,
        items: [{ ...first, coordinate: "alpha-2.bin" }],
      },
    } as never);
  render(
    <MemoryRouter initialEntries={["/search?q=alpha"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await screen.findByText("alpha.bin");
  fireEvent.click(screen.getByRole("button", { name: "加载更多" }));
  expect(
    await screen.findByText("continuation unavailable"),
  ).toBeInTheDocument();
  expect(screen.getByText("alpha.bin")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "加载更多" }));
  expect(await screen.findByText("alpha-2.bin")).toBeInTheDocument();
  expect(vi.mocked(searchArtifacts).mock.calls[2][0]?.query).toEqual({
    q: "alpha",
    match: "contains",
    pageSize: 100,
    pageToken: "next",
  });
});

it("retains existing results and cursor after an identical search refresh rejects", async () => {
  vi.mocked(searchArtifacts)
    .mockResolvedValueOnce({
      data: {
        searchedRepositories: 1,
        items: [
          {
            repositoryId: "first",
            repositoryName: "raw",
            format: "raw",
            matchKind: "coordinate",
            coordinate: "alpha.bin",
          },
        ],
        nextPageToken: "next",
      },
    } as never)
    .mockRejectedValueOnce(new Error("refresh unavailable"));
  render(
    <MemoryRouter initialEntries={["/search?q=alpha"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await screen.findByText("alpha.bin");
  fireEvent.keyDown(screen.getByRole("searchbox"), {
    key: "Enter",
    code: "Enter",
  });
  expect(await screen.findByText("refresh unavailable")).toBeInTheDocument();
  expect(screen.getByText("alpha.bin")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "加载更多" })).toBeEnabled();
});

it("requests literal contains matching by default", async () => {
  vi.mocked(searchArtifacts).mockResolvedValue({
    data: { searchedRepositories: 1, items: [] },
  } as never);
  render(
    <MemoryRouter initialEntries={["/search?q=alpha"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(searchArtifacts).toHaveBeenCalledWith(
      expect.objectContaining({
        query: { q: "alpha", pageSize: 100, match: "contains" },
      }),
    ),
  );
});

it("shows unavailable detail for a search hit from a newer server format", async () => {
  vi.mocked(searchArtifacts).mockResolvedValue({
    data: {
      searchedRepositories: 1,
      items: [
        {
          repositoryId: "new-format",
          repositoryName: "nuget-hosted",
          format: "nuget",
          matchKind: "coordinate",
          coordinate: "Widget:1.0",
        },
      ],
    },
  } as never);
  render(
    <MemoryRouter initialEntries={["/search?q=Widget"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await userEvent.setup().click(await screen.findByText("Widget:1.0"));
  expect(
    await screen.findByText("当前 Console 尚未支持 nuget 格式"),
  ).toBeInTheDocument();
});

it("presents Raw global-search hits with a readable file path", async () => {
  vi.mocked(searchArtifacts).mockResolvedValue({
    data: {
      searchedRepositories: 1,
      items: [
        {
          repositoryId: "33333333-3333-4333-8333-333333333333",
          repositoryName: "raw-releases",
          format: "raw",
          matchKind: "coordinate",
          coordinate: "ChatGPT%20Image%20%282%29.png",
          digest: `sha256:${"c".repeat(64)}`,
          size: 1024,
        },
      ],
    },
  } as never);

  render(
    <MemoryRouter initialEntries={["/search?q=ChatGPT%20Image"]}>
      <TestQueryProvider>
        <PreferencesProvider>
          <SearchPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );

  expect(await screen.findByText("ChatGPT Image (2).png")).toBeInTheDocument();
  expect(
    screen.queryByText("ChatGPT%20Image%20%282%29.png"),
  ).not.toBeInTheDocument();
});
