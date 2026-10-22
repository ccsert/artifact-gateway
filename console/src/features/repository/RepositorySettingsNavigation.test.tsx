import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import type { Repository } from "../../client";
import { updateRepository } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { repositoryKeys } from "../../lib/query/keys";
import { RepositoryDetailPage } from "./RepositoryDetail";

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ identity: { administrator: true, role: "admin" } }),
}));
vi.mock("../../client", async () => ({
  ...(await vi.importActual<typeof import("../../client")>("../../client")),
  updateRepository: vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  localStorage.clear();
});

it.each(["go", "maven", "oci"] as const)(
  "drops the %s settings draft and all secrets on cached repository navigation",
  async (format) => {
    localStorage.setItem("ag.console.locale", "en-US");
    const user = userEvent.setup();
    const first: Repository = {
      id: "11111111-1111-4111-8111-111111111111",
      name: "first-proxy",
      format,
      type: "proxy",
      state: "active",
      version: "1",
      anonymousRead: false,
      mavenStrictPublication: false,
      endpoint: "https://first.example.test",
      allowedHosts: ["first.example.test"],
      upstreamAuth: { scheme: "bearer", credentialsConfigured: true },
      ...(format === "oci"
        ? {
            ociBearer: {
              realm: "https://issuer.example.test/token",
              service: "first-service",
            },
          }
        : {}),
      egressProxy: {
        mode: "custom",
        protocol: "http",
        host: "proxy.example.test",
        port: 3128,
      },
    };
    const second: Repository = {
      ...first,
      id: "22222222-2222-4222-8222-222222222222",
      name: "second-proxy",
      version: "9",
      endpoint: "https://second.example.test",
      allowedHosts: ["second.example.test"],
      ociBearer: null,
      upstreamAuth: { scheme: "bearer", credentialsConfigured: true },
    };
    const client = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity, retry: false } },
    });
    for (const repo of [first, second]) {
      client.setQueryData(repositoryKeys.detail(repo.id), repo);
      client.setQueryData(repositoryKeys.capabilities(repo.id), {
        format,
        type: "proxy",
        operations: ["read"],
      });
      client.setQueryData(repositoryKeys.effectiveAccess(repo.id), {});
      client.setQueryData(repositoryKeys.capacity(repo.id), {
        usedBytes: 0,
        quotaBytes: 0,
        objectCount: 0,
      });
    }
    vi.mocked(updateRepository).mockResolvedValue({
      error: { status: 503 },
    } as never);
    render(
      <QueryClientProvider client={client}>
        <PreferencesProvider>
          <AntdProvider>
            <MemoryRouter
              initialEntries={[`/repositories/${first.id}?tab=settings`]}
            >
              <Link to={`/repositories/${second.id}?tab=settings`}>
                Switch to cached repository
              </Link>
              <Routes>
                <Route
                  path="/repositories/:repositoryId"
                  element={<RepositoryDetailPage />}
                />
              </Routes>
            </MemoryRouter>
          </AntdProvider>
        </PreferencesProvider>
      </QueryClientProvider>,
    );
    const upstreamToken = await screen.findByLabelText("Upstream token");
    const proxyPassword = screen.getByLabelText("Proxy password (optional)");
    // Paste complete credentials without rendering the form for each keystroke.
    await user.click(upstreamToken);
    await user.paste("owned-first-upstream-secret");
    await user.click(proxyPassword);
    await user.paste("owned-first-proxy-secret");
    expect(upstreamToken).toHaveValue("owned-first-upstream-secret");
    expect(proxyPassword).toHaveValue("owned-first-proxy-secret");
    expect(updateRepository).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("link", { name: "Switch to cached repository" }),
    );
    await screen.findByRole("heading", { name: "second-proxy" });
    expect
      .soft(screen.getByLabelText("Upstream URL"))
      .toHaveValue(second.endpoint);
    expect.soft(screen.getByLabelText("Upstream token")).toHaveValue("");
    expect
      .soft(screen.getByLabelText("Proxy password (optional)"))
      .toHaveValue("");
    if (format === "oci")
      expect
        .soft(screen.getByRole("switch", { name: "OCI token exchange" }))
        .not.toBeChecked();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(updateRepository).toHaveBeenCalledOnce());
    const request = vi.mocked(updateRepository).mock.calls[0][0];
    expect(request.path.repositoryId).toBe(second.id);
    expect(request.headers).toEqual({ "If-Match": "9" });
    expect.soft(request.body).toMatchObject({
      endpoint: second.endpoint,
      allowedHosts: second.allowedHosts,
    });
    expect(JSON.stringify(request)).not.toContain("owned-first-");
  },
);
