import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import {
  getAuditRetentionPolicy,
  listAuditRetentionJobs,
  replaceAuditRetentionPolicy,
  listAuditPage,
  listRepositories,
  listGroups,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { AuditRetentionPage } from "./AuditRetention";
import { AuditsPage } from "./Audits";

vi.mock("../../client", async (original) => ({
  ...(await original<typeof import("../../client")>()),
  getAuditRetentionPolicy: vi.fn(),
  listAuditRetentionJobs: vi.fn(),
  replaceAuditRetentionPolicy: vi.fn(),
  executeAuditRetention: vi.fn(),
  listAuditPage: vi.fn(),
  listRepositories: vi.fn(),
  listGroups: vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  localStorage.clear();
});

it("does not silently write a v3 retention draft using a refreshed v4 token", async () => {
  const user = userEvent.setup();
  vi.mocked(getAuditRetentionPolicy)
    .mockResolvedValueOnce({
      data: { version: "3", enabled: true, keepDays: 90 },
    } as never)
    .mockResolvedValue({
      data: { version: "4", enabled: true, keepDays: 180 },
    } as never);
  vi.mocked(listAuditRetentionJobs).mockResolvedValue({ data: [] } as never);
  vi.mocked(replaceAuditRetentionPolicy).mockResolvedValue({
    data: { version: "5", enabled: true, keepDays: 30 },
  } as never);
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AuditRetentionPage />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  const days = await screen.findByRole("spinbutton");
  fireEvent.change(days, { target: { value: "30" } });
  expect(days).toHaveValue("30");
  await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
  await waitFor(() => expect(getAuditRetentionPolicy).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(days).toHaveValue("180"));
  await user.click(screen.getByRole("button", { name: "保存策略" }));
  expect(replaceAuditRetentionPolicy).not.toHaveBeenCalledWith({
    body: expect.objectContaining({ keepDays: 30 }),
    headers: { "If-Match": "4" },
  });
  fireEvent.change(days, { target: { value: "45" } });
  await user.click(screen.getByRole("button", { name: "保存策略" }));
  await waitFor(() =>
    expect(replaceAuditRetentionPolicy).toHaveBeenCalledWith({
      body: expect.objectContaining({ keepDays: 45 }),
      headers: { "If-Match": "4" },
    }),
  );
});

it("retains the displayed second audit page if first-page refresh fails", async () => {
  const user = userEvent.setup();
  const record = (actor: string) => ({
    actor,
    occurredAt: "2026-10-12T08:00:00Z",
    operation: "get",
    outcome: "resolved",
    repository: "stable",
  });
  vi.mocked(listRepositories).mockResolvedValue({
    data: { items: [] },
  } as never);
  vi.mocked(listGroups).mockResolvedValue({ data: { items: [] } } as never);
  vi.mocked(listAuditPage)
    .mockResolvedValueOnce({
      data: { items: [record("user:first")], nextPageToken: "cursor-2" },
    } as never)
    .mockResolvedValueOnce({
      data: { items: [record("user:second")] },
    } as never)
    .mockResolvedValue({
      error: { status: 503, message: "refresh unavailable" },
    } as never);
  render(
    <MemoryRouter>
      <TestQueryProvider>
        <PreferencesProvider>
          <AuditsPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await screen.findByText("user:first");
  await user.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("user:second");
  await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
  await screen.findByText("refresh unavailable");
  expect(screen.getByText("user:second")).toBeInTheDocument();
  expect(screen.getByText("第 2 页 · 当前页 1 条")).toBeInTheDocument();
});

it("can filter by a group from the second cursor page in the 200-group window", async () => {
  const user = userEvent.setup();
  const groups = (start: number) =>
    Array.from({ length: 100 }, (_, index) => ({
      id: `id-${start + index}`,
      name: `group-${start + index}`,
    }));
  vi.mocked(listRepositories).mockResolvedValue({
    data: { items: [] },
  } as never);
  vi.mocked(listGroups)
    .mockResolvedValueOnce({
      data: { items: groups(1), nextPageToken: "groups-2" },
    } as never)
    .mockResolvedValue({
      data: { items: groups(101), nextPageToken: "groups-3" },
    } as never);
  vi.mocked(listAuditPage).mockResolvedValue({ data: { items: [] } } as never);
  render(
    <MemoryRouter>
      <TestQueryProvider>
        <PreferencesProvider>
          <AuditsPage />
        </PreferencesProvider>
      </TestQueryProvider>
    </MemoryRouter>,
  );
  await user.click(screen.getByText("高级筛选"));
  const label = screen.getByText("所属分组");
  const field = label.closest("label");
  if (!field) throw new Error("Missing group filter field");
  const select = within(field).getByRole("combobox");
  await user.click(select);
  await user.type(select, "group-150");
  await user.click(
    await screen.findByText("group-150", {
      selector: ".ant-select-item-option-content",
    }),
  );
  await waitFor(() =>
    expect(listAuditPage).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ group: "group-150" }),
      }),
    ),
  );
  expect(listGroups).toHaveBeenCalledTimes(2);
  expect(vi.mocked(listGroups).mock.calls[1][0]?.query?.pageToken).toBe(
    "groups-2",
  );
});

it("keeps retention edits after a failed refresh and same-version retry", async () => {
  const user = userEvent.setup();
  const policy = { version: "3", enabled: true, keepDays: 90 };
  vi.mocked(getAuditRetentionPolicy).mockResolvedValue({
    data: policy,
  } as never);
  vi.mocked(listAuditRetentionJobs).mockResolvedValue({ data: [] } as never);
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AuditRetentionPage />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  const days = await screen.findByRole("spinbutton");
  fireEvent.change(days, { target: { value: "30" } });
  vi.mocked(getAuditRetentionPolicy).mockResolvedValue({
    error: { status: 503, message: "policy unavailable" },
  } as never);
  await user.click(screen.getByRole("button", { name: /刷\s*新/ }));
  await screen.findByText("policy unavailable");
  expect(screen.getByRole("spinbutton")).toHaveValue("30");
  vi.mocked(getAuditRetentionPolicy).mockResolvedValue({
    data: policy,
  } as never);
  await user.click(screen.getByRole("button", { name: /重试/ }));
  await waitFor(() =>
    expect(screen.queryByText("policy unavailable")).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("spinbutton")).toHaveValue("30");
});
