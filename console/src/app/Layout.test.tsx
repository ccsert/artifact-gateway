import { cleanup, render, screen } from "@testing-library/react";
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
  return <div data-testid="location">{useLocation().pathname}</div>;
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
});

describe("AppLayout", () => {
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
    expect(await screen.findByTestId("location")).toHaveTextContent("/search");

    await user.click(screen.getByRole("button", { name: "收起导航" }));
    expect(window.localStorage.getItem("ag:sider-collapsed")).toBe("1");

    await user.click(screen.getByRole("button", { name: /退出/ }));
    expect(auth.clearToken).toHaveBeenCalledOnce();
  });
});
