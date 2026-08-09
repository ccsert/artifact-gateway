import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getDiagnostics } from "../client";
import type { Diagnostics } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { SystemDiagnosticsPanel } from "./SystemDiagnosticsPanel";

vi.mock("../client", () => ({ getDiagnostics: vi.fn() }));
const mockGetDiagnostics = vi.mocked(getDiagnostics);

const diagnostics: Diagnostics = {
  generatedAt: "2026-08-10T08:00:00Z",
  build: {
    version: "v1.2.3",
    revision: "abc123",
    goVersion: "go1.test",
    modified: false,
  },
  runtime: {
    instanceId: "gateway-01",
    roles: ["standalone"],
    workerFormats: ["maven"],
    workerKinds: ["retention"],
  },
  dependencies: [
    { name: "postgresql", status: "reachable", detail: "reachable" },
    {
      name: "object-storage",
      status: "unreachable",
      detail: "health check failed",
    },
  ],
  queues: [
    {
      kind: "promotion",
      format: "maven",
      state: "failed",
      count: 2,
      oldestCreatedAt: "2026-08-10T07:00:00Z",
    },
  ],
  nodes: { status: "degraded", online: 1, stale: 1, offline: 0, issues: [] },
};

const renderPanel = () =>
  render(
    <PreferencesProvider>
      <SystemDiagnosticsPanel />
    </PreferencesProvider>,
  );

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("SystemDiagnosticsPanel", () => {
  it("renders dependency, build, node, and queue diagnostics", async () => {
    mockGetDiagnostics.mockResolvedValue({ data: diagnostics } as never);
    renderPanel();

    expect(await screen.findByText("系统运行状态需要关注")).toBeInTheDocument();
    expect(screen.getByText("v1.2.3")).toBeInTheDocument();
    expect(screen.getByText("gateway-01")).toBeInTheDocument();
    expect(screen.getByText("Worker 格式")).toBeInTheDocument();
    expect(screen.getByText("retention")).toBeInTheDocument();
    expect(screen.getByText("干净")).toBeInTheDocument();
    expect(screen.getByText("PostgreSQL")).toBeInTheDocument();
    expect(screen.getByText("对象存储")).toBeInTheDocument();
    expect(screen.getByText("不可用")).toBeInTheDocument();
    expect(screen.getByText("promotion")).toBeInTheDocument();
    expect(screen.getAllByText("maven")).toHaveLength(2);
  });

  it("copies only the sanitized server response", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    mockGetDiagnostics.mockResolvedValue({ data: diagnostics } as never);
    renderPanel();

    await user.click(
      await screen.findByRole("button", { name: "复制脱敏诊断 JSON" }),
    );
    expect(writeText).toHaveBeenCalledOnce();
    const copied = writeText.mock.calls[0]?.[0] as string;
    expect(copied).toContain('"instanceId": "gateway-01"');
    expect(copied).not.toMatch(/password|token|databaseUrl/i);
    expect(screen.getByText("已复制")).toBeInTheDocument();
  });

  it("treats unconfigured dependencies as unavailable", async () => {
    mockGetDiagnostics.mockResolvedValue({
      data: {
        ...diagnostics,
        dependencies: [
          { name: "runtime", status: "not_configured", detail: "未配置" },
        ],
      },
    } as never);
    renderPanel();

    expect(await screen.findByText("系统运行状态需要关注")).toBeInTheDocument();
    const summary = within(screen.getByRole("group", { name: "页面摘要" }));
    const dependencyMetric = summary.getByText("依赖可用").parentElement;
    expect(dependencyMetric).not.toBeNull();
    expect(
      within(dependencyMetric as HTMLElement).getByText("0"),
    ).toBeInTheDocument();
    expect(
      within(dependencyMetric as HTMLElement).getByText("1 项检查"),
    ).toBeInTheDocument();
  });
});
