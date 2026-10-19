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
  getNexusMigrationCapabilities,
  getNexusMigrationBatch,
  listNexusMigrationBatches,
  listNexusMigrationEntries,
  getRepository,
  pauseNexusMigrationBatch,
  resumeNexusMigrationBatch,
  retryNexusMigrationBatch,
  cancelNexusMigrationBatch,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import {
  nexusBatch,
  nexusCapabilities,
  nexusEntries,
  nexusTargets,
} from "../../test/nexusBatchFixtures";
import { NexusBatchesPanel } from "./NexusBatchesPanel";

vi.mock("../../client", () => ({
  getNexusMigrationCapabilities: vi.fn(),
  getNexusMigrationBatch: vi.fn(),
  listNexusMigrationBatches: vi.fn(),
  listNexusMigrationEntries: vi.fn(),
  getRepository: vi.fn(),
  pauseNexusMigrationBatch: vi.fn(),
  resumeNexusMigrationBatch: vi.fn(),
  retryNexusMigrationBatch: vi.fn(),
  cancelNexusMigrationBatch: vi.fn(),
}));
const list = vi.mocked(listNexusMigrationBatches);
const detail = vi.mocked(getNexusMigrationBatch);
const entries = vi.mocked(listNexusMigrationEntries);
function show(id = nexusBatch.id) {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter
            initialEntries={[
              `/operations?tab=migrations&view=batches${id ? `&batch=${id}` : ""}`,
            ]}
          >
            <NexusBatchesPanel draft={null} onDraftConsumed={() => undefined} />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}
beforeEach(() => {
  vi.mocked(getNexusMigrationCapabilities).mockResolvedValue({
    data: nexusCapabilities,
  } as never);
  list.mockResolvedValue({
    data: { items: [nexusBatch], nextCursor: "" },
  } as never);
  detail.mockResolvedValue({ data: nexusBatch } as never);
  entries.mockResolvedValue({
    data: { items: nexusEntries, nextCursor: "" },
  } as never);
  vi.mocked(getRepository).mockImplementation(
    async (options) =>
      ({
        data: nexusTargets.find(
          (target) => target.id === options.path.repositoryId,
        ),
      }) as never,
  );
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
describe("real batch read/control states", () => {
  it("shows exactly one initial error branch and sanitizes unknown transport text", async () => {
    list.mockRejectedValue(new Error("password=never-show"));
    show("");
    await screen.findByText(/批次请求未完成/);
    expect(screen.queryByText("尚无迁移批次")).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("never-show");
  });
  it("retains loaded facts on refresh failure and keeps nullable total unknown", async () => {
    detail.mockResolvedValue({
      data: { ...nexusBatch, totalBytes: null, scanComplete: false },
    } as never);
    show();
    await screen.findByText("未知；完整清单尚未封存");
    expect(
      screen.getByText("本批次 target-only 冷客户端验收：未验证"),
    ).toBeInTheDocument();
    list.mockRejectedValue(new Error("private-upstream"));
    await userEvent.click(
      screen.getAllByRole("button", { name: "刷新批次" })[0],
    );
    await screen.findByText(/批次请求未完成/);
    expect(
      screen.getByRole("heading", { name: nexusBatch.id }),
    ).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("private-upstream");
  });
  it("reads each entry cursor and exposes full digest only as the server observation", async () => {
    entries.mockImplementation(
      async (options) =>
        ({
          data: options.query?.cursor
            ? { items: [nexusEntries[1]], nextCursor: "" }
            : { items: [nexusEntries[0]], nextCursor: "next-page" },
        }) as never,
    );
    show();
    await screen.findByText("已加载 1 / 2 个计划条目");
    expect(screen.getByText(/库存尚未完整加载/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "读取完整库存" }));
    await screen.findByText("已加载 2 / 2 个计划条目");
    await waitFor(() =>
      expect(screen.queryByText(/库存尚未完整加载/)).not.toBeInTheDocument(),
    );
    expect(entries).toHaveBeenCalledWith(
      expect.objectContaining({ query: { limit: 100, cursor: "next-page" } }),
    );
    await userEvent.click(screen.getAllByRole("button", { name: "展开行" })[0]);
    expect(
      screen.getByText(nexusEntries[0].assets[0].sha256),
    ).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(
      "must-not-export-upstream-validator",
    );
  });
  it("pauses with CAS and idempotency then GETs authoritative state even if replay body is older", async () => {
    const running = { ...nexusBatch, state: "running" as const };
    detail.mockResolvedValue({ data: running } as never);
    vi.mocked(pauseNexusMigrationBatch).mockImplementation(async () => {
      detail.mockResolvedValue({
        data: { ...nexusBatch, state: "paused", version: "12" },
      } as never);
      return { data: { ...running, state: "pausing", version: "8" } } as never;
    });
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "暂停批次" }),
    );
    await screen.findByRole("button", { name: "恢复批次" });
    expect(vi.mocked(pauseNexusMigrationBatch)).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { batchId: nexusBatch.id },
        headers: { "If-Match": "7", "Idempotency-Key": expect.any(String) },
      }),
    );
    expect(screen.getByText("批次版本 12")).toBeInTheDocument();
  });
  it("keeps the identical control request key on uncertain retry and never renders an upstream error", async () => {
    detail.mockResolvedValue({
      data: { ...nexusBatch, state: "running" },
    } as never);
    vi.mocked(pauseNexusMigrationBatch).mockRejectedValue(
      new Error("password=hide-me"),
    );
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "暂停批次" }),
    );
    await screen.findByText(/提交结果尚未确认/);
    await userEvent.click(screen.getByRole("button", { name: "暂停批次" }));
    await waitFor(() =>
      expect(pauseNexusMigrationBatch).toHaveBeenCalledTimes(2),
    );
    const calls = vi.mocked(pauseNexusMigrationBatch).mock.calls;
    expect(calls[0][0].headers?.["Idempotency-Key"]).toBe(
      calls[1][0].headers?.["Idempotency-Key"],
    );
    expect(document.body).not.toHaveTextContent("hide-me");
  });
  it("leaves resume blank, blocks unknown evidence, and uses a fresh explicit runtime-bound observation", async () => {
    detail.mockResolvedValue({
      data: { ...nexusBatch, state: "paused" },
    } as never);
    vi.mocked(resumeNexusMigrationBatch).mockResolvedValue({
      error: { status: 412 },
    } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "恢复批次" }),
    );
    // rc-util uses a fixed ID in jsdom; table pagination also owns that ID.
    // The real-browser gate checks the dialog's actual accessible title.
    const dialog = screen.getByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "恢复批次" });
    expect(submit).toBeDisabled();
    for (const label of [
      "存储池可用字节",
      "下载峰值字节",
      "上传峰值字节",
      "备份峰值字节",
      "恢复峰值字节",
      "保留余量字节",
    ]) {
      const input = within(dialog).getByRole("textbox", { name: label });
      expect(input).toHaveValue("");
      await userEvent.type(input, label === "存储池可用字节" ? "1000000" : "0");
    }
    await userEvent.type(
      within(dialog).getByRole("textbox", { name: "容量观察时间（含时区）" }),
      new Date(Date.now() - 1000).toISOString(),
    );
    await userEvent.type(
      within(dialog).getByRole("textbox", {
        name: "证据有效截止时间（含时区）",
      }),
      new Date(Date.now() + 600_000).toISOString(),
    );
    await userEvent.click(submit);
    await within(dialog).findByText(/批次已被后台或其他管理员更新/);
    expect(resumeNexusMigrationBatch).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: { "If-Match": "7", "Idempotency-Key": expect.any(String) },
        body: {
          capacityEvidence: expect.objectContaining({
            targetId: nexusCapabilities.targetId,
            poolId: nexusCapabilities.poolId,
            freeBytes: 1000000,
            downloadBytes: 0,
          }),
        },
      }),
    );
    expect(retryNexusMigrationBatch).not.toHaveBeenCalled();
  });
  it("confirms cancel, preserves the captured version, and never sends on merely opening/closing a page", async () => {
    detail.mockResolvedValue({
      data: { ...nexusBatch, state: "paused" },
    } as never);
    vi.mocked(cancelNexusMigrationBatch).mockResolvedValue({
      data: { ...nexusBatch, state: "cancelled" },
    } as never);
    const rendered = show();
    await userEvent.click(
      await screen.findByRole("button", { name: "取消批次" }),
    );
    const dialog = screen.getByRole("dialog");
    expect(cancelNexusMigrationBatch).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "取消批次" }),
    );
    await waitFor(() =>
      expect(cancelNexusMigrationBatch).toHaveBeenCalledTimes(1),
    );
    expect(
      vi.mocked(cancelNexusMigrationBatch).mock.calls[0][0].headers?.[
        "If-Match"
      ],
    ).toBe("7");
    rendered.unmount();
    expect(resumeNexusMigrationBatch).not.toHaveBeenCalled();
    expect(pauseNexusMigrationBatch).not.toHaveBeenCalled();
  });
});
