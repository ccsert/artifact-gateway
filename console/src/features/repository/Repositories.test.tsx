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
  identity: { administrator: false, role: "writer" },
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
});

describe("RepositoriesPage role-scoped catalog", () => {
  it("shows a writer readable repositories without administrator controls", async () => {
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
});
