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
import type { Mock } from "vitest";
import {
  createGroup,
  deleteGroup,
  getGroupCapacity,
  listFormatProfiles,
  listGroupMembers,
  listGroups,
  listRepositories,
  replaceGroup,
  replaceGroupMembers,
} from "../../client";
import type { FormatProfile, Group, Repository } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { resetFormatProfilesCacheForTests } from "../../lib/formatProfiles";
import { GroupsPage } from "./Groups";

vi.mock("../../client", () => ({
  createGroup: vi.fn(),
  deleteGroup: vi.fn(),
  getGroupCapacity: vi.fn(),
  listFormatProfiles: vi.fn(),
  listGroupMembers: vi.fn(),
  listGroups: vi.fn(),
  listRepositories: vi.fn(),
  replaceGroup: vi.fn(),
  replaceGroupMembers: vi.fn(),
}));

const group: Group = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "oci-mirror",
  format: "oci",
  anonymousRead: false,
  members: [],
  version: "2",
};

const repository: Repository = {
  id: "22222222-2222-4222-8222-222222222222",
  name: "npm-lib",
  format: "oci",
  type: "hosted",
  allowedHosts: [],
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

const profiles: FormatProfile[] = [
  {
    format: "oci",
    repositoryTypes: ["hosted", "proxy"],
    groupSupported: true,
    anonymousRead: true,
    hostedOperations: ["read", "publish"],
    proxyOperations: ["read"],
  },
];

// The mask is the dismissal route these dialogs' `busy` flag controls, and it
// only closes on a click that both starts and lands on it.
function clickMask() {
  const mask = document.querySelector(".ant-modal-wrap");
  expect(mask).not.toBeNull();
  fireEvent.mouseDown(mask as HTMLElement);
  fireEvent.click(mask as HTMLElement);
}

function renderPage() {
  vi.mocked(listGroups).mockResolvedValue({
    data: { items: [group] },
  } as never);
  vi.mocked(listRepositories).mockResolvedValue({
    data: { items: [repository] },
  } as never);
  vi.mocked(listFormatProfiles).mockResolvedValue({
    data: { items: profiles },
  } as never);
  vi.mocked(getGroupCapacity).mockResolvedValue({ data: {} } as never);

  return render(
    <PreferencesProvider>
      <AntdProvider>
        <GroupsPage />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

function pendingSave(mock: Mock) {
  let rejectSave: (error: unknown) => void = () => {};
  mock.mockImplementationOnce(
    () =>
      new Promise((_, reject) => {
        rejectSave = reject;
      }),
  );
  return (error: unknown) => rejectSave(error);
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  resetFormatProfilesCacheForTests();
});

describe("GroupsPage dialogs", () => {
  it("holds the create dialog shut while saving and frees it after a failure", async () => {
    const user = userEvent.setup();
    const fail = pendingSave(vi.mocked(createGroup));

    renderPage();
    await user.click(await screen.findByRole("button", { name: /新建分组/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(
      screen.getByRole("textbox", { name: "分组名称" }),
      "js-utils",
    );
    await user.click(within(dialog).getByRole("button", { name: /npm-lib/ }));
    await user.click(within(dialog).getByRole("button", { name: /创\s*建/ }));

    clickMask();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fail(new Error("network down"));
    expect(
      await within(screen.getByRole("dialog")).findByText("network down"),
    ).toBeInTheDocument();

    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("holds the settings dialog shut while saving and frees it after a failure", async () => {
    const user = userEvent.setup();
    const fail = pendingSave(vi.mocked(replaceGroup));

    renderPage();
    await user.click(await screen.findByRole("button", { name: /设\s*置/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /保\s*存/ }));

    clickMask();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fail(new Error("network down"));
    expect(
      await within(screen.getByRole("dialog")).findByText("network down"),
    ).toBeInTheDocument();

    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("holds the member dialog shut while saving and frees it after a failure", async () => {
    const user = userEvent.setup();
    vi.mocked(listGroupMembers).mockResolvedValue({ data: [] } as never);
    const fail = pendingSave(vi.mocked(replaceGroupMembers));

    renderPage();
    await user.click(await screen.findByRole("button", { name: /编辑成员/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /保\s*存/ }));

    clickMask();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fail(new Error("network down"));
    expect(
      await within(screen.getByRole("dialog")).findByText("network down"),
    ).toBeInTheDocument();

    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("frees the delete confirmation when the request rejects", async () => {
    const user = userEvent.setup();
    vi.mocked(deleteGroup).mockRejectedValueOnce(new Error("network down"));

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: "删除分组 oci-mirror" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /^删\s*除$/ }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
    // The rejection released `deleting`, so the dialog is dismissible again.
    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
});
