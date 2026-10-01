import { App } from "antd";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listRuntimeLogs } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RuntimeLogsPanel } from "./RuntimeLogsPanel";

vi.mock("../../client", () => ({ listRuntimeLogs: vi.fn() }));
const mockListRuntimeLogs = vi.mocked(listRuntimeLogs);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RuntimeLogsPanel", () => {
  it("uses the audit request ID and appends older process-local results", async () => {
    mockListRuntimeLogs
      .mockResolvedValueOnce({
        data: {
          scope: "local",
          instanceId: "gateway-01",
          sessionId: "session-01",
          items: [
            {
              sequence: 2,
              time: "2026-09-29T10:00:00Z",
              level: "ERROR",
              message: "worker failed",
              instanceId: "gateway-01",
              sessionId: "session-01",
              component: "worker",
              operation: "job.run",
              requestId: "job-1",
              traceId: "trace-1",
            },
          ],
          nextSequence: 2,
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          scope: "local",
          instanceId: "gateway-01",
          sessionId: "session-01",
          items: [],
        },
      } as never);
    render(
      <MemoryRouter initialEntries={["/system?tab=logs&requestId=job-1"]}>
        <PreferencesProvider>
          <App>
            <RuntimeLogsPanel />
          </App>
        </PreferencesProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText("worker failed")).toBeInTheDocument();
    expect(screen.getByText(/仅查询当前进程的内存日志/)).toBeInTheDocument();
    expect(mockListRuntimeLogs).toHaveBeenCalledWith(
      expect.objectContaining({
        query: expect.objectContaining({ requestId: "job-1" }),
      }),
    );
    await userEvent.click(screen.getByRole("button", { name: /加载更早日志/ }));
    await waitFor(() =>
      expect(mockListRuntimeLogs).toHaveBeenLastCalledWith(
        expect.objectContaining({
          query: expect.objectContaining({
            beforeSequence: 2,
            requestId: "job-1",
          }),
        }),
      ),
    );
    expect(screen.getByText("worker failed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /加载更早日志/ })).toBeNull();
  });

  it("does not show pagination for an empty result", async () => {
    mockListRuntimeLogs.mockResolvedValueOnce({
      data: {
        scope: "local",
        instanceId: "gateway-01",
        sessionId: "session-01",
        items: [],
      },
    } as never);
    render(
      <MemoryRouter initialEntries={["/system?tab=logs"]}>
        <PreferencesProvider>
          <App>
            <RuntimeLogsPanel />
          </App>
        </PreferencesProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText("当前范围内没有日志")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /加载更早日志/ })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
