import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createNexusMigrationSchedule,
  getNexusMigrationCapabilities,
  listNexusMigrationBatches,
  listNexusMigrationSchedules,
  pauseNexusMigrationSchedule,
  refreshNexusMigrationScheduleCapacity,
  resumeNexusMigrationSchedule,
  stopNexusMigrationSchedule,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { nexusBatch, nexusCapabilities } from "../../test/nexusBatchFixtures";
import { nexusSchedule } from "../../test/nexusScheduleFixtures";
import { NexusSchedulesPanel } from "./NexusSchedulesPanel";

vi.mock("../../client", () => ({
  createNexusMigrationSchedule: vi.fn(),
  getNexusMigrationCapabilities: vi.fn(),
  listNexusMigrationBatches: vi.fn(),
  listNexusMigrationSchedules: vi.fn(),
  pauseNexusMigrationSchedule: vi.fn(),
  refreshNexusMigrationScheduleCapacity: vi.fn(),
  resumeNexusMigrationSchedule: vi.fn(),
  stopNexusMigrationSchedule: vi.fn(),
}));

const list = vi.mocked(listNexusMigrationSchedules);
function show() {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter>
            <NexusSchedulesPanel />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}
beforeEach(() => {
  localStorage.setItem("ag.console.locale", "zh-CN");
  vi.mocked(getNexusMigrationCapabilities).mockResolvedValue({
    data: nexusCapabilities,
  } as never);
  list.mockResolvedValue({
    data: { items: [nexusSchedule], nextCursor: "" },
  } as never);
  vi.mocked(listNexusMigrationBatches).mockResolvedValue({
    data: { items: [nexusBatch], nextCursor: "" },
  } as never);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  localStorage.clear();
});

describe("durable migration schedules", () => {
  it("renders the capacity boundary and persisted dates without dispatching", async () => {
    show();
    expect(await screen.findByText("等待新容量观测")).toBeInTheDocument();
    expect(screen.getByText("最近成功追平")).toBeInTheDocument();
    expect(screen.getByText(/还不是完全无人值守迁移/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看最近批次" })).toHaveAttribute(
      "href",
      expect.stringContaining(nexusBatch.id),
    );
    expect(createNexusMigrationSchedule).not.toHaveBeenCalled();
    expect(refreshNexusMigrationScheduleCapacity).not.toHaveBeenCalled();
  });
  it("renders an initial error without a spinner or an empty result", async () => {
    list.mockResolvedValue({ error: { status: 503 } } as never);
    show();
    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(screen.queryByText("还没有周期计划")).not.toBeInTheDocument();
    expect(screen.queryByTestId("nexus-schedule-card")).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
  it("retains loaded plans when a refresh fails", async () => {
    const user = userEvent.setup();
    show();
    await screen.findByTestId("nexus-schedule-card");
    list.mockResolvedValue({ error: { status: 503 } } as never);
    await user.click(screen.getByRole("button", { name: "刷新计划" }));
    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(screen.getByTestId("nexus-schedule-card")).toBeInTheDocument();
  });
  it("creates a waiting plan from a successful batch without copying its capacity", async () => {
    const user = userEvent.setup();
    vi.mocked(createNexusMigrationSchedule).mockResolvedValue({
      data: nexusSchedule,
    } as never);
    show();
    await screen.findByTestId("nexus-schedule-card");
    await user.click(screen.getByRole("button", { name: "创建周期计划" }));
    const form = await screen.findByTestId("nexus-schedule-create");
    await user.click(within(form).getByRole("combobox", { name: "成功批次" }));
    await user.click(
      await screen.findByText(
        `releases, raw-source · ${nexusBatch.id.slice(0, 8)}`,
        { selector: ".ant-select-item-option-content" },
      ),
    );
    await user.click(
      within(form).getByRole("button", { name: "创建周期计划" }),
    );
    await waitFor(() =>
      expect(createNexusMigrationSchedule).toHaveBeenCalledTimes(1),
    );
    expect(vi.mocked(createNexusMigrationSchedule).mock.calls[0][0]).toEqual(
      expect.objectContaining({
        body: { templateBatchId: nexusBatch.id, intervalSeconds: 3600 },
        headers: { "Idempotency-Key": expect.any(String) },
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByTestId("nexus-schedule-create"),
      ).not.toBeInTheDocument(),
    );
  });
  it("submits explicit new evidence with CAS and preserves inputs after a stale response", async () => {
    const user = userEvent.setup();
    vi.mocked(refreshNexusMigrationScheduleCapacity).mockResolvedValue({
      error: { status: 412 },
    } as never);
    show();
    await screen.findByTestId("nexus-schedule-card");
    await user.click(screen.getByRole("button", { name: "补充容量观测" }));
    const form = await screen.findByTestId("nexus-schedule-capacity");
    const submit = within(form).getByRole("button", { name: "提交新观测" });
    expect(submit).toBeDisabled();
    const observed = new Date(Date.now() - 1000).toISOString();
    const until = new Date(Date.now() + 60_000).toISOString();
    await user.type(
      within(form).getByRole("textbox", { name: "容量观察时间（含时区）" }),
      observed,
    );
    await user.type(
      within(form).getByRole("textbox", { name: "证据有效截止时间（含时区）" }),
      until,
    );
    const fields = within(form).getAllByRole("textbox");
    for (const field of fields.slice(2)) await user.type(field, "1000");
    await user.click(submit);
    await waitFor(() =>
      expect(refreshNexusMigrationScheduleCapacity).toHaveBeenCalledTimes(1),
    );
    expect(
      vi.mocked(refreshNexusMigrationScheduleCapacity).mock.calls[0][0],
    ).toEqual(
      expect.objectContaining({
        path: { scheduleId: nexusSchedule.id },
        headers: {
          "If-Match": nexusSchedule.version,
          "Idempotency-Key": expect.any(String),
        },
        body: {
          capacityEvidence: {
            targetId: nexusCapabilities.targetId,
            poolId: nexusCapabilities.poolId,
            observedAt: observed,
            validUntil: until,
            freeBytes: 1000,
            downloadBytes: 1000,
            uploadBytes: 1000,
            backupBytes: 1000,
            restoreBytes: 1000,
            headroomBytes: 1000,
          },
        },
      }),
    );
    expect(await within(form).findByRole("alert")).toBeInTheDocument();
    expect(
      within(form).getByRole("textbox", { name: "存储池可用字节" }),
    ).toHaveValue("1000");
    expect(resumeNexusMigrationSchedule).not.toHaveBeenCalled();
  });
  it("pauses only the plan and shows no future controls for stopped plans", async () => {
    const user = userEvent.setup();
    vi.mocked(pauseNexusMigrationSchedule).mockImplementation(async () => {
      const paused = { ...nexusSchedule, state: "paused", version: "3" };
      list.mockResolvedValue({
        data: { items: [paused], nextCursor: "" },
      } as never);
      return { data: paused } as never;
    });
    show();
    await screen.findByTestId("nexus-schedule-card");
    await user.click(screen.getByRole("button", { name: "暂停计划" }));
    expect(await screen.findByText("计划已暂停")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "更新容量并恢复" }),
    ).toBeInTheDocument();
    expect(pauseNexusMigrationSchedule).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { scheduleId: nexusSchedule.id },
        headers: {
          "If-Match": nexusSchedule.version,
          "Idempotency-Key": expect.any(String),
        },
      }),
    );
    list.mockResolvedValue({
      data: {
        items: [{ ...nexusSchedule, state: "stopped", nextRunAt: null }],
        nextCursor: "",
      },
    } as never);
    await user.click(screen.getByRole("button", { name: "刷新计划" }));
    expect(await screen.findByText("已停止，不再触发")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "暂停计划" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "更新容量并恢复" }),
    ).not.toBeInTheDocument();
    expect(stopNexusMigrationSchedule).not.toHaveBeenCalled();
  });
});
