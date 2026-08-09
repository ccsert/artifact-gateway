import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listRuntimeNodes } from "../client";
import type { RuntimeNode } from "../client";
import { RuntimeNodesPanel } from "./RuntimeNodesPanel";
import { PreferencesProvider } from "../lib/preferences";

const renderPanel = () =>
  render(
    <PreferencesProvider>
      <RuntimeNodesPanel />
    </PreferencesProvider>,
  );

vi.mock("../client", () => ({
  listRuntimeNodes: vi.fn(),
}));

const mockListRuntimeNodes = vi.mocked(listRuntimeNodes);

const healthy = {
  status: "healthy" as const,
  online: 1,
  stale: 0,
  offline: 0,
  issues: [],
};

function runtimeNode(status: RuntimeNode["status"]): RuntimeNode {
  return {
    instanceId: "worker-01",
    sessionId: "session-worker-01",
    roles: ["worker"],
    workerFormats: ["oci"],
    workerKinds: ["reclaim"],
    startedAt: "2026-08-08T08:00:00Z",
    lastSeenAt: "2026-08-08T08:01:00Z",
    status,
  };
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("RuntimeNodesPanel", () => {
  it("loads and renders runtime capabilities independently", async () => {
    mockListRuntimeNodes.mockResolvedValue({
      data: { items: [runtimeNode("online")], health: healthy },
    } as never);

    renderPanel();

    expect(await screen.findByText("worker-01")).toBeInTheDocument();
    expect(screen.getByText("在线")).toBeInTheDocument();
    expect(screen.getByText("oci")).toBeInTheDocument();
    expect(screen.getByText("reclaim")).toBeInTheDocument();
    expect(screen.getByText("1 个实例")).toBeInTheDocument();
    expect(screen.getByText("健康")).toBeInTheDocument();
  });

  it("keeps polling while no lifecycle task is active", async () => {
    vi.useFakeTimers();
    mockListRuntimeNodes
      .mockResolvedValueOnce({
        data: { items: [runtimeNode("online")], health: healthy },
      } as never)
      .mockResolvedValueOnce({
        data: {
          items: [runtimeNode("stale")],
          health: {
            ...healthy,
            status: "degraded",
            stale: 1,
            issues: [
              {
                code: "stale_nodes",
                severity: "warning",
                message: "存在心跳过期的运行节点",
              },
            ],
          },
        },
      } as never);

    renderPanel();
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("在线")).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(mockListRuntimeNodes).toHaveBeenCalledTimes(2);
    expect(screen.getByText("陈旧")).toBeInTheDocument();
    expect(screen.getByText("集群运行能力需要关注")).toBeInTheDocument();
  });

  it("retries a node-specific error without involving task loading", async () => {
    const user = userEvent.setup();
    mockListRuntimeNodes
      .mockResolvedValueOnce({
        error: new Error("node request failed"),
      } as never)
      .mockResolvedValueOnce({ data: { items: [], health: healthy } } as never);

    renderPanel();
    expect(await screen.findByText("node request failed")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("暂未收到节点心跳")).toBeInTheDocument();
    expect(mockListRuntimeNodes).toHaveBeenCalledTimes(2);
  });

  it("renders legacy rows whose optional arrays are null", async () => {
    mockListRuntimeNodes.mockResolvedValue({
      data: {
        items: [
          {
            ...runtimeNode("offline"),
            sessionId: null,
            roles: null,
            workerFormats: null,
            workerKinds: null,
          },
        ],
        health: { ...healthy, issues: null },
      },
    } as never);

    renderPanel();

    expect(await screen.findByText("worker-01")).toBeInTheDocument();
    expect(screen.getByText("无格式 Worker")).toBeInTheDocument();
  });
});
