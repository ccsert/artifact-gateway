import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RouteErrorPage } from "./RouteErrorPage";
import { PreferencesProvider } from "../lib/preferences";

const route = vi.hoisted(() => ({
  error: new TypeError(
    "Failed to fetch dynamically imported module: http://localhost:4173/src/app/Layout.tsx",
  ) as unknown,
}));

vi.mock("react-router-dom", async () => {
  const actual =
    await vi.importActual<typeof import("react-router-dom")>(
      "react-router-dom",
    );
  return { ...actual, useRouteError: () => route.error };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
  route.error = new TypeError(
    "Failed to fetch dynamically imported module: http://localhost:4173/src/app/Layout.tsx",
  );
});

describe("RouteErrorPage", () => {
  it("replaces the developer exception page for a failed lazy module", async () => {
    render(
      <PreferencesProvider>
        <RouteErrorPage />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByRole("heading", { name: "页面资源加载失败" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/页面资源可能已更新，或开发服务正在重建依赖/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "重新加载" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Unexpected Application Error!"),
    ).not.toBeInTheDocument();
  });

  it("presents a regular runtime failure with theme-safe recovery actions", async () => {
    localStorage.setItem("ag.console.theme", "light");
    route.error = new TypeError(
      "Cannot read properties of null (reading 'useContext')",
    );

    render(
      <PreferencesProvider>
        <RouteErrorPage />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByRole("heading", { name: "页面加载失败" }),
    ).toBeInTheDocument();
    expect(screen.getByText("技术详情")).toBeInTheDocument();
    expect(
      screen.getByText("Cannot read properties of null (reading 'useContext')"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重新加载" })).toHaveClass(
      "ag-route-error-primary",
    );
    expect(screen.getByRole("link", { name: "浏览公开制品" })).toHaveClass(
      "ag-route-error-secondary",
    );
  });
});
