import { App } from "antd";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listRuntimeLogs } from "../../client";
import type { RuntimeLogEntry, RuntimeLogPage } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RuntimeLogsPanel } from "./RuntimeLogsPanel";

vi.mock("../../client", () => ({ listRuntimeLogs: vi.fn() }));
const api = vi.mocked(listRuntimeLogs);
const row = (sequence: number, sessionId = "session"): RuntimeLogEntry => ({
  sequence,
  time: "2026-10-03T00:00:00Z",
  level: "INFO",
  instanceId: "node",
  sessionId,
  component: "http",
  operation: "http.request",
  message: `event-${sequence}`,
  requestId: "request",
  traceId: "trace",
  method: "GET",
  status: 200,
  durationMs: 12,
});
function page(
  first: number,
  last: number,
  extra: Partial<RuntimeLogPage> = {},
): RuntimeLogPage {
  return {
    scope: "local",
    instanceId: "node",
    sessionId: "session",
    items: Array.from({ length: Math.max(0, last - first + 1) }, (_, i) =>
      row(first + i),
    ),
    afterCursor: `cursor-${last}`,
    order: "ascending",
    hasMore: false,
    retention: { earliestSequence: 1, latestSequence: last, gap: false },
    source: {
      minimumLevel: "INFO",
      accessMode: "limited",
      slowThresholdMs: 1000,
      capacityLines: 1000,
      maxLineBytes: 16384,
      components: ["http", "proxy_worker"],
      componentsTruncated: false,
    },
    ...extra,
  };
}
const reply = (data: RuntimeLogPage) => ({
  data,
  request: new Request("http://127.0.0.1/api/v2/runtime/logs"),
  response: new Response(),
});
const mount = () =>
  render(
    <MemoryRouter initialEntries={["/system?tab=logs&requestId=request"]}>
      <PreferencesProvider>
        <App>
          <RuntimeLogsPanel />
        </App>
      </PreferencesProvider>
    </MemoryRouter>,
  );
const clipboardDescriptor = Object.getOwnPropertyDescriptor(
  navigator,
  "clipboard",
);

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  api.mockReset();
  localStorage.clear();
  if (clipboardDescriptor)
    Object.defineProperty(navigator, "clipboard", clipboardDescriptor);
  else Reflect.deleteProperty(navigator, "clipboard");
});

describe("incremental runtime log controls", () => {
  for (const newSession of [false, true])
    it(`invalidates selection on a replacement snapshot (session change: ${newSession})`, async () => {
      api
        .mockResolvedValueOnce(reply(page(1, 1)))
        .mockResolvedValueOnce(
          reply(
            page(
              2,
              2,
              newSession
                ? { sessionId: "new-session", items: [row(2, "new-session")] }
                : {},
            ),
          ),
        );
      mount();
      await screen.findByText("event-1");
      await act(async () => {});
      const range = document.createRange();
      range.selectNodeContents(screen.getByText("event-1"));
      const selection = window.getSelection()!;
      selection.removeAllRanges();
      selection.addRange(range);
      fireEvent(document, new Event("selectionchange"));
      expect(screen.getByRole("button", { name: /复制选择/ })).toBeEnabled();
      fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
      await screen.findByText("event-2");
      expect(screen.getByRole("button", { name: /复制选择/ })).toBeDisabled();
      expect(selection.toString()).toBe("");
    });

  it("does not revive selected protected rows after authorization recovery", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockRejectedValueOnce({
        status: 403,
        code: "password_change_required",
        message: "synthetic reset",
      })
      .mockResolvedValueOnce(reply(page(2, 2)));
    mount();
    await screen.findByText("event-1");
    const range = document.createRange();
    range.selectNodeContents(screen.getByText("event-1"));
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    fireEvent(document, new Event("selectionchange"));
    expect(screen.getByRole("button", { name: /复制选择/ })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
    await screen.findByText("synthetic reset");
    expect(screen.queryByRole("button", { name: /复制选择/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
    await screen.findByText("event-2");
    expect(screen.getByRole("button", { name: /复制选择/ })).toBeDisabled();
  });

  it("rechecks an empty native selection at click time even without a selection event", async () => {
    api.mockResolvedValue(reply(page(1, 1)));
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    mount();
    await screen.findByText("event-1");
    const range = document.createRange();
    range.selectNodeContents(screen.getByText("event-1"));
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    fireEvent(document, new Event("selectionchange"));
    expect(screen.getByRole("button", { name: /复制选择/ })).toBeEnabled();
    selection.removeAllRanges();
    fireEvent.click(screen.getByRole("button", { name: /复制选择/ }));
    expect(writeText).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /复制选择/ })).toBeDisabled();
  });

  it("copies a full multi-row selection and rejects an oversized UTF-8 selection visibly", async () => {
    const items = Array.from({ length: 8 }, (_, i) => ({
      ...row(i + 1),
      message: String(i + 1).repeat(3000),
    }));
    api.mockResolvedValue(reply(page(1, 8, { items })));
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    mount();
    const viewport = await screen.findByLabelText("运行日志流");
    const range = document.createRange();
    range.selectNodeContents(viewport);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    const actualSelected = selection.toString();
    expect(actualSelected.length).toBeGreaterThan(24_000);
    fireEvent(document, new Event("selectionchange"));
    fireEvent.click(screen.getByRole("button", { name: /复制选择/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(actualSelected));
    vi.spyOn(selection, "toString").mockReturnValue("中".repeat(350_000));
    fireEvent(document, new Event("selectionchange"));
    expect(screen.getByRole("button", { name: /复制选择/ })).toBeDisabled();
    expect(
      screen.getByText("选择内容超过 1 MiB，请缩小选择后复制"),
    ).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledTimes(1);
  });

  it("counts duplicate sequences inside one response only once as unread", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockResolvedValueOnce(reply(page(2, 2, { items: [row(2), row(2)] })));
    mount();
    await screen.findByText("event-1");
    const viewport = screen.getByLabelText("运行日志流");
    Object.defineProperties(viewport, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 100 },
      scrollTop: { configurable: true, writable: true, value: 20 },
    });
    fireEvent.scroll(viewport);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    expect(screen.getAllByText("event-2")).toHaveLength(1);
    expect(screen.getByRole("button", { name: /条未读/ })).toHaveTextContent(
      "1 条未读",
    );
  });
  it("drains 100+ pages from consumed cursors, deduplicates, and preserves the live anchor through older history", async () => {
    api
      .mockResolvedValueOnce(reply(page(230, 230, { nextSequence: 230 })))
      .mockResolvedValueOnce(
        reply(page(1, 2, { afterCursor: "wrong-history-anchor" })),
      )
      .mockResolvedValueOnce(reply(page(231, 330, { hasMore: true })))
      .mockResolvedValueOnce(reply(page(330, 429, { hasMore: true })))
      .mockResolvedValueOnce(reply(page(430, 460)));
    mount();
    await screen.findByText("event-230");
    fireEvent.click(screen.getByRole("button", { name: /加载更早日志/ }));
    await screen.findByText("event-1");
    vi.useFakeTimers();
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    expect(api).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({
          afterCursor: "cursor-230",
          beforeSequence: undefined,
          requestId: "request",
          limit: 100,
        }),
      }),
    );
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(api).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ afterCursor: "cursor-330" }),
      }),
    );
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(api).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ afterCursor: "cursor-429" }),
      }),
    );
    const numbers = [
      ...document.querySelectorAll<HTMLElement>("[data-sequence]"),
    ].map((node) => Number(node.dataset.sequence));
    expect(numbers).toEqual([
      1,
      2,
      ...Array.from({ length: 231 }, (_, i) => i + 230),
    ]);
    expect(screen.queryByText(/服务器保留已覆盖/)).toBeNull();
  });

  it("pauses network reads, suspends hidden-page polling and resumes from the same cursor", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockResolvedValueOnce(reply(page(2, 2)))
      .mockResolvedValue(reply(page(3, 3)));
    mount();
    await screen.findByText("event-1");
    vi.useFakeTimers();
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    expect(screen.getByText("event-2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /暂停跟随/ }));
    await act(async () => vi.advanceTimersByTimeAsync(10_000));
    expect(api).toHaveBeenCalledTimes(2);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    expect(api).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ afterCursor: "cursor-2" }),
      }),
    );
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    fireEvent(document, new Event("visibilitychange"));
    const calls = api.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(10_000));
    expect(api).toHaveBeenCalledTimes(calls);
    hidden.mockReturnValue(false);
    fireEvent(document, new Event("visibilitychange"));
    await act(async () => vi.advanceTimersByTimeAsync(3000));
    expect(api).toHaveBeenCalledTimes(calls + 1);
    expect(api).toHaveBeenLastCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ afterCursor: "cursor-3" }),
      }),
    );
  });

  it("does not force the bottom while reading above it and reports only fetched unread events", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockResolvedValueOnce(reply(page(2, 3)));
    mount();
    await screen.findByText("event-1");
    const viewport = screen.getByLabelText("运行日志流");
    Object.defineProperties(viewport, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 100 },
      scrollTop: { configurable: true, writable: true, value: 20 },
    });
    fireEvent.scroll(viewport);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    expect(viewport.scrollTop).toBe(20);
    expect(
      screen.getByRole("button", { name: /2 条未读/ }),
    ).toBeInTheDocument();
  });

  it("keeps refresh data after failure, but discards protected data on permission revocation", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockRejectedValueOnce({
        status: 503,
        code: "log_buffer_unavailable",
        message: "synthetic unavailable",
      })
      .mockRejectedValueOnce({
        status: 403,
        code: "password_change_required",
        message: "synthetic reset",
      });
    mount();
    await screen.findByText("event-1");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /刷新快照/ }),
      ).not.toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
    await screen.findByText("synthetic unavailable");
    expect(screen.getByText("event-1")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /刷新快照/ }),
      ).not.toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
    await screen.findByText("synthetic reset");
    expect(screen.queryByText("event-1")).toBeNull();
    expect(screen.queryByRole("status", { name: /查询运行日志/ })).toBeNull();
  });

  it("clears the stream when an incremental response changes session", async () => {
    api.mockResolvedValueOnce(reply(page(1, 1))).mockResolvedValueOnce(
      reply(
        page(2, 2, {
          sessionId: "new-session",
          items: [row(2, "new-session")],
        }),
      ),
    );
    mount();
    await screen.findByText("event-1");
    fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ }));
    await screen.findByText("进程或会话已变化，请刷新新快照");
    expect(screen.queryByText("event-1")).toBeNull();
    expect(screen.queryByText("event-2")).toBeNull();
  });

  it("resets filters and cursors, rejecting a stale in-flight follow result", async () => {
    let complete: ((value: ReturnType<typeof reply>) => void) | undefined;
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockImplementationOnce(
        () =>
          new Promise<ReturnType<typeof reply>>((resolve) => {
            complete = resolve;
          }),
      )
      .mockResolvedValueOnce(reply(page(7, 7)));
    mount();
    await screen.findByText("event-1");
    fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ }));
    fireEvent.click(screen.getByRole("button", { name: /清除筛选/ }));
    await screen.findByText("event-7");
    await act(async () => complete?.(reply(page(99, 99))));
    expect(screen.queryByText("event-99")).toBeNull();
    expect(screen.queryByText("event-1")).toBeNull();
    expect(api.mock.calls.at(-1)?.[0]?.query?.afterCursor).toBeUndefined();
    expect(api.mock.calls.at(-1)?.[0]?.query?.requestId).toBeUndefined();
  });

  it("bounds a stalled request, retaining the previous snapshot with an error", async () => {
    api.mockResolvedValueOnce(reply(page(1, 1))).mockImplementationOnce(
      (options) =>
        new Promise<ReturnType<typeof reply>>((_, reject) => {
          options?.signal?.addEventListener("abort", () =>
            reject(new DOMException("Canceled", "AbortError")),
          );
        }),
    );
    mount();
    await screen.findByText("event-1");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ }));
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(
      screen.getByText("Runtime log request timed out"),
    ).toBeInTheDocument();
    expect(screen.getByText("event-1")).toBeInTheDocument();
    expect(api.mock.calls.at(-1)?.[0]?.signal?.aborted).toBe(true);
  });

  it("preserves a paused in-flight cursor and discards the canceled result", async () => {
    let complete: ((value: ReturnType<typeof reply>) => void) | undefined;
    api
      .mockResolvedValueOnce(reply(page(1, 1)))
      .mockImplementationOnce(
        () =>
          new Promise<ReturnType<typeof reply>>((resolve) => {
            complete = resolve;
          }),
      )
      .mockResolvedValueOnce(reply(page(2, 3)));
    mount();
    await screen.findByText("event-1");
    fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ }));
    fireEvent.click(screen.getByRole("button", { name: /暂停跟随/ }));
    await act(async () => complete?.(reply(page(2, 2))));
    expect(screen.queryByText("event-2")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ }));
    await screen.findByText("event-3");
    expect(screen.getByText("event-2")).toBeInTheDocument();
    expect(api.mock.calls.at(-1)?.[0]?.query?.afterCursor).toBe("cursor-1");
  });

  it("announces a new session on refresh without mixing records", async () => {
    api.mockResolvedValueOnce(reply(page(1, 1))).mockResolvedValueOnce(
      reply(
        page(2, 2, {
          sessionId: "new-session",
          items: [row(2, "new-session")],
        }),
      ),
    );
    mount();
    await screen.findByText("event-1");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /刷新快照/ }),
      ).not.toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: /刷新快照/ }));
    await screen.findByText("event-2");
    expect(screen.queryByText("event-1")).toBeNull();
    expect(
      screen.getByText("已切换新进程或会话，旧记录已清除"),
    ).toBeInTheDocument();
  });

  it("separates retention loss from local trimming and bounds a high-volume stream", async () => {
    api
      .mockResolvedValueOnce(reply(page(1, 50)))
      .mockResolvedValueOnce(reply(page(51, 150, { hasMore: true })))
      .mockResolvedValueOnce(reply(page(151, 250, { hasMore: true })))
      .mockResolvedValueOnce(reply(page(251, 350, { hasMore: true })))
      .mockResolvedValueOnce(
        reply(
          page(351, 430, {
            retention: {
              earliestSequence: 350,
              latestSequence: 430,
              gap: true,
            },
          }),
        ),
      );
    mount();
    await screen.findByText("event-50");
    vi.useFakeTimers();
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: /跟随新日志/ })),
    );
    for (let i = 0; i < 3; i++)
      await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(document.querySelectorAll("[data-sequence]")).toHaveLength(300);
    expect(screen.getByText(/客户端容量已移除 130 条/)).toBeInTheDocument();
    expect(
      screen.getByText("服务器保留已覆盖部分未读日志"),
    ).toBeInTheDocument();
  });

  for (const locale of ["zh-CN", "en-US"])
    it(`reports actual source policy and missing-event uncertainty in ${locale}`, async () => {
      localStorage.setItem("ag.console.locale", locale);
      api.mockResolvedValueOnce(reply(page(1, 0)));
      mount();
      await waitFor(() =>
        expect(
          screen.getByText(
            locale === "zh-CN" ? "当前范围内没有日志" : "No logs in this range",
          ),
        ).toBeInTheDocument(),
      );
      expect(
        screen.getByText(/INFO.*limited.*1000ms.*1000/),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          locale === "zh-CN"
            ? /DEBUG 筛选不会启用 DEBUG/
            : /A DEBUG filter does not enable DEBUG/,
        ),
      ).toBeInTheDocument();
    });
});
