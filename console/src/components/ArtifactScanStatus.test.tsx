import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createRepositoryArtifactScan,
  getRepositoryArtifactScanStatus,
} from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { ArtifactScanStatus } from "./ArtifactScanStatus";

vi.mock("../client", () => ({
  createRepositoryArtifactScan: vi.fn(),
  getRepositoryArtifactScanStatus: vi.fn(),
}));

const mockCreateScan = vi.mocked(createRepositoryArtifactScan);
const mockGetStatus = vi.mocked(getRepositoryArtifactScanStatus);
const digest = `sha256:${"a".repeat(64)}`;

const renderStatus = () =>
  render(
    <PreferencesProvider>
      <ArtifactScanStatus
        repositoryId="repo-1"
        format="raw"
        coordinate="releases/widget.bin"
        digest={digest}
      />
    </PreferencesProvider>,
  );

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
});

describe("ArtifactScanStatus", () => {
  it("shows an unscanned artifact and queues a manual scan", async () => {
    mockGetStatus.mockResolvedValue({
      data: {
        coordinate: "releases/widget.bin",
        digest,
        state: "never",
      },
    } as never);
    mockCreateScan.mockResolvedValue({
      data: {
        id: "scan-1",
        kind: "scan",
        state: "pending",
        createdAt: "2026-08-11T08:00:00Z",
        attempts: 0,
        maxAttempts: 3,
        progressCurrent: 0,
        progressTotal: 0,
      },
    } as never);

    renderStatus();

    expect(await screen.findByText("未扫描")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /重新扫描/ }));

    expect(mockCreateScan).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { repositoryId: "repo-1" },
        body: { coordinate: "releases/widget.bin", digest },
      }),
    );
    expect(await screen.findByText("待处理")).toBeInTheDocument();
  });

  it("keeps status failures visible and retries them", async () => {
    mockGetStatus
      .mockResolvedValueOnce({
        error: new Error("network unavailable"),
      } as never)
      .mockResolvedValueOnce({
        data: {
          coordinate: "releases/widget.bin",
          digest,
          state: "completed",
          job: {
            id: "scan-2",
            kind: "scan",
            state: "completed",
            createdAt: "2026-08-11T08:00:00Z",
            completedAt: "2026-08-11T08:01:00Z",
            attempts: 1,
            maxAttempts: 3,
            progressCurrent: 1,
            progressTotal: 1,
          },
        },
      } as never);

    renderStatus();

    expect(await screen.findByText("读取扫描状态失败")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /重\s*试/ }));

    expect(await screen.findByText("已完成")).toBeInTheDocument();
    expect(mockGetStatus).toHaveBeenCalledTimes(2);
  });
});
