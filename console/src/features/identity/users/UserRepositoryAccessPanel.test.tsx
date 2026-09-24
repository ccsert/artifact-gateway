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
  listAuthorizationRoles,
  listGrants,
  listRepositories,
  listRepositoryGrants,
  replaceGrants,
} from "../../../client";
import type { Grant } from "../../../client";
import { AntdProvider } from "../../../app/AntdProvider";
import { PreferencesProvider } from "../../../lib/preferences";
import { UserRepositoryAccessPanel } from "./UserRepositoryAccessPanel";

vi.mock("../../../client", () => ({
  listAuthorizationRoles: vi.fn(),
  listGrants: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryGrants: vi.fn(),
  replaceGrants: vi.fn(),
}));

const mockListRepositoryGrants = vi.mocked(listRepositoryGrants);
const mockListRepositories = vi.mocked(listRepositories);
const mockListAuthorizationRoles = vi.mocked(listAuthorizationRoles);
const mockListGrants = vi.mocked(listGrants);
const mockReplaceGrants = vi.mocked(replaceGrants);

const userId = "00000000-0000-0000-0000-000000000001";
const username = "alice";
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
  principal: `user:${username}`,
  scopes: ["repositories:write"],
  resourcePrefix: "releases/",
};

const aliceSnapshots = {
  principal: `user:${username}`,
  scopes: ["repositories:read"],
  resourcePrefix: "snapshots/",
};

function versionResponse(etag: string) {
  return { headers: { get: () => etag } };
}

function record(overrides: Record<string, unknown> = {}) {
  return {
    repositoryId: artifactsId,
    repositoryName: "artifacts",
    format: "raw",
    principal: `user:${username}`,
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

function submittedBody(callIndex = 0): Grant[] {
  return mockReplaceGrants.mock.calls[callIndex][0].body;
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

  it("adds a grant by merging into the repository's current set", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [{ principal: "user:bob", scopes: ["repositories:write"] }],
      response: versionResponse('"7"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByText("artifacts · raw"));
    expect(await screen.findByText("读取 · 浏览 / 拉取")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(1);
    });
    expect(mockReplaceGrants).toHaveBeenCalledWith({
      path: { repositoryId: artifactsId },
      body: [
        { principal: "user:bob", scopes: ["repositories:write"] },
        expect.objectContaining({
          principal: `user:${username}`,
          scopes: ["repositories:read"],
        }),
      ],
      headers: { "If-Match": "7" },
    });
    expect(await screen.findByText("仓库授权已保存")).toBeInTheDocument();
  });

  it("prefills the acted-on row when editing it and replaces it in place", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases],
      response: versionResponse('"4"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑该条授权" }));

    expect(await screen.findByText("写入 · 发布 / 编辑")).toBeInTheDocument();
    expect(screen.getByDisplayValue("releases/")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledWith({
        path: { repositoryId: artifactsId },
        body: [
          expect.objectContaining({
            principal: `user:${username}`,
            scopes: ["repositories:write"],
            resourcePrefix: "releases/",
          }),
        ],
        headers: { "If-Match": "4" },
      });
    });
  });

  it("edits one entry and submits the account's other prefixes untouched", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots],
      response: versionResponse('"5"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    // The first row stands for the releases/ entry.
    await user.click(
      screen.getAllByRole("button", { name: "编辑该条授权" })[0],
    );
    expect(await screen.findByText("写入 · 发布 / 编辑")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(1);
    });
    const body = submittedBody();
    expect(body).toHaveLength(2);
    expect(body.map((grant) => grant.resourcePrefix)).toEqual([
      "releases/",
      "snapshots/",
    ]);
    // Byte-identical: the account's other prefix is resubmitted as the very
    // same object, so editing one entry cannot silently drop the others.
    expect(body[1]).toBe(aliceSnapshots);
  });

  it("moves the acted-on entry when the edit changes its prefix", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots],
      response: versionResponse('"5"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

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

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(1);
    });
    const body = submittedBody();
    // The edited entry moved to stable/ in the slot it occupied; nothing was
    // left behind under the old prefix and the account's other entry is still
    // there untouched.
    expect(body.map((grant) => grant.resourcePrefix)).toEqual([
      "stable/",
      "snapshots/",
    ]);
    expect(body[1]).toBe(aliceSnapshots);
  });

  it("refreshes the version after a 412 and lets the user save again", async () => {
    mockListRepositoryGrants.mockResolvedValue({ data: [] } as never);
    mockOkSources();
    mockListGrants
      .mockResolvedValueOnce({
        data: [],
        response: versionResponse('"7"'),
      } as never)
      .mockResolvedValueOnce({
        data: [],
        response: versionResponse('"8"'),
      } as never);
    mockReplaceGrants
      .mockResolvedValueOnce({
        error: { status: 412, code: "version_conflict", message: "conflict" },
      } as never)
      .mockResolvedValueOnce({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /添加授权/ }));
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByText("artifacts · raw"));
    await screen.findByText("读取 · 浏览 / 拉取");

    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));
    expect(await screen.findByText("授权版本冲突")).toBeInTheDocument();
    await waitFor(() => {
      expect(mockListGrants).toHaveBeenCalledTimes(2);
    });

    // The composed grant is still there, so the second save retries against
    // the refreshed version instead of failing silently.
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));
    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(2);
    });
    expect(mockReplaceGrants).toHaveBeenLastCalledWith(
      expect.objectContaining({ headers: { "If-Match": "8" } }),
    );
  });

  it("keeps the acted-on entry after a 412 refresh and merges into the new set", async () => {
    const bobInSet = {
      principal: "user:bob",
      scopes: ["repositories:admin"],
    };
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants
      .mockResolvedValueOnce({
        data: [aliceReleases, aliceSnapshots],
        response: versionResponse('"7"'),
      } as never)
      .mockResolvedValueOnce({
        data: [aliceReleases, aliceSnapshots, bobInSet],
        response: versionResponse('"8"'),
      } as never);
    mockReplaceGrants
      .mockResolvedValueOnce({
        error: { status: 412, code: "version_conflict", message: "conflict" },
      } as never)
      .mockResolvedValueOnce({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "编辑该条授权" })[0],
    );
    expect(await screen.findByText("写入 · 发布 / 编辑")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));
    expect(await screen.findByText("授权版本冲突")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^保\s*存$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(2);
    });
    const retry = mockReplaceGrants.mock.calls[1][0];
    expect(retry.headers).toEqual({ "If-Match": "8" });
    // Still the same acted-on entry, merged into the refreshed set: the other
    // entries of this account and the concurrent one survive untouched.
    expect(retry.body).toHaveLength(3);
    expect(retry.body[0]).toEqual({
      principal: `user:${username}`,
      scopes: ["repositories:write"],
      resourcePrefix: "releases/",
    });
    expect(retry.body[1]).toBe(aliceSnapshots);
    expect(retry.body[2]).toBe(bobInSet);
  });

  it("removes a grant by merging the rest of the repository's set", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record(),
        record({
          repositoryId: iconsId,
          repositoryName: "icons",
          format: "oci",
          scopes: ["repositories:write"],
        }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [
        { principal: `user:${username}`, scopes: ["repositories:read"] },
        bobAdmin,
      ],
      response: versionResponse('"9"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("artifacts")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "移除仓库授权" })[0],
    );
    await user.click(await screen.findByRole("button", { name: /^移\s*除$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(1);
    });
    expect(mockReplaceGrants).toHaveBeenCalledWith({
      path: { repositoryId: artifactsId },
      body: [bobAdmin],
      headers: { "If-Match": "9" },
    });
  });

  it("removal drops every entry of the account on that repository and nothing else", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [
        record({ scopes: ["repositories:write"], resourcePrefix: "releases/" }),
        record({ resourcePrefix: "snapshots/" }),
      ],
    } as never);
    mockOkSources();
    mockListGrants.mockResolvedValue({
      data: [aliceReleases, aliceSnapshots, bobAdmin],
      response: versionResponse('"11"'),
    } as never);
    mockReplaceGrants.mockResolvedValue({ data: [] } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("releases/")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "移除仓库授权" })[0],
    );
    await user.click(await screen.findByRole("button", { name: /^移\s*除$/ }));

    await waitFor(() => {
      expect(mockReplaceGrants).toHaveBeenCalledTimes(1);
    });
    // The action is explicitly bulk for this repository: both prefixes go,
    // and the other principal's entry is resubmitted as the very same object.
    const body = submittedBody();
    expect(body).toHaveLength(1);
    expect(body[0]).toBe(bobAdmin);
  });

  it("tells the user when a concurrent removal already happened", async () => {
    mockListRepositoryGrants.mockResolvedValue({
      data: [record()],
    } as never);
    mockOkSources();
    mockListGrants
      .mockResolvedValueOnce({
        data: [
          { principal: `user:${username}`, scopes: ["repositories:read"] },
        ],
        response: versionResponse('"9"'),
      } as never)
      .mockResolvedValueOnce({
        data: [{ principal: "user:bob", scopes: ["repositories:admin"] }],
        response: versionResponse('"10"'),
      } as never);
    mockReplaceGrants.mockResolvedValueOnce({
      error: { status: 412, code: "version_conflict", message: "conflict" },
    } as never);

    const user = userEvent.setup();
    renderPanel();

    expect(await screen.findByText("artifacts")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "移除仓库授权" }));
    await user.click(await screen.findByRole("button", { name: /^移\s*除$/ }));

    expect(
      await screen.findByText("该授权已被其他人移除，列表已刷新。"),
    ).toBeInTheDocument();
  });
});
