import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listUserSessions, revokeUserSession } from "../../client";
import type { UserSession } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { UserSessionsPanel } from "./UserSessionsPanel";

vi.mock("../../client", () => ({
  listUserSessions: vi.fn(),
  revokeUserSession: vi.fn(),
}));

const mockListSessions = vi.mocked(listUserSessions);
const mockRevokeSession = vi.mocked(revokeUserSession);
const userId = "00000000-0000-0000-0000-000000000001";
const active: UserSession = {
  id: "00000000-0000-0000-0000-000000000101",
  userId,
  kind: "local_session",
  ipAddress: "127.0.0.1",
  userAgent: "Chrome on Linux",
  createdAt: "2026-08-10T08:00:00Z",
  expiresAt: "2099-08-10T20:00:00Z",
  current: false,
};

function renderPanel() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <UserSessionsPanel userId={userId} />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("UserSessionsPanel", () => {
  it("loads active sessions, toggles history, and revokes one session", async () => {
    mockListSessions
      .mockResolvedValueOnce({ data: { items: [active] } } as never)
      .mockResolvedValueOnce({
        data: {
          items: [
            active,
            {
              ...active,
              id: "00000000-0000-0000-0000-000000000102",
              userAgent: "OIDC client",
              kind: "oidc",
              revokedAt: "2026-08-10T09:00:00Z",
            },
          ],
        },
      } as never);
    mockRevokeSession.mockResolvedValue({
      data: { ...active, revokedAt: "2026-08-10T10:00:00Z" },
    } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("Chrome on Linux")).toBeInTheDocument();
    expect(mockListSessions).toHaveBeenCalledWith({
      path: { userId },
      query: { includeInactive: false },
    });

    await user.click(screen.getByRole("switch", { name: "显示失效会话" }));
    expect(await screen.findByText("OIDC client")).toBeInTheDocument();
    await waitFor(() => {
      expect(mockListSessions).toHaveBeenLastCalledWith({
        path: { userId },
        query: { includeInactive: true },
      });
    });

    await user.click(screen.getAllByRole("button", { name: "撤销会话" })[0]);
    await user.click(await screen.findByRole("button", { name: /^撤\s*销$/ }));
    await waitFor(() => {
      expect(mockRevokeSession).toHaveBeenCalledWith({
        path: { userId, sessionId: active.id },
      });
    });
    expect((await screen.findAllByText("已撤销")).length).toBe(2);
  });
});
