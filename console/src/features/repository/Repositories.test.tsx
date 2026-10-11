import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import {
  createRepository,
  deleteRepository,
  listRepositories,
  listRepositoryCapacities,
  listFormatProfiles,
} from "../../client";
import type { FormatProfile } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { RepositoriesPage } from "./Repositories";

const auth = vi.hoisted(() => ({
  identity: { administrator: false, role: "member" },
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => auth,
}));

vi.mock("../../client", async () => ({
  ...(await vi.importActual<typeof import("../../client")>("../../client")),
  createRepository: vi.fn(),
  deleteRepository: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryCapacities: vi.fn(),
  listFormatProfiles: vi.fn(),
}));

const profiles: FormatProfile[] = [
  {
    format: "oci",
    repositoryTypes: ["hosted", "proxy"],
    groupSupported: true,
    anonymousRead: true,
    hostedOperations: ["read", "publish"],
    proxyOperations: ["read"],
  },
];

// The mask is the dismissal route the create dialog's `busy` flag controls,
// and it only closes on a click that both starts and lands on it.
function clickMask() {
  const mask = document.querySelector(".ant-modal-wrap");
  expect(mask).not.toBeNull();
  fireEvent.mouseDown(mask as HTMLElement);
  fireEvent.click(mask as HTMLElement);
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  auth.identity = { administrator: false, role: "member" };
});

describe("RepositoriesPage role-scoped catalog", () => {
  it("clears a failed deletion after retrying the same confirmation successfully", async () => {
    const user = userEvent.setup();
    auth.identity = { administrator: true, role: "admin" };
    const repository = {
      id: "11111111-1111-4111-8111-111111111111",
      name: "retry-delete",
      format: "raw",
      type: "hosted",
      state: "active",
      version: "1",
    };
    vi.mocked(listRepositories)
      .mockResolvedValueOnce({ data: { items: [repository] } } as never)
      .mockResolvedValue({ data: { items: [] } } as never);
    vi.mocked(listRepositoryCapacities).mockResolvedValue({
      data: [],
    } as never);
    vi.mocked(listFormatProfiles).mockResolvedValue({
      data: { items: profiles },
    } as never);
    vi.mocked(deleteRepository)
      .mockResolvedValueOnce({
        error: { status: 503, message: "Deletion unavailable" },
      } as never)
      .mockResolvedValue({ data: undefined } as never);
    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <MemoryRouter>
            <RepositoriesPage />
          </MemoryRouter>
        </PreferencesProvider>
      </TestQueryProvider>,
    );
    await user.click(
      await screen.findByRole("button", { name: "删除 retry-delete" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /^删\s*除$/ }));
    expect(await screen.findByText("Deletion unavailable")).toBeVisible();
    const retry = within(dialog).getByRole("button", { name: /^删\s*除$/ });
    await waitFor(() => {
      expect(retry).toBeEnabled();
      expect(retry).not.toHaveClass("ant-btn-loading");
    });
    await user.click(retry);
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(await screen.findByText("暂无仓库")).toBeVisible();
    expect(screen.queryByText("Deletion unavailable")).not.toBeInTheDocument();
    expect(vi.mocked(deleteRepository)).toHaveBeenCalledTimes(2);
  });
  it("shows a member readable repositories without administrator controls", async () => {
    vi.mocked(listRepositories).mockResolvedValue({
      data: {
        items: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            name: "release-files",
            format: "raw",
            type: "hosted",
            state: "active",
            version: "1",
          },
        ],
      },
    } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <MemoryRouter>
            <RepositoriesPage />
          </MemoryRouter>
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    expect(
      await screen.findByRole("link", { name: "release-files" }),
    ).toHaveAttribute(
      "href",
      "/repositories/11111111-1111-4111-8111-111111111111",
    );
    expect(
      screen.queryByRole("button", { name: "新建仓库" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /删除 release-files/ }),
    ).not.toBeInTheDocument();
    expect(vi.mocked(listRepositoryCapacities)).not.toHaveBeenCalled();
    expect(vi.mocked(listFormatProfiles)).not.toHaveBeenCalled();
  });

  it("tells a member without grants where repository access comes from", async () => {
    vi.mocked(listRepositories).mockResolvedValue({
      data: { items: [] },
    } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <MemoryRouter>
            <RepositoriesPage />
          </MemoryRouter>
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    expect(
      await screen.findByText(
        "你还没有任何仓库授权。仓库权限由平台管理员按仓库分配；请联系管理员为你的账号授权。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        "仓库是制品格式与策略的边界；创建后即可发布、代理和治理制品。",
      ),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "新建仓库" }),
    ).not.toBeInTheDocument();
  });

  it("holds the create dialog shut while saving and frees it after a failure", async () => {
    const user = userEvent.setup();
    auth.identity = { administrator: true, role: "admin" };
    vi.mocked(listRepositories).mockResolvedValue({
      data: { items: [] },
    } as never);
    vi.mocked(listRepositoryCapacities).mockResolvedValue({
      data: [],
    } as never);
    vi.mocked(listFormatProfiles).mockResolvedValue({
      data: { items: profiles },
    } as never);
    let rejectSave: (error: unknown) => void = () => {};
    vi.mocked(createRepository).mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          rejectSave = reject;
        }) as never,
    );

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <MemoryRouter>
            <RepositoriesPage />
          </MemoryRouter>
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await user.click(await screen.findByRole("button", { name: /新建仓库/ }));
    await user.type(
      screen.getByRole("textbox", { name: "仓库名称" }),
      "release-files",
    );
    await user.click(screen.getByRole("button", { name: /创\s*建/ }));

    clickMask();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    rejectSave(new Error("network down"));
    expect(
      await within(screen.getByRole("dialog")).findByText("network down"),
    ).toBeInTheDocument();

    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("frees the delete confirmation when the request rejects", async () => {
    const user = userEvent.setup();
    auth.identity = { administrator: true, role: "admin" };
    vi.mocked(listRepositories).mockResolvedValue({
      data: {
        items: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            name: "release-files",
            format: "raw",
            type: "hosted",
            state: "active",
            version: "1",
          },
        ],
      },
    } as never);
    vi.mocked(listRepositoryCapacities).mockResolvedValue({
      data: [],
    } as never);
    vi.mocked(listFormatProfiles).mockResolvedValue({
      data: { items: profiles },
    } as never);
    vi.mocked(deleteRepository).mockRejectedValueOnce(
      new Error("network down"),
    );

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <MemoryRouter>
            <RepositoriesPage />
          </MemoryRouter>
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await user.click(
      await screen.findByRole("button", { name: "删除 release-files" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /^删\s*除$/ }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
    // The rejection released `deleting`, so the dialog is dismissible again.
    clickMask();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
});
