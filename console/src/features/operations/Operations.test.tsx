import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  listAuditRetentionJobs,
  listLifecycleJobs,
  runRepositoryLifecycleJobNow,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { OperationsPage } from "./Operations";

vi.mock("../../client", () => ({
  cancelRepositoryLifecycleJob: vi.fn(),
  listAuditRetentionJobs: vi.fn(),
  listLifecycleJobs: vi.fn(),
  retryRepositoryLifecycleJob: vi.fn(),
  runRepositoryLifecycleJobNow: vi.fn(),
}));

vi.mock("./ScheduledTasksPanel", () => ({
  ScheduledTasksPanel: () => <div>scheduled tasks</div>,
}));
vi.mock("./WebhookDeliveriesPanel", () => ({
  WebhookDeliveriesPanel: () => <div>webhook deliveries</div>,
}));

const mockListLifecycleJobs = vi.mocked(listLifecycleJobs);
const mockListAuditRetentionJobs = vi.mocked(listAuditRetentionJobs);
const mockRunNow = vi.mocked(runRepositoryLifecycleJobNow);

const repositoryId = "00000000-0000-4000-8000-000000000001";

function lifecycleRecord(state: string) {
  return {
    repositoryId,
    repositoryName: "releases",
    job: {
      id: "job-1",
      kind: "retention",
      state,
      createdAt: "2026-10-01T08:00:00Z",
      attempts: 1,
      maxAttempts: 5,
      progressCurrent: 0,
      progressTotal: 0,
    },
  };
}

function renderPage(path: string) {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter initialEntries={[path]}>
            <OperationsPage />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}

// The card layout renders every row in jsdom; the virtual desktop table does
// not lay out without real geometry.
beforeEach(() => {
  const stub = window.matchMedia;
  vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
    ...stub(query),
    matches: query.includes("max-width"),
  }));
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("OperationsPage job history", () => {
  it("loads when the page opens directly on the job history tab", async () => {
    mockListLifecycleJobs.mockResolvedValue({
      data: [lifecycleRecord("failed")],
    } as never);
    mockListAuditRetentionJobs.mockResolvedValue({ data: [] } as never);

    renderPage("/operations?tab=jobs");

    expect(await screen.findByText("releases")).toBeInTheDocument();
    expect(mockListLifecycleJobs).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("加载任务状态…")).not.toBeInTheDocument();
  });

  it("queues a pending job and re-reads the history", async () => {
    mockListLifecycleJobs
      .mockResolvedValueOnce({ data: [lifecycleRecord("pending")] } as never)
      .mockResolvedValue({ data: [lifecycleRecord("running")] } as never);
    mockListAuditRetentionJobs.mockResolvedValue({ data: [] } as never);
    mockRunNow.mockResolvedValue({ data: {} } as never);

    renderPage("/operations?tab=jobs");
    await userEvent.click(
      await screen.findByRole("button", { name: "立即加入执行队列" }),
    );

    await waitFor(() =>
      expect(mockRunNow).toHaveBeenCalledWith({
        path: { repositoryId, lifecycleJobId: "job-1" },
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "立即加入执行队列" }),
      ).not.toBeInTheDocument(),
    );
    expect(mockListLifecycleJobs).toHaveBeenCalledTimes(2);
  });

  it("does not read the job history until its tab is opened", async () => {
    mockListLifecycleJobs.mockResolvedValue({ data: [] } as never);
    mockListAuditRetentionJobs.mockResolvedValue({ data: [] } as never);

    renderPage("/operations?tab=webhooks");

    await screen.findByRole("tab", { name: "执行记录" });
    expect(mockListLifecycleJobs).not.toHaveBeenCalled();
  });

  it("re-reads malformed job data when the operator retries", async () => {
    const record = lifecycleRecord("failed");
    mockListLifecycleJobs
      .mockResolvedValueOnce({
        data: [
          { ...record, repositoryName: null },
          {
            ...record,
            repositoryId: "00000000-0000-4000-8000-000000000002",
            repositoryName: null,
            job: { ...record.job, id: "job-2" },
          },
        ],
      } as never)
      .mockResolvedValue({ data: [record] } as never);
    mockListAuditRetentionJobs.mockResolvedValue({ data: [] } as never);
    vi.spyOn(console, "error").mockImplementation(() => undefined);

    renderPage("/operations?tab=jobs");
    await userEvent.click(await screen.findByRole("button", { name: /重试/ }));

    expect(await screen.findByText("releases")).toBeInTheDocument();
    expect(mockListLifecycleJobs).toHaveBeenCalledTimes(2);
    expect(mockListAuditRetentionJobs).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("请求出错")).not.toBeInTheDocument();
  });
});
