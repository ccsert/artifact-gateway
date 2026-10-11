import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getAnonymousAccessPolicy,
  listApiKeys,
  listAuthorizationRoles,
  listAuthorizationTemplates,
  listRepositories,
  listRepositoryGrants,
  listServiceAccounts,
  listUsers,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { accessKeys } from "../../lib/query/keys";
import { AccessControlPage } from "./AccessControl";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  getAnonymousAccessPolicy: vi.fn(),
  listApiKeys: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  listAuthorizationTemplates: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryGrants: vi.fn(),
  listServiceAccounts: vi.fn(),
  listUsers: vi.fn(),
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    identity: { actor: "user:synthetic", role: "admin", administrator: true },
    identityLoading: false,
  }),
}));

const repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "synthetic-recovery",
  state: "active",
  format: "raw",
  type: "hosted",
};
const grant = {
  repositoryId: repository.id,
  repositoryName: repository.name,
  format: "raw",
  principal: "user:synthetic",
  scopes: ["repositories:read"],
  resourcePrefix: "",
};
const grants = vi.mocked(listRepositoryGrants);
const repositories = vi.mocked(listRepositories);
const clients: QueryClient[] = [];

function access(url = "/access") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  clients.push(client);
  const router = createMemoryRouter(
    [
      {
        path: "/access",
        element: <AccessControlPage />,
        errorElement: <div data-testid="route-error">Route failed</div>,
      },
    ],
    { initialEntries: [url] },
  );
  render(
    <QueryClientProvider client={client}>
      <PreferencesProvider>
        <AntdProvider>
          <RouterProvider router={router} />
        </AntdProvider>
      </PreferencesProvider>
    </QueryClientProvider>,
  );
  return client;
}

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  grants.mockResolvedValue({ data: [grant] } as never);
  repositories.mockResolvedValue({ data: { items: [repository] } } as never);
  vi.mocked(listUsers).mockResolvedValue({ data: { items: [] } } as never);
  vi.mocked(listApiKeys).mockResolvedValue({ data: { items: [] } } as never);
  vi.mocked(listServiceAccounts).mockResolvedValue({
    data: { items: [] },
  } as never);
  vi.mocked(getAnonymousAccessPolicy).mockResolvedValue({
    data: { enabled: false, version: "1" },
  } as never);
  vi.mocked(listAuthorizationRoles).mockResolvedValue({ data: [] } as never);
  vi.mocked(listAuthorizationTemplates).mockResolvedValue({
    data: [],
  } as never);
});
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.restoreAllMocks();
  vi.resetAllMocks();
});

describe("access control read recovery", () => {
  it.each(["early", "late"])(
    "waits for every directory source when a Retry fails %s",
    async (failureOrder) => {
      vi.mocked(listApiKeys).mockResolvedValue({
        data: {
          items: [{ id: "synthetic-key", name: "Synthetic key", roles: null }],
        },
      } as never);
      access("/access?tab=evaluate");
      expect(await screen.findByText("此区域暂时无法显示")).toBeVisible();
      const errorsBefore = vi.mocked(console.error).mock.calls.length;
      let finish: (value: unknown) => void = () => {};
      repositories.mockImplementation(
        () =>
          new Promise((resolve, reject) => {
            finish = failureOrder === "early" ? resolve : reject;
          }) as never,
      );
      vi.mocked(listApiKeys).mockImplementation(() => {
        if (failureOrder === "early")
          return Promise.reject(
            new Error("Synthetic key refresh failed"),
          ) as never;
        return Promise.resolve({
          data: {
            items: [
              { id: "synthetic-key", name: "Synthetic key", roles: ["member"] },
            ],
          },
        }) as never;
      });
      fireEvent.click(screen.getByRole("button", { name: /重试/ }));
      await waitFor(() => expect(repositories).toHaveBeenCalledTimes(2));
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 25));
      });
      expect(screen.getByText("此区域暂时无法显示")).toBeVisible();
      expect(vi.mocked(console.error).mock.calls.length).toBe(errorsBefore);
      await act(async () => {
        finish(
          failureOrder === "early"
            ? { data: { items: [repository] } }
            : new Error("Synthetic directory refresh failed"),
        );
      });
      expect(screen.getByRole("tab", { name: "策略与模板" })).toBeVisible();
      expect(screen.queryByTestId("route-error")).not.toBeInTheDocument();
    },
  );

  it("keeps cached grant rows and a Retry action after a failed refresh", async () => {
    const client = access();
    expect(
      await screen.findByRole("link", { name: repository.name }),
    ).toBeVisible();
    grants.mockRejectedValue(new Error("Synthetic refresh failed"));
    await act(() =>
      client.invalidateQueries({ queryKey: accessKeys.grants() }),
    );
    expect(await screen.findByText("Synthetic refresh failed")).toBeVisible();
    expect(screen.getByRole("link", { name: repository.name })).toBeVisible();
    grants.mockResolvedValue({ data: [grant] } as never);
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    await waitFor(() =>
      expect(
        screen.queryByText("Synthetic refresh failed"),
      ).not.toBeInTheDocument(),
    );
    expect(grants).toHaveBeenCalledTimes(3);
  });

  it("refetches a corrected answer when a failed grant section is retried", async () => {
    grants.mockResolvedValue({
      data: [{ ...grant, resourcePrefix: { malformed: true } }],
    } as never);
    access();
    expect(await screen.findByText("此区域暂时无法显示")).toBeVisible();
    expect(screen.getByRole("tab", { name: "权限检查" })).toBeVisible();
    grants.mockResolvedValue({ data: [grant] } as never);
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(
      await screen.findByRole("link", { name: repository.name }),
    ).toBeVisible();
    expect(grants).toHaveBeenCalledTimes(2);
    expect(screen.queryByTestId("route-error")).not.toBeInTheDocument();
  });

  it("contains malformed grant metrics while policy navigation remains usable", async () => {
    grants.mockResolvedValue({ data: [{ ...grant, scopes: null }] } as never);
    access("/access?tab=policies");
    expect(await screen.findByText("公开访问边界")).toBeVisible();
    expect(await screen.findByText("此区域暂时无法显示")).toBeVisible();
    expect(screen.queryByTestId("route-error")).not.toBeInTheDocument();
  });

  it("contains a malformed repository directory and retries its source", async () => {
    repositories.mockResolvedValue({ data: { items: [null] } } as never);
    access("/access?tab=evaluate");
    expect(
      await screen.findByRole("combobox", { name: "授权主体" }),
    ).toBeVisible();
    expect(screen.getByRole("tab", { name: "策略与模板" })).toBeVisible();
    expect(screen.queryByTestId("route-error")).not.toBeInTheDocument();
    repositories.mockResolvedValue({ data: { items: [repository] } } as never);
    fireEvent.click(await screen.findByRole("button", { name: /重试/ }));
    await waitFor(() => expect(repositories).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /重试/ }),
      ).not.toBeInTheDocument(),
    );
  });
});
