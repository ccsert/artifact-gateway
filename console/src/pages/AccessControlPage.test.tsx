import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import {
  getAnonymousAccessPolicy,
  getRepositoryEffectiveAccess,
  listApiKeys,
  listRepositories,
  listRepositoryGrants,
  listServiceAccounts,
  listUsers,
  replaceAnonymousAccessPolicy,
} from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { AccessControlPage } from "./AccessControl";

vi.mock("../client", async () => {
  const actual = await vi.importActual<typeof import("../client")>("../client");
  return {
    ...actual,
    getAnonymousAccessPolicy: vi.fn(),
    getRepositoryEffectiveAccess: vi.fn(),
    listApiKeys: vi.fn(),
    listRepositories: vi.fn(),
    listRepositoryGrants: vi.fn(),
    listServiceAccounts: vi.fn(),
    listUsers: vi.fn(),
    replaceAnonymousAccessPolicy: vi.fn(),
  };
});

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    identity: {
      actor: "user:operator",
      kind: "local",
      role: "admin",
      administrator: true,
    },
    identityLoading: false,
  }),
}));

const mockGetAnonymousAccessPolicy = vi.mocked(getAnonymousAccessPolicy);
const mockGetRepositoryEffectiveAccess = vi.mocked(
  getRepositoryEffectiveAccess,
);
const mockListApiKeys = vi.mocked(listApiKeys);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRepositoryGrants = vi.mocked(listRepositoryGrants);
const mockListServiceAccounts = vi.mocked(listServiceAccounts);
const mockListUsers = vi.mocked(listUsers);
const mockReplaceAnonymousAccessPolicy = vi.mocked(
  replaceAnonymousAccessPolicy,
);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("AccessControlPage", () => {
  it("omits disabled users and unusable API keys from the principal picker", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockGetAnonymousAccessPolicy.mockResolvedValue({
      data: { enabled: false, version: "1" },
    } as never);
    mockListRepositories.mockResolvedValue({
      data: {
        items: [
          {
            id: "00000000-0000-4000-8000-000000000001",
            name: "releases",
            format: "raw",
            type: "hosted",
            state: "active",
          },
        ],
      },
    } as never);
    mockListUsers.mockResolvedValue({
      data: {
        items: [
          { name: "active-user", role: "reader", state: "active" },
          { name: "disabled-user", role: "writer", state: "disabled" },
        ],
      },
    } as never);
    mockListApiKeys.mockResolvedValue({
      data: {
        items: [
          { id: "active-key", name: "Active key", roles: ["reader"] },
          {
            id: "revoked-key",
            name: "Revoked key",
            roles: ["writer"],
            revokedAt: "2026-08-12T00:00:00Z",
          },
          {
            id: "expired-key",
            name: "Expired key",
            roles: ["reader"],
            expiresAt: "2000-01-01T00:00:00Z",
          },
        ],
      },
    } as never);
    mockListServiceAccounts.mockResolvedValue({
      data: {
        items: [
          {
            id: "active-service-account",
            name: "pipeone-ci",
            description: "CI publisher",
            state: "active",
            createdAt: "2026-08-18T00:00:00Z",
            updatedAt: "2026-08-18T00:00:00Z",
            version: "1",
          },
          {
            id: "disabled-service-account",
            name: "old-ci",
            description: "disabled",
            state: "disabled",
            createdAt: "2026-08-18T00:00:00Z",
            updatedAt: "2026-08-18T00:00:00Z",
            version: "1",
          },
        ],
      },
    } as never);

    const user = userEvent.setup();
    render(
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter initialEntries={["/access?tab=evaluate"]}>
            <AccessControlPage />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>,
    );

    const principalPicker = await screen.findByRole("combobox", {
      name: "授权主体",
    });
    await user.click(principalPicker);

    expect(screen.getByText(/用户 · active-user/)).toBeInTheDocument();
    expect(screen.getByText(/API Key · Active key/)).toBeInTheDocument();
    expect(screen.getByText(/服务账号 · pipeone-ci/)).toBeInTheDocument();
    expect(screen.getAllByText(/当前登录身份/)).not.toHaveLength(0);
    expect(screen.getByText(/OIDC \/ 自定义 actor/)).toBeInTheDocument();
    expect(screen.queryByText(/disabled-user/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Revoked key/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Expired key/)).not.toBeInTheDocument();
    expect(screen.queryByText(/old-ci/)).not.toBeInTheDocument();

    await user.click(screen.getByText(/服务账号 · pipeone-ci/));
    mockGetRepositoryEffectiveAccess.mockResolvedValue({
      data: {
        actor: "service-account:active-service-account",
        resource: "",
        simulated: true,
        repository: {
          id: "00000000-0000-4000-8000-000000000001",
          name: "releases",
          format: "raw",
          type: "hosted",
          state: "active",
        },
        identity: {
          kind: "service_account_credential",
          subject: "active-service-account",
          displayName: "pipeone-ci",
        },
        anonymousRead: {
          allowed: false,
          source: "repository",
          reason: "private",
        },
        permissions: {
          read: { allowed: true, source: "grant", reason: "matched" },
          write: { allowed: true, source: "grant", reason: "matched" },
          admin: { allowed: false, source: "none", reason: "not granted" },
          intelligence: {
            allowed: false,
            source: "none",
            reason: "not granted",
          },
        },
      },
    } as never);
    await user.click(screen.getByRole("button", { name: /检\s*查/ }));
    await waitFor(() =>
      expect(mockGetRepositoryEffectiveAccess).toHaveBeenCalledWith({
        path: { repositoryId: "00000000-0000-4000-8000-000000000001" },
        query: {
          actor: "service-account:active-service-account",
          role: undefined,
          resource: undefined,
        },
      }),
    );
    expect(await screen.findByText("模拟结果")).toBeInTheDocument();
  });

  it("explains anonymous access as a layered read-only boundary without changing policy semantics", async () => {
    const user = userEvent.setup();
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockGetAnonymousAccessPolicy.mockResolvedValue({
      data: { enabled: true, version: "7" },
    } as never);
    mockListRepositories.mockResolvedValue({
      data: {
        items: [
          {
            id: "00000000-0000-4000-8000-000000000001",
            name: "public-maven",
            format: "maven",
            type: "hosted",
            state: "active",
            anonymousRead: true,
          },
          {
            id: "00000000-0000-4000-8000-000000000002",
            name: "private-npm",
            format: "npm",
            type: "hosted",
            state: "active",
            anonymousRead: false,
          },
        ],
      },
    } as never);
    mockListUsers.mockResolvedValue({ data: { items: [] } } as never);
    mockListApiKeys.mockResolvedValue({ data: { items: [] } } as never);
    mockListServiceAccounts.mockResolvedValue({ data: { items: [] } } as never);
    mockReplaceAnonymousAccessPolicy.mockResolvedValue({
      data: { enabled: false, version: "8" },
    } as never);

    render(
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter initialEntries={["/access?tab=policies"]}>
            <AccessControlPage />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>,
    );

    expect(await screen.findByText("公开访问边界")).toBeInTheDocument();
    expect(screen.getByText("全局总闸")).toBeInTheDocument();
    expect(screen.getByText("仓库显式开启")).toBeInTheDocument();
    expect(screen.getByText("分组双重同意")).toBeInTheDocument();
    expect(screen.getByText("只开放读取协议")).toBeInTheDocument();
    expect(screen.getByText("1 / 2 个仓库公开")).toBeInTheDocument();
    expect(
      screen.getByRole("switch", { name: "切换全局匿名读取" }),
    ).toBeChecked();
    await user.click(screen.getByRole("switch", { name: "切换全局匿名读取" }));
    await user.click(
      within(await screen.findByRole("tooltip")).getByRole("button", {
        name: /继\s*续/,
      }),
    );
    await waitFor(() =>
      expect(mockReplaceAnonymousAccessPolicy).toHaveBeenCalledWith({
        body: { enabled: false, version: "7" },
        headers: { "If-Match": "7" },
      }),
    );
    expect(
      await screen.findByRole("switch", { name: "切换全局匿名读取" }),
    ).not.toBeChecked();
  });

  it("renders and filters user, service-account, API key, and custom repository grants", async () => {
    const user = userEvent.setup();
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        {
          repositoryId: "00000000-0000-4000-8000-000000000001",
          repositoryName: "release-repository",
          format: "maven",
          principal: "user:alice",
          scopes: ["repositories:read"],
        },
        {
          repositoryId: "00000000-0000-4000-8000-000000000001",
          repositoryName: "release-repository",
          format: "maven",
          principal: "service-account:11111111-1111-4111-8111-111111111111",
          scopes: ["repositories:write"],
          resourcePrefix: "org/example",
        },
        {
          repositoryId: "00000000-0000-4000-8000-000000000001",
          repositoryName: "release-repository",
          format: "maven",
          principal: "api-key:legacy-publisher",
          scopes: ["repositories:admin"],
        },
        {
          repositoryId: "00000000-0000-4000-8000-000000000002",
          repositoryName: "raw-assets",
          format: "raw",
          principal: "oidc:gitlab:release-team",
          scopes: ["repositories:intelligence"],
        },
      ],
    } as never);
    mockGetAnonymousAccessPolicy.mockResolvedValue({
      data: { enabled: false, version: "1" },
    } as never);
    mockListRepositories.mockResolvedValue({ data: { items: [] } } as never);
    mockListUsers.mockResolvedValue({ data: { items: [] } } as never);
    mockListApiKeys.mockResolvedValue({ data: { items: [] } } as never);
    mockListServiceAccounts.mockResolvedValue({ data: { items: [] } } as never);

    render(
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter initialEntries={["/access"]}>
            <AccessControlPage />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>,
    );

    expect(await screen.findByText("用户 · alice")).toBeInTheDocument();
    expect(screen.getByText(/服务账号 · 11111111/)).toBeInTheDocument();
    expect(screen.getByText(/API Key · legacy-p…/)).toBeInTheDocument();
    expect(screen.getByText("OIDC / 自定义")).toBeInTheDocument();
    expect(screen.getByText("制品情报")).toBeInTheDocument();
    expect(screen.getByText("高权限")).toBeInTheDocument();

    await user.type(
      screen.getByPlaceholderText("用户名、API Key 或 actor"),
      "service-account",
    );
    expect(await screen.findByText("逐仓库授权（1）")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /清\s*除/ }));
    expect(await screen.findByText("逐仓库授权（4）")).toBeInTheDocument();
    await user.type(screen.getByPlaceholderText("仓库名称"), "raw-assets");
    expect(await screen.findByText("逐仓库授权（1）")).toBeInTheDocument();
  });
});
