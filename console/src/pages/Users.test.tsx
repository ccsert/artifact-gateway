import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getOidcSettings,
  listUserIdentities,
  listUserSessions,
  listUsers,
} from "../client";
import type { User } from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { UsersPage } from "./Users";

vi.mock("../client", () => ({
  createUser: vi.fn(),
  createUserIdentity: vi.fn(),
  deleteUser: vi.fn(),
  deleteUserIdentity: vi.fn(),
  getOidcSettings: vi.fn(),
  listUsers: vi.fn(),
  listUserIdentities: vi.fn(),
  listUserSessions: vi.fn(),
  resetUserPassword: vi.fn(),
  revokeUserSessions: vi.fn(),
  revokeUserSession: vi.fn(),
  updateUser: vi.fn(),
}));

const mockListUsers = vi.mocked(listUsers);
const mockListUserIdentities = vi.mocked(listUserIdentities);
const mockGetOidcSettings = vi.mocked(getOidcSettings);
const mockListUserSessions = vi.mocked(listUserSessions);

const alice: User = {
  id: "00000000-0000-0000-0000-000000000001",
  name: "alice",
  displayName: "Alice Chen",
  email: "alice@example.test",
  description: "Release owner",
  role: "admin",
  state: "active",
  lastLoginAt: "2026-08-10T08:00:00Z",
  passwordChangedAt: "2026-08-01T08:00:00Z",
  localPasswordEnabled: true,
  failedLoginAttempts: 0,
  mustChangePassword: false,
  createdAt: "2026-07-01T08:00:00Z",
  updatedAt: "2026-08-10T08:00:00Z",
  version: "4",
};

function renderPage() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <UsersPage />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("UsersPage", () => {
  it("loads a server-paginated user list and opens the selected account", async () => {
    mockListUsers.mockResolvedValue({
      data: { items: [alice], total: 1, limit: 20, offset: 0 },
    } as never);
    mockListUserIdentities.mockResolvedValue({ data: { items: [] } } as never);
    mockGetOidcSettings.mockResolvedValue({
      data: { issuer: "https://issuer.example.test" },
    } as never);
    mockListUserSessions.mockResolvedValue({ data: { items: [] } } as never);

    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText("Alice Chen")).toBeInTheDocument();
    expect(mockListUsers).toHaveBeenCalledWith({
      query: {
        search: undefined,
        role: undefined,
        state: undefined,
        limit: 20,
        offset: 0,
      },
    });

    await user.click(screen.getByText("Alice Chen"));
    expect(
      await screen.findByRole("heading", { name: "账户资料" }),
    ).toBeInTheDocument();
    expect(screen.getByText("登录与安全")).toBeInTheDocument();
    expect(screen.getByDisplayValue("alice@example.test")).toBeInTheDocument();
  });

  it("debounces search and sends it to the server", async () => {
    mockListUsers.mockResolvedValue({
      data: { items: [], total: 0, limit: 20, offset: 0 },
    } as never);
    mockListUserIdentities.mockResolvedValue({ data: { items: [] } } as never);
    const user = userEvent.setup();
    renderPage();

    await screen.findByText("暂无用户");
    await user.type(
      screen.getByPlaceholderText("搜索用户名、显示名或邮箱…"),
      " alice ",
    );

    await waitFor(() => {
      expect(mockListUsers).toHaveBeenLastCalledWith({
        query: {
          search: "alice",
          role: undefined,
          state: undefined,
          limit: 20,
          offset: 0,
        },
      });
    });
  });
});
