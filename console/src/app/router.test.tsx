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
});
