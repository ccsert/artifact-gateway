import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listApiKeys,
  listAuthorizationRoles,
  listGrants,
  listUsers,
} from "../../client";
import type { Repository } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryGrantsTab } from "./RepositoryGrantsTab";

vi.mock("../../client", () => ({
  listApiKeys: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  listGrants: vi.fn(),
  listUsers: vi.fn(),
  replaceGrants: vi.fn(),
}));

const mockListApiKeys = vi.mocked(listApiKeys);
const mockListAuthorizationRoles = vi.mocked(listAuthorizationRoles);
const mockListGrants = vi.mocked(listGrants);
const mockListUsers = vi.mocked(listUsers);

const repository: Repository = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "releases",
  format: "raw",
  type: "hosted",
  anonymousRead: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryGrantsTab", () => {
  it("offers only active users and usable API keys when adding a grant", async () => {
    mockListGrants.mockResolvedValue({ data: [] } as never);
    mockListAuthorizationRoles.mockResolvedValue({ data: [] } as never);
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

    const user = userEvent.setup();
    render(
      <PreferencesProvider>
        <AntdProvider>
          <RepositoryGrantsTab repo={repository} />
        </AntdProvider>
      </PreferencesProvider>,
    );

    await user.click(await screen.findByText("编辑授权"));
    await user.click(screen.getByText("添加授权规则"));
    await user.click(screen.getByRole("combobox", { name: "授权主体" }));

    expect(screen.getByText(/用户 · active-user/)).toBeInTheDocument();
    expect(screen.getByText(/API Key · Active key/)).toBeInTheDocument();
    expect(screen.queryByText(/disabled-user/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Revoked key/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Expired key/)).not.toBeInTheDocument();
  });
});
