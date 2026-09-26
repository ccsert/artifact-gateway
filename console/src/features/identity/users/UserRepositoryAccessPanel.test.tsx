import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  deleteGrant,
  listAuthorizationRoles,
  listGrants,
  listRepositories,
  listRepositoryGrants,
  upsertGrant,
} from "../../../client";
import { AntdProvider } from "../../../app/AntdProvider";
import { PreferencesProvider } from "../../../lib/preferences";
import { UserRepositoryAccessPanel } from "./UserRepositoryAccessPanel";

vi.mock("../../../client", () => ({
  deleteGrant: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  listGrants: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryGrants: vi.fn(),
  upsertGrant: vi.fn(),
}));

const mockListRepositoryGrants = vi.mocked(listRepositoryGrants);
const mockListRepositories = vi.mocked(listRepositories);
const mockListAuthorizationRoles = vi.mocked(listAuthorizationRoles);
const mockListGrants = vi.mocked(listGrants);
const mockUpsertGrant = vi.mocked(upsertGrant);
const mockDeleteGrant = vi.mocked(deleteGrant);

const userId = "00000000-0000-0000-0000-000000000001";
const username = "alice";
const principal = `user:${username}`;
const artifactsId = "00000000-0000-0000-0000-0000000000a1";
const iconsId = "00000000-0000-0000-0000-0000000000a2";

const artifacts = {
  id: artifactsId,
  name: "artifacts",
  format: "raw",
  state: "active",
  version: "3",
  repositoryTypes: ["hosted"],
  groupSupported: false,
  anonymousRead: false,
};

const icons = { ...artifacts, id: iconsId, name: "icons", format: "oci" };

const bobAdmin = {
  principal: "user:bob",
  scopes: ["repositories:admin", "repositories:read"],
  resourcePrefix: "builds/",
};

const aliceReleases = {
  principal,
  scopes: ["repositories:write"],
  resourcePrefix: "releases/",
};

const aliceSnapshots = {
  principal,
  scopes: ["repositories:read"],
  resourcePrefix: "snapshots/",
};

function record(overrides: Record<string, unknown> = {}) {
  return {
    repositoryId: artifactsId,
    repositoryName: "artifacts",
    format: "raw",
    principal,
    scopes: ["repositories:read"],
    ...overrides,
  };
}

function renderPanel() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <UserRepositoryAccessPanel userId={userId} username={username} />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

function mockOkSources() {
  mockListRepositories.mockResolvedValue({
    data: { items: [artifacts, icons] },
  } as never);
  mockListAuthorizationRoles.mockResolvedValue({ data: [] } as never);
}

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe("UserRepositoryAccessPanel", () => {
  it("lists only this account's grants with capability and prefix", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({
          scopes: ["repositories:write"],
          resourcePrefix: "releases/",
        }),
        record({
          repositoryId: iconsId,
          repositoryName: "icons",
          format: "oci",
          principal: "user:bob",
          scopes: ["repositories:admin"],
        }),
      ],
    } as never);
    mockOkSources();

    renderPanel();

    expect(await screen.findByText("artifacts")).toBeInTheDocument();
    expect(screen.getByText("读取 + 写入")).toBeInTheDocument();
    expect(screen.getByText("releases/")).toBeInTheDocument();
    expect(screen.queryByText("icons")).not.toBeInTheDocument();
    expect(mockListRepositoryGrants).toHaveBeenCalledTimes(1);
  });

  it("lists every entry of one repository separately so each can be acted on", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();

    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    expect(screen.getByText("snapshots/")).toBeInTheDocument();
    expect(screen.getAllByText("artifacts")).toHaveLength(2);
    expect(
      screen.getAllByRole("button", { name: "编辑该条授权" }),
    ).toHaveLength(2);
    expect(
      screen.getAllByRole("button", { name: "移除仓库授权" }),
    ).toHaveLength(2);
  });

  it("shows the empty state and the load error with a retry", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockOkSources();
    const { unmount } = renderPanel();
    expect(await screen.findByText("该用户暂无仓库授权")).toBeInTheDocument();
    unmount();

    mockListRepositoryGrants.mockResolvedValue({
      error: new Error("boom"),
    } as never);
    renderPanel();
    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /重\s*试/ })).toBeInTheDocument();
  });

  it("adds a grant through the single-grant upsert, without the repository's set", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [{ principal: "user:bob", scopes: ["repositories:write"] }],
    } as never);
    mockUpsertGrant.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByText("artifacts · raw"));
    expect(await screen.findByText("读取 · 浏览 / 拉取")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockUpsertGrant).toHaveBeenCalledTimes(1);
    });
    expect(mockUpsertGrant).toHaveBeenCalledWith({
      path: { repositoryId: artifactsId },
      body: {
        principal,
        scopes: ["repositories:read"],
        resourcePrefix: undefined,
      },
    });
    // The repository's other grants are never read back into a submission.
    expect(mockDeleteGrant).not.toHaveBeenCalled();
    expect(await screen.findByText("仓库授权已保存")).toBeInTheDocument();
  });

  it("prefills the acted-on row and updates it in place when the prefix is unchanged", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({ data: [aliceReleases] } as never);
    mockUpsertGrant.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑该条授权" }));

    expect(await screen.findByText("写入 · 发布 / 编辑")).toBeInTheDocument();
    expect(screen.getByDisplayValue("releases/")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockUpsertGrant).toHaveBeenCalledWith({
        path: { repositoryId: artifactsId },
        body: {
          principal,
          scopes: ["repositories:write"],
          resourcePrefix: "releases/",
        },
      });
    });
    expect(mockDeleteGrant).not.toHaveBeenCalled();
  });

  it("upserts the new prefix and deletes the old row when an edit moves the entry", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots],
    } as never);
    mockUpsertGrant.mockResolvedValue({ data: [] } as never);
    mockDeleteGrant.mockResolvedValue({} as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "编辑该条授权" })[0],
    );
    const prefixInput = await screen.findByDisplayValue("releases/");
    // One change event replaces the whole prefix; clearing first would unmount
    // the prefix fields while the value is empty.
    fireEvent.change(prefixInput, { target: { value: "stable/" } });
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    // The old row goes first, or the edit would fork one grant into two and a
    // failure would leave the grant wider than asked for; the account's
    // snapshots/ entry is never part of either call.
    await waitFor(() => {
      expect(mockDeleteGrant).toHaveBeenCalledWith({
        path: { repositoryId: artifactsId },
        query: { principal, resourcePrefix: "releases/" },
      });
    });
    await waitFor(() => {
      expect(mockUpsertGrant).toHaveBeenCalledWith({
        path: { repositoryId: artifactsId },
        body: {
          principal,
          scopes: ["repositories:write"],
          resourcePrefix: "stable/",
        },
      });
    });
    expect(mockDeleteGrant.mock.invocationCallOrder[0]).toBeLessThan(
      mockUpsertGrant.mock.invocationCallOrder[0],
    );
  });

  it("does not write the new prefix when removing the old one fails", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({ data: [aliceReleases] } as never);
    mockDeleteGrant.mockResolvedValue({
      error: { status: 500, message: "delete failed" },
    } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑该条授权" }));
    const prefixInput = await screen.findByDisplayValue("releases/");
    fireEvent.change(prefixInput, { target: { value: "stable/" } });
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    expect(
      await screen.findByText(
        "原资源范围的授权删除失败，新授权未保存，请重试。",
      ),
    ).toBeInTheDocument();
    expect(mockUpsertGrant).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("keeps the dialog open with the error when the upsert fails", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({ data: [] } as never);
    mockUpsertGrant.mockResolvedValue({
      error: { status: 500, message: "upsert failed" },
    } as never);

    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByText("artifacts · raw"));
    await screen.findByText("读取 · 浏览 / 拉取");
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    expect(await screen.findByText("upsert failed")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("removes every entry of the account on that repository, one delete each", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots, bobAdmin],
    } as never);
    mockDeleteGrant.mockResolvedValue({} as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "移除仓库授权" })[0],
    );
    await user.click(await screen.findByRole("button", { name: /^移\s*除$/ }));

    await waitFor(() => {
      expect(mockDeleteGrant).toHaveBeenCalledTimes(2);
    });
    expect(mockDeleteGrant).toHaveBeenNthCalledWith(1, {
      path: { repositoryId: artifactsId },
      query: { principal, resourcePrefix: "releases/" },
    });
    expect(mockDeleteGrant).toHaveBeenNthCalledWith(2, {
      path: { repositoryId: artifactsId },
      query: { principal, resourcePrefix: "snapshots/" },
    });
    // The other principal's entry is never deleted or resubmitted.
    expect(mockUpsertGrant).not.toHaveBeenCalled();
    expect(await screen.findByText("仓库授权已移除")).toBeInTheDocument();
  });

  it("tolerates rows a concurrent removal already deleted", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots],
    } as never);
    mockDeleteGrant
      .mockResolvedValueOnce({
        error: { status: 404, code: "not_found", message: "gone" },
      } as never)
      .mockResolvedValueOnce({} as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "移除仓库授权" })[0],
    );
    await user.click(await screen.findByRole("button", { name: /^移\s*除$/ }));

    // The already-gone row is skipped and the remaining row is still removed.
    await waitFor(() => {
      expect(mockDeleteGrant).toHaveBeenCalledTimes(2);
    });
    expect(await screen.findByText("仓库授权已移除")).toBeInTheDocument();
  });
});
