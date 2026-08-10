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
  createUserIdentity,
  deleteUserIdentity,
  getOidcSettings,
  listUserIdentities,
} from "../../client";
import type { UserIdentity } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { UserIdentitiesPanel } from "./UserIdentitiesPanel";

vi.mock("../../client", () => ({
  createUserIdentity: vi.fn(),
  deleteUserIdentity: vi.fn(),
  getOidcSettings: vi.fn(),
  listUserIdentities: vi.fn(),
}));

const mockCreateIdentity = vi.mocked(createUserIdentity);
const mockDeleteIdentity = vi.mocked(deleteUserIdentity);
const mockGetOidcSettings = vi.mocked(getOidcSettings);
const mockListIdentities = vi.mocked(listUserIdentities);

const identity: UserIdentity = {
  id: "00000000-0000-0000-0000-000000000101",
  userId: "00000000-0000-0000-0000-000000000001",
  kind: "oidc",
  issuer: "https://issuer.example.test",
  subject: "provider-subject",
  email: "alice@example.test",
  displayName: "Alice",
  emailVerified: true,
  createdAt: "2026-08-10T08:00:00Z",
  updatedAt: "2026-08-10T08:00:00Z",
};

function renderPanel() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <UserIdentitiesPanel userId={identity.userId} />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("UserIdentitiesPanel", () => {
  it("loads identities and supports linking and unlinking", async () => {
    mockListIdentities.mockResolvedValue({
      data: { items: [identity] },
    } as never);
    mockGetOidcSettings.mockResolvedValue({
      data: { issuer: identity.issuer },
    } as never);
    mockCreateIdentity.mockResolvedValue({
      data: {
        ...identity,
        id: "00000000-0000-0000-0000-000000000102",
        subject: "second-subject",
      },
    } as never);
    mockDeleteIdentity.mockResolvedValue({} as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("provider-subject")).toBeInTheDocument();
    expect(mockListIdentities).toHaveBeenCalledWith({
      path: { userId: identity.userId },
    });

    await user.click(screen.getByText("绑定身份", { selector: "span" }));
    expect(screen.getByLabelText("Issuer")).toHaveValue(identity.issuer);
    await user.type(screen.getByLabelText("Subject"), "second-subject");
    const dialog = screen.getByRole("dialog", { name: "绑定 OIDC 身份" });
    await user.click(within(dialog).getByRole("button", { name: /绑\s*定/ }));

    await waitFor(() => {
      expect(mockCreateIdentity).toHaveBeenCalledWith({
        path: { userId: identity.userId },
        body: {
          issuer: "https://issuer.example.test",
          subject: "second-subject",
        },
      });
    });
    expect(await screen.findByText("second-subject")).toBeInTheDocument();

    const unlinkButtons = screen.getAllByRole("button", { name: "解绑身份" });
    await user.click(unlinkButtons[0]);
    await user.click(await screen.findByRole("button", { name: /^解\s*绑$/ }));

    await waitFor(() => {
      expect(mockDeleteIdentity).toHaveBeenCalledWith({
        path: { userId: identity.userId, identityId: identity.id },
      });
    });
    expect(screen.queryByText("provider-subject")).not.toBeInTheDocument();
  });

  it("clears a stale issuer when the OIDC provider is removed", async () => {
    mockListIdentities.mockResolvedValue({ data: { items: [] } } as never);
    mockGetOidcSettings
      .mockResolvedValueOnce({ data: { issuer: identity.issuer } } as never)
      .mockResolvedValueOnce({ data: {} } as never);

    const user = userEvent.setup();
    renderPanel();

    await screen.findByText("尚未绑定外部身份");
    await user.click(screen.getByText("绑定身份", { selector: "span" }));
    expect(screen.getByLabelText("Issuer")).toHaveValue(identity.issuer);
    await user.click(screen.getByRole("button", { name: /取\s*消/ }));

    await user.click(screen.getByRole("button", { name: "刷新外部身份" }));
    await waitFor(() => expect(mockGetOidcSettings).toHaveBeenCalledTimes(2));
    await user.click(screen.getByText("绑定身份", { selector: "span" }));

    expect(screen.getByLabelText("Issuer")).toHaveValue("");
    expect(
      screen.getByText("尚未配置 OIDC 提供方，暂时无法绑定身份。"),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("Subject"), "provider-subject");
    const dialog = screen.getByRole("dialog", { name: "绑定 OIDC 身份" });
    expect(
      within(dialog).getByRole("button", { name: /绑\s*定/ }),
    ).toBeDisabled();
  });
});
