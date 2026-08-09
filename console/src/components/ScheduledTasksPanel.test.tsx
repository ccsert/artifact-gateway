import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listRepositories,
  listScheduledTaskRuns,
  listScheduledTasks,
} from "../client";
import type { ScheduledTask } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { ScheduledTasksPanel } from "./ScheduledTasksPanel";

vi.mock("../client", () => ({
  createScheduledTask: vi.fn(),
  deleteScheduledTask: vi.fn(),
  listRepositories: vi.fn(),
  listScheduledTaskRuns: vi.fn(),
  listScheduledTasks: vi.fn(),
  runScheduledTask: vi.fn(),
  updateScheduledTask: vi.fn(),
}));

const mockListTasks = vi.mocked(listScheduledTasks);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRuns = vi.mocked(listScheduledTaskRuns);

function task(id: string, name: string): ScheduledTask {
  return {
    id,
    name,
    description: "",
    kind: "audit-retention",
    intervalMinutes: 60,
    enabled: true,
    nextRunAt: "2026-08-10T10:00:00Z",
    version: "1",
    createdAt: "2026-08-10T09:00:00Z",
    updatedAt: "2026-08-10T09:00:00Z",
  };
}

const renderPanel = () =>
  render(
    <PreferencesProvider>
      <ScheduledTasksPanel />
    </PreferencesProvider>,
  );

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ScheduledTasksPanel", () => {
  it("keeps exactly one dispatch history row expanded", async () => {
    const user = userEvent.setup();
    const first = task("11111111-1111-4111-8111-111111111111", "Audit one");
    const second = task("22222222-2222-4222-8222-222222222222", "Audit two");
    mockListTasks.mockResolvedValue({ data: [first, second] } as never);
    mockListRepositories.mockResolvedValue({
      data: { items: [], nextPageToken: undefined },
    } as never);
    mockListRuns.mockImplementation(
      async ({ path }) =>
        ({
          data: [
            {
              id:
                path.taskId === first.id
                  ? "33333333-3333-4333-8333-333333333333"
                  : "44444444-4444-4444-8444-444444444444",
              taskId: path.taskId,
              trigger: "manual",
              state: "submitted",
              scheduledAt: "2026-08-10T09:30:00Z",
              createdAt: "2026-08-10T09:30:00Z",
              targetKind: "audit-cleanup",
              targetId: path.taskId === first.id ? "job-first" : "job-second",
            },
          ],
        }) as never,
    );

    renderPanel();
    expect(await screen.findByText("Audit one")).toBeInTheDocument();
    const historyButtons = screen.getAllByRole("button", { name: "投递历史" });
    await user.click(historyButtons[0]);
    expect(await screen.findByText("job-first")).toBeInTheDocument();

    await user.click(historyButtons[1]);
    expect(await screen.findByText("job-second")).toBeInTheDocument();
    expect(screen.getByText("job-first")).not.toBeVisible();
    expect(mockListRuns).toHaveBeenCalledTimes(2);
  });

  it("renders an actionable empty state", async () => {
    mockListTasks.mockResolvedValue({ data: [] } as never);
    mockListRepositories.mockResolvedValue({
      data: { items: [], nextPageToken: undefined },
    } as never);
    renderPanel();
    expect(await screen.findByText("还没有计划任务")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /新建计划/ }),
    ).toBeInTheDocument();
  });
});
