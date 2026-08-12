import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import {
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
} from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { RepositoryDetailPage } from "./RepositoryDetail";

const scanningTab = vi.hoisted(() => ({
  render: vi.fn((props: unknown) => {
    void props;
    return "扫描工作区已加载";
  }),
}));

vi.mock("../client", async () => {
  const actual = await vi.importActual<typeof import("../client")>("../client");
  return {
    ...actual,
    getRepository: vi.fn(),
    getRepositoryCapabilities: vi.fn(),
    getRepositoryCapacity: vi.fn(),
    getRepositoryEffectiveAccess: vi.fn(),
  };
});

vi.mock("./repository-detail/RepositoryScanningTab", () => ({
  RepositoryScanningTab: scanningTab.render,
}));

const mockGetRepository = vi.mocked(getRepository);
const mockGetCapabilities = vi.mocked(getRepositoryCapabilities);
const mockGetCapacity = vi.mocked(getRepositoryCapacity);
const mockGetEffectiveAccess = vi.mocked(getRepositoryEffectiveAccess);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryDetailPage scanning deep link", () => {
  it("discovers and renders the scanning workspace from ?tab=scanning", async () => {
    const repositoryId = "11111111-1111-4111-8111-111111111111";
    const repository = {
      id: repositoryId,
      name: "npm-hosted",
      format: "npm",
      type: "hosted",
      anonymousRead: false,
      state: "active",
      version: "1",
    } as const;
    mockGetRepository.mockResolvedValue({ data: repository } as never);
    mockGetCapabilities.mockResolvedValue({
      data: {
        format: "npm",
        type: "hosted",
        operations: ["read", "publish"],
        artifactScanning: true,
        publicationScanning: true,
      },
    } as never);
    mockGetCapacity.mockResolvedValue({
      data: {
        repositoryId,
        format: "npm",
        usedBytes: 0,
        objectCount: 0,
        quotaBytes: 0,
        quotaExceeded: false,
        usageRatio: 0,
        reclaimableBytes: 0,
      },
    } as never);
    mockGetEffectiveAccess.mockResolvedValue({
      data: {
        actor: "admin",
        resource: "",
        simulated: false,
        repository,
        identity: { kind: "static", subject: "admin", displayName: "admin" },
        anonymousRead: { allowed: false, source: "repository", reason: "off" },
        permissions: {
          read: { allowed: true, source: "admin", reason: "admin" },
          write: { allowed: true, source: "admin", reason: "admin" },
          admin: { allowed: true, source: "admin", reason: "admin" },
          intelligence: { allowed: true, source: "admin", reason: "admin" },
        },
      },
    } as never);

    render(
      <PreferencesProvider>
        <MemoryRouter
          initialEntries={[`/repositories/${repositoryId}?tab=scanning`]}
        >
          <Routes>
            <Route
              path="/repositories/:repositoryId"
              element={<RepositoryDetailPage />}
            />
          </Routes>
        </MemoryRouter>
      </PreferencesProvider>,
    );

    expect(
      await screen.findByRole("tab", { name: "制品扫描", selected: true }),
    ).toBeInTheDocument();
    expect(await screen.findByText("扫描工作区已加载")).toBeInTheDocument();
    await waitFor(() => expect(scanningTab.render).toHaveBeenCalled());
    expect(scanningTab.render.mock.calls.at(-1)?.[0]).toMatchObject({
      repo: repository,
      capabilitiesLoading: false,
      capabilitiesError: null,
      canManage: true,
      canViewJobs: true,
    });
  });
});
