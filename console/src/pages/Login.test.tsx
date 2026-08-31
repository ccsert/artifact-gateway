import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { LoginPage } from "./Login";

const auth = vi.hoisted(() => ({
  authenticated: false,
  identityLoading: false,
  setToken: vi.fn(),
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    authenticated: auth.authenticated,
    identityLoading: auth.identityLoading,
    setToken: auth.setToken,
  }),
}));

function LocationProbe() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

function renderPage() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <MemoryRouter initialEntries={["/login"]}>
          <LoginPage />
        </MemoryRouter>
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  auth.setToken.mockReset();
  vi.unstubAllGlobals();
});

beforeEach(() => {
  Object.assign(auth, {
    authenticated: false,
    identityLoading: false,
  });
});

describe("LoginPage forced password change", () => {
  it("mounts the source-derived Beams background behind the whole sign-in card", () => {
    renderPage();

    const beams = document.querySelector(
      '[data-kokonutui-component="beams-background"]',
    );
    expect(beams).toBeInTheDocument();
    expect(beams?.parentElement).toHaveClass("ag-login-shell");
    expect(beams?.nextElementSibling).toHaveClass("ag-login-toolbar");
    expect(
      document.querySelector(".ag-login-brand-panel")?.contains(beams),
    ).toBe(false);
    expect(beams).toHaveAttribute("data-color-mode", "dark");
  });

  it("skips the login form when the current browser session is already authenticated", async () => {
    Object.assign(auth, { authenticated: true });

    render(
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter initialEntries={["/login?redirect=%2Frepositories"]}>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/repositories" element={<LocationProbe />} />
            </Routes>
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>,
    );

    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/repositories",
    );
    expect(
      screen.queryByRole("heading", { name: "登录控制台" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the restricted token in memory, changes the password, and signs in again", async () => {
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const target = String(input);
        if (target === "/auth/oidc/config") {
          return new Response(JSON.stringify({ enabled: false }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        if (target === "/auth/change-password") {
          return new Response(null, { status: 204 });
        }
        if (target === "/auth/login") {
          const body = JSON.parse(String(init?.body)) as { password: string };
          return new Response(
            JSON.stringify(
              body.password === "temporary-password"
                ? {
                    token: "restricted-token",
                    role: "reader",
                    mustChangePassword: true,
                  }
                : {
                    token: "final-token",
                    role: "reader",
                    mustChangePassword: false,
                  },
            ),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        throw new Error(`unexpected fetch: ${target}`);
      },
    );
    vi.stubGlobal("fetch", fetchMock);

    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("用户名"), "alice");
    await user.type(screen.getByLabelText("密码"), "temporary-password");
    await user.click(screen.getByRole("button", { name: /登录/ }));

    expect(await screen.findByText("更新初始密码")).toBeInTheDocument();
    expect(auth.setToken).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText("新密码"), "personal-password");
    await user.type(screen.getByLabelText("确认新密码"), "personal-password");
    await user.click(screen.getByRole("button", { name: /更新密码并继续/ }));

    expect(auth.setToken).toHaveBeenCalledWith("final-token", "reader");
    expect(fetchMock).toHaveBeenCalledWith("/auth/change-password", {
      method: "POST",
      headers: {
        Authorization: "Bearer restricted-token",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        currentPassword: "temporary-password",
        newPassword: "personal-password",
      }),
    });
  });
});
