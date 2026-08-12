import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getQuarantineReadPolicy,
  getSecurityPolicy,
  replaceQuarantineReadPolicy,
  replaceSecurityPolicy,
} from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositorySecurityTab } from "./RepositorySecurityTab";

vi.mock("../../client", () => ({
  getSecurityPolicy: vi.fn(),
  replaceSecurityPolicy: vi.fn(),
  getQuarantineReadPolicy: vi.fn(),
  replaceQuarantineReadPolicy: vi.fn(),
}));

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
    await user.click(screen.getByRole("switch", { name: "发布后自动扫描" }));
    await user.click(screen.getByRole("switch", { name: "启用准入检查" }));
    await user.click(screen.getByRole("button", { name: "保存策略" }));

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
  });

  it("disables automatic scans when the repository format has no scanner", async () => {
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
    expect(
      screen.getByText(/当前仓库类型或格式未启用发布后自动扫描/),
    ).toBeInTheDocument();
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
    await user.click(readSwitch);
    await user.click(screen.getByRole("button", { name: "保存读取策略" }));

    await waitFor(() =>
      expect(mockReplaceQuarantineReadPolicy).toHaveBeenCalledWith({
        path: { repositoryId: repo.id },
        headers: { "If-Match": "7" },
        body: { version: "7", enabled: true },
      }),
    );
    expect(await screen.findByText("读取策略当前版本 8")).toBeInTheDocument();
    expect(screen.getByText("隔离读取策略已保存")).toBeInTheDocument();
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
  });
});
