import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import {
  getAnonymousAccessPolicy,
  listApiKeys,
  listRepositories,
  listRepositoryGrants,
  listUsers,
} from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { AccessControlPage } from "./AccessControl";

vi.mock("../client", async () => {
  const actual = await vi.importActual<typeof import("../client")>("../client");
  return {
    ...actual,
    getAnonymousAccessPolicy: vi.fn(),
    listApiKeys: vi.fn(),
    listRepositories: vi.fn(),
    listRepositoryGrants: vi.fn(),
    listUsers: vi.fn(),
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
const mockListApiKeys = vi.mocked(listApiKeys);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRepositoryGrants = vi.mocked(listRepositoryGrants);
const mockListUsers = vi.mocked(listUsers);

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
    expect(screen.getAllByText(/当前登录身份/)).not.toHaveLength(0);
    expect(screen.getByText(/OIDC \/ 自定义 actor/)).toBeInTheDocument();
    expect(screen.queryByText(/disabled-user/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Revoked key/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Expired key/)).not.toBeInTheDocument();
  });
});
