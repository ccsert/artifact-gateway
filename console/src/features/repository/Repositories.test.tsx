import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import {
  listRepositories,
  listRepositoryCapacities,
  listFormatProfiles,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoriesPage } from "./Repositories";

const auth = vi.hoisted(() => ({
  identity: { administrator: false, role: "member" },
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => auth,
}));

vi.mock("../../client", async () => ({
  ...(await vi.importActual<typeof import("../../client")>("../../client")),
  listRepositories: vi.fn(),
  listRepositoryCapacities: vi.fn(),
  listFormatProfiles: vi.fn(),
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  auth.identity = { administrator: false, role: "member" };
});

describe("RepositoriesPage role-scoped catalog", () => {
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
      <PreferencesProvider>
        <MemoryRouter>
          <RepositoriesPage />
        </MemoryRouter>
      </PreferencesProvider>,
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
      <PreferencesProvider>
        <MemoryRouter>
          <RepositoriesPage />
        </MemoryRouter>
      </PreferencesProvider>,
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
});
