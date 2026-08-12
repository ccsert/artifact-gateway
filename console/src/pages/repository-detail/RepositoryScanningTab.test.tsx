import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createRepositoryArtifactScan,
  listRepositoryLifecycleJobs,
  reconcileRepositoryArtifactScans,
} from "../../client";
import type { Repository, RepositoryCapabilities } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryScanningTab } from "./RepositoryScanningTab";

vi.mock("../../client", () => ({
  createRepositoryArtifactScan: vi.fn(),
  listRepositoryLifecycleJobs: vi.fn(),
  reconcileRepositoryArtifactScans: vi.fn(),
}));

const mockCreateScan = vi.mocked(createRepositoryArtifactScan);
const mockListJobs = vi.mocked(listRepositoryLifecycleJobs);
const mockReconcileScans = vi.mocked(reconcileRepositoryArtifactScans);
const digest = `sha256:${"a".repeat(64)}`;
const repository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "npm-hosted",
  format: "npm",
  type: "hosted",
  anonymousRead: false,
  state: "active",
  version: "1",
};
const capabilities: RepositoryCapabilities = {
  format: "npm",
  type: "hosted",
  operations: ["read", "publish"],
  artifactScanning: true,
  publicationScanning: true,
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryScanningTab", () => {
  it("queues a manual scan from a discoverable repository page", async () => {
    const user = userEvent.setup();
    mockListJobs.mockResolvedValue({ data: [] } as never);
    mockCreateScan.mockResolvedValue({
      data: {
        id: "scan-job-1",
        kind: "scan",
        state: "pending",
        createdAt: "2026-08-12T06:00:00Z",
        attempts: 0,
        maxAttempts: 3,
        progressCurrent: 0,
        progressTotal: 0,
        details: {
          format: "npm",
          coordinate: "@team/widget@1.2.3",
          digest,
        },
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={repository}
          capabilities={capabilities}
          capabilitiesLoading={false}
          capabilitiesError={null}
          canManage
          canViewJobs
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("手动扫描不可变制品")).toBeInTheDocument();
    await user.type(screen.getByLabelText("制品坐标"), "@team/widget@1.2.3");
    await user.type(screen.getByLabelText("SHA-256 摘要"), digest);
    await user.click(screen.getByRole("button", { name: "提交扫描" }));

    await waitFor(() => expect(mockCreateScan).toHaveBeenCalledTimes(1));
    expect(mockCreateScan).toHaveBeenCalledWith({
      path: { repositoryId: repository.id },
      headers: { "Idempotency-Key": expect.stringMatching(/^manual-scan:/) },
      body: { coordinate: "@team/widget@1.2.3", digest },
    });
    expect(await screen.findByText("扫描任务已提交")).toBeInTheDocument();
    expect(screen.getAllByText("scan-job-1")).not.toHaveLength(0);
    expect(screen.getByText("@team/widget@1.2.3")).toBeInTheDocument();
  });

  it("explains scanner configuration when the repository cannot scan", async () => {
    mockListJobs.mockResolvedValue({ data: [] } as never);

    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={repository}
          capabilities={{
            ...capabilities,
            artifactScanning: false,
            publicationScanning: false,
          }}
          capabilitiesLoading={false}
          capabilitiesError={null}
          canManage
          canViewJobs
        />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByText("当前仓库未配置可用扫描器"),
    ).toBeInTheDocument();
    expect(screen.getByText("GATEWAY_SCANNER_ENDPOINT")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交扫描" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "对账历史制品" })).toBeDisabled();
    expect(mockReconcileScans).not.toHaveBeenCalled();
  });

  it("backfills visible publications from the same scanning workspace", async () => {
    const user = userEvent.setup();
    mockListJobs.mockResolvedValue({ data: [] } as never);
    mockReconcileScans.mockResolvedValue({
      data: {
        repositoryId: repository.id,
        inspected: 7,
        enqueued: 3,
        retried: 1,
        skipped: 3,
        jobIds: ["scan-job-2"],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={repository}
          capabilities={capabilities}
          capabilitiesLoading={false}
          capabilitiesError={null}
          canManage
          canViewJobs
        />
      </PreferencesProvider>,
    );

    await user.click(
      await screen.findByRole("button", { name: "对账历史制品" }),
    );

    await waitFor(() => expect(mockReconcileScans).toHaveBeenCalledTimes(1));
    expect(mockReconcileScans).toHaveBeenCalledWith({
      path: { repositoryId: repository.id },
      query: { limit: 500 },
    });
    expect(
      await screen.findByText("已检查 7 个制品，补入 3 个，重试 1 个"),
    ).toBeInTheDocument();
  });

  it("does not turn capability failures into scanner configuration advice", async () => {
    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={repository}
          capabilities={null}
          capabilitiesLoading={false}
          capabilitiesError={new Error("capability request failed")}
          canManage={false}
          canViewJobs={false}
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(
      screen.queryByText("当前仓库未配置可用扫描器"),
    ).not.toBeInTheDocument();
    expect(mockListJobs).not.toHaveBeenCalled();
  });

  it("keeps intelligence mutations disabled for a read-only identity", async () => {
    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={repository}
          capabilities={capabilities}
          capabilitiesLoading={false}
          capabilitiesError={null}
          canManage={false}
          canViewJobs={false}
        />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByText("当前身份没有制品扫描权限"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交扫描" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "对账历史制品" })).toBeDisabled();
    expect(screen.getByText("最近任务仅对仓库管理员可见")).toBeInTheDocument();
    expect(mockListJobs).not.toHaveBeenCalled();
  });

  it("asks OCI users for an image name while keeping the digest separate", async () => {
    render(
      <PreferencesProvider>
        <RepositoryScanningTab
          repo={{ ...repository, format: "oci", name: "oci-hosted" }}
          capabilities={{ ...capabilities, format: "oci" }}
          capabilitiesLoading={false}
          capabilitiesError={null}
          canManage
          canViewJobs={false}
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByLabelText("制品坐标")).toHaveAttribute(
      "placeholder",
      "team/widget",
    );
    expect(screen.getByLabelText("SHA-256 摘要")).toHaveAttribute(
      "placeholder",
      "sha256:…",
    );
  });
});
