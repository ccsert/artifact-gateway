import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { createQueryClient } from "../../lib/query/client";
import { PublicBrowsePage } from "./PublicBrowsePage";

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ authenticated: false, identityLoading: false }),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("public catalog with the production query policy", () => {
  it.each([401, 403, 404])(
    "shows HTTP %i once without ending a management session",
    async (status) => {
      const fetch = vi
        .spyOn(globalThis, "fetch")
        .mockImplementation(async () => new Response("refused", { status }));
      const onAuthFailure = vi.fn();
      const client = createQueryClient(onAuthFailure);
      try {
        render(
          <MemoryRouter initialEntries={["/browse"]}>
            <QueryClientProvider client={client}>
              <PreferencesProvider>
                <PublicBrowsePage />
              </PreferencesProvider>
            </QueryClientProvider>
          </MemoryRouter>,
        );

        expect(
          await screen.findByText(`读取公开仓库失败 (${status})`, undefined, {
            timeout: 3000,
          }),
        ).toBeInTheDocument();
        expect(
          screen.getByRole("link", { name: "管理登录" }),
        ).toBeInTheDocument();
        expect(fetch).toHaveBeenCalledTimes(1);
        expect(onAuthFailure).not.toHaveBeenCalled();
      } finally {
        client.clear();
      }
    },
  );

  it("recovers a temporary server failure with the permitted retry", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response("unavailable", { status: 503 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ enabled: true, items: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    const client = createQueryClient();
    try {
      render(
        <MemoryRouter initialEntries={["/browse"]}>
          <QueryClientProvider client={client}>
            <PreferencesProvider>
              <PublicBrowsePage />
            </PreferencesProvider>
          </QueryClientProvider>
        </MemoryRouter>,
      );
      expect(
        await screen.findByText("暂无公开仓库", undefined, { timeout: 3000 }),
      ).toBeInTheDocument();
      expect(fetch).toHaveBeenCalledTimes(2);
    } finally {
      client.clear();
    }
  });
});
