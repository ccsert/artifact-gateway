import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import {
  getRepository,
  getRepositoryCapabilities,
  getRepositoryCapacity,
  getRepositoryEffectiveAccess,
  updateRepository,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import type { Repository } from "../../client";
import { RepositoryDetailPage } from "./RepositoryDetail";
import { RepositorySettingsTab } from "./RepositorySettingsTab";

const auth = vi.hoisted(() => ({
  identity: { administrator: true, role: "admin" },
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => auth,
}));

const scanningTab = vi.hoisted(() => ({
  render: vi.fn((props: unknown) => {
    void props;
    return "扫描工作区已加载";
  }),
}));
const artifactsTab = vi.hoisted(() => ({
  render: vi.fn((props: unknown) => {
    void props;
    return "制品视图已加载";
  }),
}));

vi.mock("../../client", async () => {
  const actual =
    await vi.importActual<typeof import("../../client")>("../../client");
  return {
    ...actual,
    getRepository: vi.fn(),
    getRepositoryCapabilities: vi.fn(),
    getRepositoryCapacity: vi.fn(),
    getRepositoryEffectiveAccess: vi.fn(),
    updateRepository: vi.fn(),
  };
});

vi.mock("./RepositoryScanningTab", () => ({
  RepositoryScanningTab: scanningTab.render,
}));
vi.mock("./RepositoryArtifactsTab", () => ({
  RepositoryArtifactsTab: artifactsTab.render,
}));

const mockGetRepository = vi.mocked(getRepository);
const mockGetCapabilities = vi.mocked(getRepositoryCapabilities);
const mockGetCapacity = vi.mocked(getRepositoryCapacity);
const mockGetEffectiveAccess = vi.mocked(getRepositoryEffectiveAccess);
const mockUpdateRepository = vi.mocked(updateRepository);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  auth.identity = { administrator: true, role: "admin" };
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
      mavenStrictPublication: false,
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
    expect(
      screen.getByRole("navigation", { name: "仓库任务" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("tab")).toHaveLength(12);
    expect(
      screen.queryByRole("tab", { name: "策略与安全" }),
    ).not.toBeInTheDocument();
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

describe("RepositoryDetailPage role-scoped tabs", () => {
  const repositoryId = "22222222-2222-4222-8222-222222222222";
  const repository = {
    id: repositoryId,
    name: "maven-hosted",
    format: "maven",
    type: "hosted",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  } as const;

  function renderRole(
    canWrite: boolean,
    format: "maven" | "oci" = "maven",
    initialTab = "settings",
    authority: Partial<{
      read: boolean;
      write: boolean;
      admin: boolean;
      intelligence: boolean;
    }> = {},
  ) {
    mockGetRepository.mockResolvedValue({
      data: { ...repository, format },
    } as never);
    mockGetCapabilities.mockResolvedValue({
      data: {
        format,
        type: "hosted",
        operations: ["read", "publish"],
      },
    } as never);
    mockGetCapacity.mockResolvedValue({ data: undefined } as never);
    mockGetEffectiveAccess.mockResolvedValue({
      data: {
        permissions: {
          read: { allowed: authority.read ?? true },
          write: { allowed: authority.write ?? canWrite },
          admin: { allowed: authority.admin ?? false },
          intelligence: { allowed: authority.intelligence ?? false },
        },
      },
    } as never);
    return render(
      <PreferencesProvider>
        <MemoryRouter
          initialEntries={[`/repositories/${repositoryId}?tab=${initialTab}`]}
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
  }

  it("offers a member with a write grant the read and write surfaces of that repository", async () => {
    auth.identity = { administrator: false, role: "member" };
    renderRole(true, "maven", "artifacts");
    expect(await screen.findByText("制品视图已加载")).toBeInTheDocument();
    // The read-gated surfaces come from the same access answer, so a write grant
    // also reaches usage; nothing that needs administration does.
    expect(screen.getByRole("tab", { name: "使用统计" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "发布" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "设置" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("tab", { name: "访问授权" }),
    ).not.toBeInTheDocument();
    expect(artifactsTab.render.mock.calls.at(-1)?.[0]).toMatchObject({
      canWrite: true,
    });
  });

  it("keeps a member without write authority on the read-only surfaces", async () => {
    auth.identity = { administrator: false, role: "member" };
    renderRole(false, "maven", "artifacts");
    expect(await screen.findByText("制品视图已加载")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "使用统计" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "发布" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "设置" })).not.toBeInTheDocument();
    expect(artifactsTab.render.mock.calls.at(-1)?.[0]).toMatchObject({
      canWrite: false,
    });
  });

  it("offers a repository administrator the management surfaces of its own repository", async () => {
    auth.identity = { administrator: false, role: "member" };
    renderRole(false, "maven", "artifacts", { admin: true });
    expect(await screen.findByText("制品视图已加载")).toBeInTheDocument();
    for (const name of [
      "访问授权",
      "保留策略",
      "安全准入",
      "容量",
      "设置",
      "墓碑",
      "晋升 / 复制",
      "生命周期任务",
    ]) {
      expect(screen.getByRole("tab", { name })).toBeInTheDocument();
    }
    // Scanning needs the independent intelligence scope, so it stays hidden even
    // for a repository administrator.
    expect(
      screen.queryByRole("tab", { name: "制品扫描" }),
    ).not.toBeInTheDocument();
  });

  it("shows OCI Docker publication instructions on a publisher deep link", async () => {
    auth.identity = { administrator: false, role: "member" };
    renderRole(true, "oci", "publish");
    expect(
      await screen.findByRole("tab", { name: "发布", selected: true }),
    ).toBeInTheDocument();
    expect(
      await screen.findByText("通过 Docker 或 Podman 发布"),
    ).toBeInTheDocument();
    expect(screen.getByText(/docker push/)).toBeInTheDocument();
  });
});

describe("RepositoryDetailPage Go lifecycle surfaces", () => {
  it("exposes retention, security, promotion, jobs, and tombstones for a Go hosted repository", async () => {
    const repositoryId = "33333333-3333-4333-8333-333333333333";
    const repository = {
      id: repositoryId,
      name: "go-modules",
      format: "go",
      type: "hosted",
      anonymousRead: false,
      mavenStrictPublication: false,
      state: "active",
      version: "1",
    } as const;
    mockGetRepository.mockResolvedValue({ data: repository } as never);
    mockGetCapabilities.mockResolvedValue({
      data: {
        format: "go",
        type: "hosted",
        operations: ["read", "publish", "retain", "promote", "replicate"],
        artifactScanning: true,
        publicationScanning: true,
      },
    } as never);
    mockGetCapacity.mockResolvedValue({
      data: {
        repositoryId,
        format: "go",
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
        <MemoryRouter initialEntries={[`/repositories/${repositoryId}`]}>
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
      await screen.findByRole("tab", { name: "保留策略" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "安全准入" })).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "晋升 / 复制" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "生命周期任务" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "墓碑" })).toBeInTheDocument();
  });
});

describe("RepositorySettingsTab upstream authentication", () => {
  const goProxy: Repository = {
    id: "44444444-4444-4444-8444-444444444444",
    name: "go-proxy",
    format: "go",
    type: "proxy",
    endpoint: "https://proxy.golang.org",
    allowedHosts: ["proxy.golang.org"],
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "3",
  };

  it("submits a sealed upstream credential for a Go proxy repository", async () => {
    const user = userEvent.setup();
    mockUpdateRepository.mockResolvedValue({ data: goProxy } as never);

    render(
      <PreferencesProvider>
        <RepositorySettingsTab
          repo={goProxy}
          capabilities={null}
          onUpdated={vi.fn()}
        />
      </PreferencesProvider>,
    );

    expect(screen.getByText("上游认证")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /Bearer 令牌/ }));
    await user.type(screen.getByLabelText("上游令牌"), "ghp_upstream_token");
    await user.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() =>
      expect(mockUpdateRepository).toHaveBeenCalledWith(
        expect.objectContaining({
          path: { repositoryId: goProxy.id },
          headers: { "If-Match": goProxy.version },
          body: expect.objectContaining({
            upstreamAuth: { scheme: "bearer", secret: "ghp_upstream_token" },
          }),
        }),
      ),
    );
  });

  it("offers upstream authentication only for a Go proxy repository", () => {
    const mavenProxy = {
      ...goProxy,
      format: "maven",
      name: "maven-proxy",
      endpoint: "https://repo1.maven.org",
    } as const;

    render(
      <PreferencesProvider>
        <RepositorySettingsTab
          repo={mavenProxy}
          capabilities={null}
          onUpdated={vi.fn()}
        />
      </PreferencesProvider>,
    );

    expect(screen.queryByText("上游认证")).not.toBeInTheDocument();
  });
});

describe("RepositorySettingsTab Maven publication policy", () => {
  it("keeps strict publication off by default and persists an explicit opt-in", async () => {
    const user = userEvent.setup();
    const repository = {
      id: "22222222-2222-4222-8222-222222222222",
      name: "maven-hosted",
      format: "maven",
      type: "hosted",
      anonymousRead: false,
      mavenStrictPublication: false,
      state: "active",
      version: "7",
    } as const;
    mockUpdateRepository.mockResolvedValue({ data: repository } as never);

    render(
      <PreferencesProvider>
        <RepositorySettingsTab
          repo={repository}
          capabilities={null}
          onUpdated={vi.fn()}
        />
      </PreferencesProvider>,
    );

    const strictSwitch = screen.getByRole("switch", { name: "严格发布" });
    expect(strictSwitch).not.toBeChecked();
    await user.click(strictSwitch);
    await user.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() =>
      expect(mockUpdateRepository).toHaveBeenCalledWith(
        expect.objectContaining({
          path: { repositoryId: repository.id },
          headers: { "If-Match": repository.version },
          body: {
            anonymousRead: false,
            mavenStrictPublication: true,
          },
        }),
      ),
    );
  });
});
