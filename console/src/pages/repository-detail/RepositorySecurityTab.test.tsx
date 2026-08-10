import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getSecurityPolicy, replaceSecurityPolicy } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositorySecurityTab } from "./RepositorySecurityTab";

vi.mock("../../client", () => ({
  getSecurityPolicy: vi.fn(),
  replaceSecurityPolicy: vi.fn(),
}));

const mockGetSecurityPolicy = vi.mocked(getSecurityPolicy);
const mockReplaceSecurityPolicy = vi.mocked(replaceSecurityPolicy);
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

describe("RepositorySecurityTab", () => {
  it("loads and saves a versioned security admission policy", async () => {
    const user = userEvent.setup();
    mockGetSecurityPolicy.mockResolvedValue({
      data: {
        version: "3",
        enabled: false,
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
        <RepositorySecurityTab repo={repo} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("当前版本 3")).toBeInTheDocument();
    const switches = screen.getAllByRole("switch");
    await user.click(switches[0]);
    await user.click(screen.getByRole("button", { name: "保存策略" }));

    await waitFor(() => expect(mockReplaceSecurityPolicy).toHaveBeenCalled());
    expect(mockReplaceSecurityPolicy).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { repositoryId: repo.id },
        headers: { "If-Match": "3" },
        body: expect.objectContaining({
          version: "3",
          enabled: true,
          requireSbom: true,
          maxAllowedSeverity: "high",
          allowedLicenses: ["MIT"],
        }),
      }),
    );
    expect(await screen.findByText("当前版本 4")).toBeInTheDocument();
    expect(screen.getByText("安全准入策略已保存")).toBeInTheDocument();
  });
});
