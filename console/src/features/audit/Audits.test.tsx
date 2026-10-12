import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { listAuditPage, listGroups, listRepositories } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { AuditsPage } from "./Audits";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  listAuditPage: vi.fn(),
  listGroups: vi.fn(),
  listRepositories: vi.fn(),
}));

const record = (actor: string) => ({
  actor,
  occurredAt: "2026-10-12T08:00:00Z",
  operation: "get",
  outcome: "resolved",
  repository: "stable-releases",
});

function renderPage() {
  vi.mocked(listRepositories).mockResolvedValue({
    data: { items: [] },
  } as never);
  vi.mocked(listGroups).mockResolvedValue({ data: { items: [] } } as never);
  render(
    <MemoryRouter>
      <TestQueryProvider>
        <PreferencesProvider>
          <AuditsPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(listAuditPage).mockReset();
});

it("pages forward by cursor, back from cache, and keeps the page when the next one fails", async () => {
  const user = userEvent.setup();
  vi.mocked(listAuditPage)
    .mockResolvedValueOnce({
      data: { items: [record("user:first")], nextPageToken: "cursor-2" },
    } as never)
    .mockResolvedValueOnce({
      data: { items: [record("user:second")], nextPageToken: "cursor-3" },
    } as never)
    .mockResolvedValueOnce({
      error: { status: 400, message: "cursor expired" },
    } as never);
  renderPage();

  expect(await screen.findByText("user:first")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(await screen.findByText("user:second")).toBeInTheDocument();
  expect(screen.getByText("第 2 页 · 当前页 1 条")).toBeInTheDocument();
  expect(vi.mocked(listAuditPage).mock.calls[1][0]?.query).toMatchObject({
    pageSize: 100,
    pageToken: "cursor-2",
  });

  await user.click(screen.getByRole("button", { name: "上一页" }));
  expect(await screen.findByText("user:first")).toBeInTheDocument();
  expect(listAuditPage).toHaveBeenCalledTimes(2);

  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(await screen.findByText("user:second")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(await screen.findByText("cursor expired")).toBeInTheDocument();
  expect(screen.getByText("user:second")).toBeInTheDocument();
  expect(screen.getByText("第 2 页 · 当前页 1 条")).toBeInTheDocument();
  expect(listAuditPage).toHaveBeenCalledTimes(3);
});

it("refreshes from the first page with one request", async () => {
  const user = userEvent.setup();
  vi.mocked(listAuditPage)
    .mockResolvedValueOnce({
      data: { items: [record("user:first")], nextPageToken: "cursor-2" },
    } as never)
    .mockResolvedValueOnce({
      data: { items: [record("user:second")] },
    } as never)
    .mockResolvedValueOnce({
      data: { items: [record("user:newest")], nextPageToken: "cursor-2b" },
    } as never);
  renderPage();

  await screen.findByText("user:first");
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("user:second");
  await user.click(screen.getByRole("button", { name: /刷\s*新/ }));

  expect(await screen.findByText("user:newest")).toBeInTheDocument();
  expect(screen.getByText("第 1 页 · 当前页 1 条")).toBeInTheDocument();
  await waitFor(() => expect(listAuditPage).toHaveBeenCalledTimes(3));
  expect(vi.mocked(listAuditPage).mock.calls[2][0]?.query).not.toHaveProperty(
    "pageToken",
    "cursor-2",
  );
});
