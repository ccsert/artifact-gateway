import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { PreferencesProvider } from "../lib/preferences";
import { AppLayout } from "./Layout";
import { getDiagnostics, listRepositories } from "../client";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  getDiagnostics: vi.fn(),
  listRepositories: vi.fn(),
}));

const mockGetDiagnostics = vi.mocked(getDiagnostics);
const mockListRepositories = vi.mocked(listRepositories);

const auth = vi.hoisted(() => ({
  token: "operator-token",
  role: "admin",
  authenticated: true,
  identity: { administrator: true },
  identityLoading: false,
  setToken: vi.fn(),
  clearToken: vi.fn(),
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => auth,
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{location.pathname + location.search}</div>
  );
}

function renderLayout(pathname: string) {
  return render(
    <PreferencesProvider>
      <MemoryRouter initialEntries={[pathname]}>
        <Routes>
          <Route path="/browse" element={<div>public browse</div>} />
          <Route path="/login" element={<LocationProbe />} />
          <Route path="/search" element={<AppLayout />}>
            <Route index element={<LocationProbe />} />
          </Route>
          <Route path="/repositories" element={<AppLayout />}>
            <Route index element={<div>repository catalog</div>} />
          </Route>
          <Route path="/repositories/:repositoryId" element={<AppLayout />}>
            <Route index element={<div>repository detail</div>} />
          </Route>
          <Route path="/groups/:groupId/browse" element={<AppLayout />}>
            <Route index element={<div>group browse</div>} />
          </Route>
          <Route path="/users" element={<AppLayout />}>
            <Route index element={<div>user management</div>} />
            <Route path=":userId" element={<div>user detail</div>} />
          </Route>
          <Route path="/service-accounts" element={<AppLayout />}>
            <Route index element={<div>service account management</div>} />
          </Route>
          <Route path="/operations" element={<AppLayout />}>
            <Route index element={<LocationProbe />} />
          </Route>
          <Route path="/system" element={<AppLayout />}>
            <Route index element={<LocationProbe />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </PreferencesProvider>,
  );
}

beforeEach(() => {
  Object.assign(auth, {
    token: "operator-token",
    role: "admin",
    authenticated: true,
    identity: { administrator: true },
    identityLoading: false,
  });
  auth.setToken.mockReset();
  auth.clearToken.mockReset();
  mockGetDiagnostics.mockReset();
  mockGetDiagnostics.mockResolvedValue({
    data: { build: { version: "v0.4.3", revision: "abc123" } },
  } as never);
  mockListRepositories.mockReset();
  mockListRepositories.mockResolvedValue({
    data: {
      items: [
        {
          id: "repo-1",
          name: "release-files",
          format: "raw",
          type: "hosted",
          anonymousRead: false,
          mavenStrictPublication: false,
          state: "active",
          version: "1",
        },
      ],
    },
  } as never);
  window.localStorage.clear();
});

function sider() {
  return document.querySelector<HTMLElement>(".ag-sider-desktop")!;
}

async function openAccountMenu(user: ReturnType<typeof userEvent.setup>) {
  await user.click(within(sider()).getByRole("button", { name: /^账户菜单/ }));
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("AppLayout", () => {
  it("waits for identity before rendering protected navigation", async () => {
    auth.identityLoading = true;

    renderLayout("/repositories");

    expect(await screen.findByText("正在加载…")).toBeInTheDocument();
    expect(screen.queryByText("repository catalog")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /仓库/ }),
    ).not.toBeInTheDocument();
  });

  it("preserves the protected destination when redirecting to login", async () => {
    Object.assign(auth, { authenticated: false, token: "", identity: null });

    renderLayout("/repositories?format=apt");

    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/login?redirect=%2Frepositories%3Fformat%3Dapt",
    );
    expect(screen.queryByText("repository catalog")).not.toBeInTheDocument();
  });

  it("redirects an unauthenticated public search to browse", async () => {
    Object.assign(auth, {
      token: "",
      role: "",
      authenticated: false,
      identity: null,
    });

    renderLayout("/search");

    expect(await screen.findByText("public browse")).toBeInTheDocument();
  });

  it("shows a member the repository catalog without administrator navigation", async () => {
    Object.assign(auth, {
      role: "member",
      identity: { administrator: false, role: "member" },
    });

    renderLayout("/repositories");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /仓库/ })).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /用户/ }),
    ).not.toBeInTheDocument();
  });

  it("keeps an identity that cannot read repositories out of the catalog", async () => {
    Object.assign(auth, {
      role: "",
      identity: { administrator: false },
    });

    renderLayout("/repositories/");

    expect(await screen.findByTestId("location")).toHaveTextContent("/search");
    expect(screen.queryByText("repository catalog")).not.toBeInTheDocument();
  });

  it("keeps Service Account credential management away from a member", async () => {
    Object.assign(auth, {
      role: "member",
      identity: { administrator: false, role: "member" },
    });

    renderLayout("/service-accounts");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(
      screen.queryByText("service account management"),
    ).not.toBeInTheDocument();
  });

  it("keeps a member on a repository deep link without administrator navigation", async () => {
    Object.assign(auth, {
      role: "member",
      identity: { administrator: false, role: "member" },
    });

    // The detail route is not an exact member of the administrator-only list,
    // so the catalog gate and the page's own capability checks decide it.
    renderLayout("/repositories/11111111-1111-4111-8111-111111111111");

    expect(await screen.findByText("repository detail")).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /用户/ }),
    ).not.toBeInTheDocument();
  });

  it("redirects a member out of an administrator-only section by prefix", async () => {
    Object.assign(auth, {
      role: "member",
      identity: { administrator: false, role: "member" },
    });

    renderLayout("/users/11111111-1111-4111-8111-111111111111");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(screen.queryByText("user detail")).not.toBeInTheDocument();
  });

  it("does not let a trailing slash reach an administrator-only section", async () => {
    Object.assign(auth, {
      role: "member",
      identity: { administrator: false, role: "member" },
    });

    renderLayout("/users/");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(screen.queryByText("user management")).not.toBeInTheDocument();
  });

  it("redirects an identity that cannot browse out of a group browse deep link", async () => {
    Object.assign(auth, { role: "", identity: { administrator: false } });

    renderLayout("/groups/11111111-1111-4111-8111-111111111111/browse");

    expect(await screen.findByTestId("location")).toHaveTextContent("/search");
    expect(screen.queryByText("group browse")).not.toBeInTheDocument();
  });

  it("shows pending SSO users an approval screen without repository navigation", async () => {
    Object.assign(auth, {
      role: "none",
      identity: { administrator: false, role: "none", kind: "oidc" },
    });

    renderLayout("/repositories");

    expect(
      await screen.findByRole("heading", { name: "等待管理员授权" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "检查授权状态" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("repository catalog")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /仓库/ }),
    ).not.toBeInTheDocument();
  });

  it("searches artifacts from the command palette, collapses navigation, and signs out", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(
      within(sider()).getByRole("link", { name: /仓库/ }),
    ).toBeInTheDocument();

    await user.click(
      within(sider()).getByRole("button", { name: "搜索或跳转…" }),
    );
    const palette = await screen.findByRole("combobox", { name: "命令面板" });
    await user.type(palette, " release/widget ");
    expect(
      screen.getByRole("option", {
        name: /在所有仓库中搜索制品“release\/widget”/,
      }),
    ).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{Enter}");
    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/search?q=release%2Fwidget",
    );

    await user.click(screen.getByRole("button", { name: "收起导航" }));
    expect(window.localStorage.getItem("ag:sider-collapsed")).toBe("1");
    const desktopSider =
      document.querySelector<HTMLElement>(".ag-sider-desktop");
    expect(desktopSider).toHaveAttribute("data-collapsed", "true");
    expect(within(desktopSider!).getByText("运行")).toBeInTheDocument();
    expect(
      within(desktopSider!).getByText("Artifact Gateway"),
    ).toBeInTheDocument();
    expect(
      within(desktopSider!).getByRole("link", {
        name: /当前节点 · v0.4.3 · abc123/,
      }),
    ).toBeInTheDocument();

    await openAccountMenu(user);
    await user.click(await screen.findByRole("menuitem", { name: /退出/ }));
    expect(auth.clearToken).toHaveBeenCalledOnce();
  });

  it("opens the command palette with ⌘K and jumps to a repository", async () => {
    const user = userEvent.setup();
    renderLayout("/search");

    await screen.findByTestId("location");
    await user.keyboard("{Meta>}k{/Meta}");
    const palette = await screen.findByRole("combobox", { name: "命令面板" });
    await user.type(palette, "release");
    const option = await screen.findByRole("option", {
      name: /release-files/,
    });
    expect(option).toHaveAttribute("aria-selected", "true");
    expect(palette).toHaveAttribute("aria-activedescendant", option.id);
    await user.keyboard("{Enter}");
    expect(await screen.findByText("repository detail")).toBeInTheDocument();
  });

  it("names the authentication method in the account menu", async () => {
    Object.assign(auth, {
      identity: {
        administrator: true,
        actor: "user:alice",
        kind: "local_session",
        role: "admin",
      },
    });
    renderLayout("/repositories");

    const account = await within(sider()).findByRole("button", {
      name: "账户菜单: user:alice",
    });
    expect(account).toHaveTextContent("管理员 · 账号登录");
    expect(within(sider()).queryByText("已配置 Token")).toBeNull();
  });

  it("opens diagnostics from the connected build version", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    const desktopSider =
      document.querySelector<HTMLElement>(".ag-sider-desktop");
    const versionLink = await within(desktopSider!).findByRole("link", {
      name: /当前节点 · v0.4.3 · abc123/,
    });
    await user.click(versionLink);
    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/system?tab=diagnostics",
    );
  });

  it("discards token edits on cancel and clears credentials only on request", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    await openAccountMenu(user);
    await user.click(
      await screen.findByRole("menuitem", { name: /已配置 Token/ }),
    );
    const dialog = await screen.findByRole("dialog", { name: "API 访问令牌" });
    const input = within(dialog).getByRole("textbox");
    expect(input).toHaveValue("operator-token");

    await user.clear(input);
    await user.type(input, "   ");
    expect(
      within(dialog).getByRole("button", { name: /^保\s*存$/ }),
    ).toBeDisabled();
    await user.type(input, "unsaved-token");
    expect(
      within(dialog).getByRole("button", { name: /^保\s*存$/ }),
    ).toBeEnabled();
    await user.click(within(dialog).getByRole("button", { name: /^取\s*消$/ }));
    await waitFor(() => expect(dialog).not.toBeVisible());
    expect(auth.setToken).not.toHaveBeenCalled();
    expect(auth.clearToken).not.toHaveBeenCalled();

    await openAccountMenu(user);
    await user.click(
      await screen.findByRole("menuitem", { name: /已配置 Token/ }),
    );
    const reopened = await screen.findByRole("dialog", {
      name: "API 访问令牌",
    });
    expect(within(reopened).getByRole("textbox")).toHaveValue("operator-token");
    await user.click(
      within(reopened).getByRole("button", { name: "清除令牌" }),
    );
    expect(auth.clearToken).toHaveBeenCalledOnce();
    expect(auth.setToken).not.toHaveBeenCalled();
    await waitFor(() => expect(reopened).not.toBeVisible());
  });

  it("keeps navigation usable when local storage is unavailable", async () => {
    const user = userEvent.setup();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("Storage disabled", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Storage disabled", "SecurityError");
    });

    renderLayout("/repositories");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "收起导航" }));
    expect(document.querySelector(".ag-sider-desktop")).toHaveAttribute(
      "data-collapsed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "展开导航" }));
    expect(document.querySelector(".ag-sider-desktop")).toHaveAttribute(
      "data-collapsed",
      "false",
    );
  });

  it("closes mobile navigation after choosing a destination", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    await user.click(screen.getByRole("button", { name: "打开导航" }));
    const drawer = await screen.findByRole("dialog");
    await user.click(within(drawer).getByRole("link", { name: /搜索/ }));

    expect(await screen.findByTestId("location")).toHaveTextContent("/search");
    await waitFor(() => expect(drawer).not.toBeVisible());
  });

  it("opens an accessible mobile navigation drawer and closes it with Escape", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    await user.click(screen.getByRole("button", { name: "打开导航" }));

    const drawer = await screen.findByRole("dialog");
    expect(
      within(drawer).getByRole("link", { name: /仓库/ }),
    ).toBeInTheDocument();
    expect(
      within(drawer).getByRole("button", { name: "关闭导航" }),
    ).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(drawer).not.toBeVisible());
  });
});
