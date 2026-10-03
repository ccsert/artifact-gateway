import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { DiagnosticLocalCapacity } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { LocalCapacityPanel } from "./LocalCapacityPanel";

afterEach(cleanup);

const snapshot: DiagnosticLocalCapacity = {
  checkedAt: "2026-10-03T09:00:00Z",
  source: "statfs",
  scope: "observer_mount_namespace",
  unit: "bytes",
  refreshIntervalSeconds: 15,
  maxSampleAgeSeconds: 60,
  timeoutMilliseconds: 1000,
  mounts: [
    {
      alias: "temporary",
      status: "available",
      sharedWith: ["logs"],
      totalBytes: 40960,
      availableBytes: 0,
      sampleAt: "2026-10-03T08:59:59Z",
    },
    {
      alias: "logs",
      status: "unknown",
      reason: "remote_filesystem",
      sharedWith: [],
    },
    {
      alias: "backups",
      status: "stale",
      reason: "timeout",
      sharedWith: [],
      sampleAt: "2026-10-03T08:58:00Z",
      totalBytes: 999999,
      availableBytes: 999999,
    },
  ],
};

describe("LocalCapacityPanel", () => {
  it("shows mount perspective, valid zero, fixed reason and old sample without stale bytes", () => {
    render(
      <PreferencesProvider>
        <LocalCapacityPanel capacity={snapshot} />
      </PreferencesProvider>,
    );
    expect(
      screen.getByRole("heading", { name: "本地挂载容量" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/当前进程的挂载视角/)).toBeInTheDocument();
    expect(screen.getByText(/不代表 S3 或 NAS 后端物理池/)).toBeInTheDocument();
    expect(screen.getByText("0 B")).toBeInTheDocument();
    expect(screen.getByText("远端文件系统，容量未知")).toBeInTheDocument();
    expect(screen.getByText("刷新超时，容量未知")).toBeInTheDocument();
    expect(screen.getByText(/与日志共享可见文件系统/)).toBeInTheDocument();
    expect(screen.queryByText(/999999|976/)).not.toBeInTheDocument();
  });

  it("reports an older node without localCapacity as unknown rather than zero", () => {
    render(
      <PreferencesProvider>
        <LocalCapacityPanel />
      </PreferencesProvider>,
    );
    expect(
      screen.getByText("此节点未提供本地容量观察，容量未知"),
    ).toBeInTheDocument();
    expect(screen.queryByText("0 B")).not.toBeInTheDocument();
  });
});
