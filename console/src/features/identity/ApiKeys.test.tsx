import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createApiKey, listApiKeys, revokeApiKey } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { ApiKeysPage } from "./ApiKeys";

vi.mock("../../client", () => ({
  createApiKey: vi.fn(),
  listApiKeys: vi.fn(),
  revokeApiKey: vi.fn(),
}));

const mockListKeys = vi.mocked(listApiKeys);
const mockCreateKey = vi.mocked(createApiKey);

// The mask is the dismissal route the create dialog's `busy` flag controls,
// and it only closes on a click that both starts and lands on it.
function clickMask() {
  const mask = document.querySelector(".ant-modal-wrap");
  expect(mask).not.toBeNull();
  fireEvent.mouseDown(mask as HTMLElement);
  fireEvent.click(mask as HTMLElement);
}

function renderPage() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <ApiKeysPage />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ApiKeysPage", () => {
  it("holds the create dialog shut while saving and frees it after a failure", async () => {
    const user = userEvent.setup();
    mockListKeys.mockResolvedValue({ data: { items: [] } } as never);
    let rejectSave: (error: unknown) => void = () => {};
    mockCreateKey.mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          rejectSave = reject;
        }) as never,
    );

    renderPage();
    await user.click(await screen.findByRole("button", { name: /新建密钥/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByPlaceholderText("my-key"), "ci-deploy");
    await user.click(within(dialog).getByRole("button", { name: /创\s*建/ }));

    clickMask();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    rejectSave(new Error("network down"));
    expect(
      await within(screen.getByRole("dialog")).findByText("network down"),
    ).toBeInTheDocument();

    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("frees the revoke confirmation when the request rejects", async () => {
    const user = userEvent.setup();
    mockListKeys.mockResolvedValue({
      data: {
        items: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            name: "ci-deploy",
            roles: ["member"],
            createdAt: "2026-08-01T00:00:00Z",
          },
        ],
      },
    } as never);
    vi.mocked(revokeApiKey).mockRejectedValueOnce(new Error("network down"));

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: "吊销密钥 ci-deploy" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /^吊\s*销$/ }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
    // The rejection released `revoking`, so the dialog is dismissible again.
    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
});
