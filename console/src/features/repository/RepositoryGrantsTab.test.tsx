import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  deleteGrant,
  listApiKeys,
  listAuthorizationRoles,
  listGrants,
  listServiceAccounts,
  listUsers,
  upsertGrant,
} from "../../client";
import type { Grant, Repository } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryGrantsTab } from "./RepositoryGrantsTab";

vi.mock("../../client", () => ({
  deleteGrant: vi.fn(),
  listApiKeys: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  listGrants: vi.fn(),
  listServiceAccounts: vi.fn(),
  listUsers: vi.fn(),
  upsertGrant: vi.fn(),
}));

const mockDeleteGrant = vi.mocked(deleteGrant);
const mockListApiKeys = vi.mocked(listApiKeys);
const mockListAuthorizationRoles = vi.mocked(listAuthorizationRoles);
const mockListGrants = vi.mocked(listGrants);
const mockListServiceAccounts = vi.mocked(listServiceAccounts);
const mockListUsers = vi.mocked(listUsers);
const mockUpsertGrant = vi.mocked(upsertGrant);

const repository: Repository = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "releases",
  format: "raw",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

const existingGrant: Grant = {
  principal: "user:active-user",
  scopes: ["repositories:read"],
};

function mockPrincipalSources() {
  mockListAuthorizationRoles.mockResolvedValue({ data: [] } as never);
  mockListUsers.mockResolvedValue({
    data: {
      items: [
        { name: "active-user", role: "member", state: "active" },
        { name: "disabled-user", role: "member", state: "disabled" },
      ],
    },
  } as never);
  mockListApiKeys.mockResolvedValue({
    data: {
      items: [
        { id: "active-key", name: "Active key", roles: ["member"] },
        {
          id: "revoked-key",
          name: "Revoked key",
          roles: ["admin"],
          revokedAt: "2026-08-12T00:00:00Z",
        },
        {
          id: "expired-key",
          name: "Expired key",
          roles: ["member"],
          expiresAt: "2000-01-01T00:00:00Z",
        },
      ],
    },
  } as never);
  mockListServiceAccounts.mockResolvedValue({
    data: {
      items: [
        {
          id: "service-account-id",
          name: "release-bot",
          description: "release publisher",
          state: "active",
          createdAt: "2026-08-18T00:00:00Z",
          updatedAt: "2026-08-18T00:00:00Z",
          version: "1",
        },
      ],
    },
  } as never);
}

function renderTab() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <RepositoryGrantsTab repo={repository} />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryGrantsTab", () => {
  it("offers only active users and usable API keys when adding a grant", async () => {
    mockListGrants.mockResolvedValue({ data: [] } as never);
    mockPrincipalSources();

    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox", { name: "授权主体" }));

    expect(screen.getByText(/用户 · active-user/)).toBeInTheDocument();
    expect(screen.getByText(/API Key · Active key/)).toBeInTheDocument();
    expect(screen.getByText(/服务账号 · release-bot/)).toBeInTheDocument();
    expect(screen.getByText(/OIDC \/ 自定义 actor/)).toBeInTheDocument();
    expect(screen.queryByText(/disabled-user/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Revoked key/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Expired key/)).not.toBeInTheDocument();
  });

  it("upserts exactly the composed row when adding a grant", async () => {
    mockListGrants.mockResolvedValue({ data: [] } as never);
    mockPrincipalSources();
    mockUpsertGrant.mockResolvedValue({
      data: [existingGrant],
    } as never);

    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox", { name: "授权主体" }));
    await user.click(await screen.findByText(/用户 · active-user/));
    await user.click(screen.getByRole("button", { name: /保\s*存/ }));

    await waitFor(() =>
      expect(mockUpsertGrant).toHaveBeenCalledWith({
        path: { repositoryId: repository.id },
        body: {
          principal: "user:active-user",
          scopes: ["repositories:read"],
          resourcePrefix: undefined,
        },
      }),
    );
    expect(mockDeleteGrant).not.toHaveBeenCalled();
  });

  it("shows an upsert failure inside the editor and keeps it open", async () => {
    mockListGrants.mockResolvedValue({ data: [] } as never);
    mockPrincipalSources();
    mockUpsertGrant.mockResolvedValue({
      error: { message: "upsert failed", status: 500 },
    } as never);

    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox", { name: "授权主体" }));
    await user.click(await screen.findByText(/用户 · active-user/));
    await user.click(screen.getByRole("button", { name: /保\s*存/ }));

    expect(await screen.findByText(/upsert failed/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /保\s*存/ })).toBeInTheDocument();
  });

  it("upserts without touching other rows when a grant is edited in place", async () => {
    mockListGrants.mockResolvedValue({ data: [existingGrant] } as never);
    mockPrincipalSources();
    mockUpsertGrant.mockResolvedValue({
      data: [{ ...existingGrant, scopes: ["repositories:write"] }],
    } as never);

    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole("button", { name: /编\s*辑/ }));
    await user.click(screen.getByRole("combobox", { name: "权限级别" }));
    await user.click(await screen.findByText("写入 · 发布 / 编辑"));
    await user.click(screen.getByRole("button", { name: /保\s*存/ }));

    await waitFor(() =>
      expect(mockUpsertGrant).toHaveBeenCalledWith({
        path: { repositoryId: repository.id },
        body: {
          principal: "user:active-user",
          scopes: ["repositories:write"],
          resourcePrefix: undefined,
        },
      }),
    );
    expect(mockDeleteGrant).not.toHaveBeenCalled();
  });

  it("deletes the previous key when an edit moves the grant to a new principal", async () => {
    mockListGrants.mockResolvedValue({ data: [existingGrant] } as never);
    mockPrincipalSources();
    mockUpsertGrant.mockResolvedValue({
      data: [
        { principal: "api-key:active-key", scopes: ["repositories:read"] },
      ],
    } as never);
    mockDeleteGrant.mockResolvedValue({} as never);

    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole("button", { name: /编\s*辑/ }));
    await user.click(screen.getByRole("combobox", { name: "授权主体" }));
    await user.click(await screen.findByText(/API Key · Active key/));
    await user.click(screen.getByRole("button", { name: /保\s*存/ }));

    await waitFor(() =>
      expect(mockDeleteGrant).toHaveBeenCalledWith({
        path: { repositoryId: repository.id },
        query: {
          principal: "user:active-user",
          resourcePrefix: undefined,
        },
      }),
    );
  });

  it("removes only the confirmed row", async () => {
    const otherGrant: Grant = {
      principal: "service-account:service-account-id",
      scopes: ["repositories:admin"],
    };
    mockListGrants.mockResolvedValue({
      data: [existingGrant, otherGrant],
    } as never);
    mockPrincipalSources();
    mockDeleteGrant.mockResolvedValue({} as never);

    const user = userEvent.setup();
    renderTab();

    const row = (await screen.findByText("user:active-user")).closest("tr");
    expect(row).not.toBeNull();
    await user.click(within(row!).getByRole("button", { name: /删\s*除/ }));
    await user.click(await screen.findByRole("button", { name: /移\s*除/ }));

    await waitFor(() =>
      expect(mockDeleteGrant).toHaveBeenCalledWith({
        path: { repositoryId: repository.id },
        query: {
          principal: "user:active-user",
          resourcePrefix: undefined,
        },
      }),
    );
    await waitFor(() =>
      expect(screen.queryByText("user:active-user")).not.toBeInTheDocument(),
    );
    expect(
      screen.getByText("service-account:service-account-id"),
    ).toBeInTheDocument();
    expect(mockUpsertGrant).not.toHaveBeenCalled();
  });
});
