import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getAptRepositorySigningState,
  getQuarantineReadPolicy,
  getSecurityPolicy,
  replaceQuarantineReadPolicy,
  replaceSecurityPolicy,
} from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositorySecurityTab } from "./RepositorySecurityTab";

vi.mock("../../client", () => ({
  getAptRepositorySigningState: vi.fn(),
  getSecurityPolicy: vi.fn(),
  replaceSecurityPolicy: vi.fn(),
  getQuarantineReadPolicy: vi.fn(),
  replaceQuarantineReadPolicy: vi.fn(),
}));

const mockGetAptRepositorySigningState = vi.mocked(
  getAptRepositorySigningState,
);
const mockGetSecurityPolicy = vi.mocked(getSecurityPolicy);
const mockReplaceSecurityPolicy = vi.mocked(replaceSecurityPolicy);
const mockGetQuarantineReadPolicy = vi.mocked(getQuarantineReadPolicy);
const mockReplaceQuarantineReadPolicy = vi.mocked(replaceQuarantineReadPolicy);
const repo: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "releases",
  format: "maven",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

beforeEach(() => {
  mockGetQuarantineReadPolicy.mockResolvedValue({
    data: { version: "1", enabled: false },
  } as never);
});

describe("RepositorySecurityTab", () => {
  it("shows APT trust rotation and immutable snapshot evidence without loading unsupported policies", async () => {
    mockGetAptRepositorySigningState.mockResolvedValue({
      data: {
        repositoryId: repo.id,
        signerMode: "remote",
        readiness: "rotation_overlap",
        trustedFingerprints: ["a".repeat(40), "b".repeat(40)],
        currentKeyRole: "active",
        currentSnapshot: {
          id: "22222222-2222-4222-8222-222222222222",
          repositoryId: repo.id,
          suite: "stable",
          sequence: 7,
          state: "visible",
          releaseDigest: `sha256:${"c".repeat(64)}`,
          inReleaseDigest: `sha256:${"d".repeat(64)}`,
          signerIdentity: "release@example.test",
          keyFingerprint: "a".repeat(40),
          signatureAlgorithm: "rsa4096-sha256",
          createdAt: "2026-08-13T08:00:00Z",
          publishedAt: "2026-08-13T08:01:00Z",
        },
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab
          repo={{ ...repo, format: "apt" }}
          publicationScanning={false}
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("签名信任与当前快照")).toBeInTheDocument();
    expect(screen.getByText("密钥轮换重叠")).toBeInTheDocument();
    expect(screen.getByText("stable / 7")).toBeInTheDocument();
    expect(screen.getByText("release@example.test")).toBeInTheDocument();
    expect(screen.getByText("当前使用")).toBeInTheDocument();
    expect(mockGetSecurityPolicy).not.toHaveBeenCalled();
    expect(mockGetQuarantineReadPolicy).not.toHaveBeenCalled();
  });

  it("does not show stale APT signing evidence after switching repositories", async () => {
    let resolveFirst: ((value: unknown) => void) | undefined;
    const first = new Promise((resolve) => {
      resolveFirst = resolve;
    });
    mockGetAptRepositorySigningState
      .mockReturnValueOnce(first as never)
      .mockResolvedValueOnce({
        data: {
          repositoryId: "33333333-3333-4333-8333-333333333333",
          signerMode: "remote",
          readiness: "ready",
          trustedFingerprints: ["b".repeat(40)],
          currentKeyRole: "active",
        },
      } as never);

    const view = render(
      <PreferencesProvider>
        <RepositorySecurityTab
          repo={{ ...repo, format: "apt" }}
          publicationScanning={false}
        />
      </PreferencesProvider>,
    );
    view.rerender(
      <PreferencesProvider>
        <RepositorySecurityTab
          repo={{
            ...repo,
            id: "33333333-3333-4333-8333-333333333333",
            name: "apt-next",
            format: "apt",
          }}
          publicationScanning={false}
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("生产信任已固定")).toBeInTheDocument();
    resolveFirst?.({
      data: {
        repositoryId: repo.id,
        signerMode: "reference",
        readiness: "fixture",
        trustedFingerprints: [],
      },
    });
    await Promise.resolve();
    expect(screen.queryByText("仅参考签名器")).not.toBeInTheDocument();
    expect(screen.getByText("生产信任已固定")).toBeInTheDocument();
  });

  it("explains an unavailable APT signing endpoint during a rolling upgrade", async () => {
    mockGetAptRepositorySigningState.mockResolvedValue({
      error: {
        status: 404,
        code: "not_found",
        message: "repository not found",
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab
          repo={{ ...repo, format: "apt" }}
          publicationScanning={false}
        />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByText("APT 签名状态功能未启用"),
    ).toBeInTheDocument();
    expect(screen.getByText(/当前后端构建尚未挂载/)).toBeInTheDocument();
    expect(screen.queryByText("请求出错")).not.toBeInTheDocument();
  });

  it("loads and saves a versioned security admission policy", async () => {
    const user = userEvent.setup();
    mockGetSecurityPolicy.mockResolvedValue({
      data: {
        version: "3",
        enabled: false,
        autoScanOnPublish: false,
        requireSignature: false,
        requireVerifiedSignature: false,
        requireSbom: true,
        requireProvenance: false,
        requireVulnerabilityScan: true,
        maxAllowedSeverity: "high",
        failOnScanError: true,
        allowedLicenses: ["MIT"],
      },
    } as never);
    mockReplaceSecurityPolicy.mockResolvedValue({
      data: {
        version: "4",
        enabled: true,
        autoScanOnPublish: true,
        requireSignature: false,
        requireVerifiedSignature: false,
        requireSbom: true,
        requireProvenance: false,
        requireVulnerabilityScan: true,
        maxAllowedSeverity: "high",
        failOnScanError: true,
        allowedLicenses: ["MIT"],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab repo={repo} publicationScanning />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("当前版本 3")).toBeInTheDocument();
    const saveButton = screen.getByRole("button", { name: "保存策略" });
    expect(saveButton).toBeDisabled();
    await user.click(screen.getByRole("switch", { name: "发布后自动扫描" }));
    await user.click(screen.getByRole("switch", { name: "启用准入检查" }));
    expect(screen.getByText("有未保存更改")).toBeInTheDocument();
    expect(saveButton).toBeEnabled();
    await user.click(saveButton);

    await waitFor(() => expect(mockReplaceSecurityPolicy).toHaveBeenCalled());
    expect(mockReplaceSecurityPolicy).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { repositoryId: repo.id },
        headers: { "If-Match": "3" },
        body: expect.objectContaining({
          version: "3",
          enabled: true,
          autoScanOnPublish: true,
          requireSbom: true,
          maxAllowedSeverity: "high",
          allowedLicenses: ["MIT"],
        }),
      }),
    );
    expect(await screen.findByText("当前版本 4")).toBeInTheDocument();
    expect(screen.getByText("仓库安全策略已保存")).toBeInTheDocument();
    expect(saveButton).toBeDisabled();
  });

  it("shows automatic scanning as unavailable when no scanner is configured", async () => {
    mockGetSecurityPolicy.mockResolvedValue({
      data: { version: "1", autoScanOnPublish: false },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab repo={repo} publicationScanning={false} />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByRole("switch", { name: "发布后自动扫描" }),
    ).toBeDisabled();
    expect(screen.getByText("未配置可用扫描器")).toBeInTheDocument();
  });

  it("loads and saves the independent quarantine read policy", async () => {
    const user = userEvent.setup();
    mockGetSecurityPolicy.mockResolvedValue({
      data: { version: "1", enabled: false },
    } as never);
    mockGetQuarantineReadPolicy.mockResolvedValue({
      data: { version: "7", enabled: false },
    } as never);
    mockReplaceQuarantineReadPolicy.mockResolvedValue({
      data: { version: "8", enabled: true },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab repo={repo} publicationScanning />
      </PreferencesProvider>,
    );

    const readSwitch = await screen.findByRole("switch", {
      name: "阻断隔离制品读取",
    });
    const readSaveButton = screen.getByRole("button", {
      name: "保存读取策略",
    });
    expect(readSaveButton).toBeDisabled();
    await user.click(readSwitch);
    expect(screen.getByText("有未保存更改")).toBeInTheDocument();
    expect(readSaveButton).toBeEnabled();
    await user.click(readSaveButton);

    await waitFor(() =>
      expect(mockReplaceQuarantineReadPolicy).toHaveBeenCalledWith({
        path: { repositoryId: repo.id },
        headers: { "If-Match": "7" },
        body: { version: "7", enabled: true },
      }),
    );
    expect(await screen.findByText("读取策略当前版本 8")).toBeInTheDocument();
    expect(screen.getByText("隔离读取策略已保存")).toBeInTheDocument();
    expect(readSaveButton).toBeDisabled();
  });

  it("keeps the security policy usable when the read policy request fails", async () => {
    mockGetSecurityPolicy.mockResolvedValue({
      data: { version: "3", enabled: false },
    } as never);
    mockGetQuarantineReadPolicy.mockResolvedValue({
      error: { code: "internal_error", message: "读取策略失败", status: 500 },
    } as never);

    render(
      <PreferencesProvider>
        <RepositorySecurityTab repo={repo} publicationScanning />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("读取策略失败")).toBeInTheDocument();
    expect(screen.getByText("当前版本 3")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "保存策略" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "保存读取策略" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("状态不可用")).toBeInTheDocument();
    expect(screen.queryByText("有未保存更改")).not.toBeInTheDocument();
  });
});
