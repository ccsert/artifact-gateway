import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getRepositoryCapacity, replaceRepositoryCapacity } from "../../client";
import type { Repository } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { RepositoryCapacityTab } from "./RepositoryCapacityTab";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  getRepositoryCapacity: vi.fn(),
  replaceRepositoryCapacity: vi.fn(),
}));

const repository: Repository = {
  id: "repo-A",
  name: "A",
  format: "npm",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe("RepositoryCapacityTab", () => {
  it("resets another repository's quota draft before saving", async () => {
    const user = userEvent.setup();
    vi.mocked(getRepositoryCapacity).mockImplementation(
      async (options) =>
        ({
          data: {
            quotaBytes:
              options?.path.repositoryId === "repo-A"
                ? 10737418240
                : 21474836480,
            usedBytes: 0,
            objectCount: 0,
          },
        }) as never,
    );
    vi.mocked(replaceRepositoryCapacity).mockResolvedValue({
      data: {},
    } as never);
    const view = (repo: Repository) => (
      <TestQueryProvider>
        <PreferencesProvider>
          <AntdProvider>
            <RepositoryCapacityTab repo={repo} />
          </AntdProvider>
        </PreferencesProvider>
      </TestQueryProvider>
    );
    const mounted = render(view(repository));
    await waitFor(() =>
      expect(screen.getByRole("spinbutton")).toHaveValue("10"),
    );
    fireEvent.change(screen.getByRole("spinbutton"), {
      target: { value: "50" },
    });
    expect(screen.getByRole("spinbutton")).toHaveValue("50");

    mounted.rerender(view({ ...repository, id: "repo-B", name: "B" }));
    await screen.findByText("20.0 GiB");
    expect.soft(screen.getByRole("spinbutton")).toHaveValue("20");
    await user.click(screen.getByRole("button", { name: /更新配额/ }));
    expect(replaceRepositoryCapacity).toHaveBeenCalledWith({
      path: { repositoryId: "repo-B" },
      body: { quotaBytes: 21474836480 },
    });
  });
});
