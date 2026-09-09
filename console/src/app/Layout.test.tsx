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
          <Route path="/service-accounts" element={<AppLayout />}>
            <Route index element={<div>service account management</div>} />
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
  window.localStorage.clear();
});

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

  it("keeps administrator-only routes away from a reader", async () => {
    Object.assign(auth, {
      role: "reader",
      identity: { administrator: false },
    });

    renderLayout("/repositories");

    expect(await screen.findByTestId("location")).toHaveTextContent("/search");
    expect(screen.queryByText("repository catalog")).not.toBeInTheDocument();
  });

  it("keeps Service Account credential management away from a reader", async () => {
    Object.assign(auth, {
      role: "reader",
      identity: { administrator: false },
    });

    renderLayout("/service-accounts");

    expect(await screen.findByTestId("location")).toHaveTextContent("/search");
    expect(
      screen.queryByText("service account management"),
    ).not.toBeInTheDocument();
  });

  it("provides global search, persistent navigation collapse, and logout", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    expect(await screen.findByText("repository catalog")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /仓库/ })).toBeInTheDocument();

    const search = screen.getByPlaceholderText("跨仓库搜索制品…");
    await user.type(search, " release/widget ");
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
      within(desktopSider!).getByText("Native Hosted API v2"),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /退出/ }));
    expect(auth.clearToken).toHaveBeenCalledOnce();
  });

  it("discards token edits on cancel and clears credentials only on request", async () => {
    const user = userEvent.setup();
    renderLayout("/repositories");

    await user.click(screen.getByRole("button", { name: "已配置 Token" }));
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

    await user.click(screen.getByRole("button", { name: "已配置 Token" }));
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
