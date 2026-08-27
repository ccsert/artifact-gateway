import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { listRepositoryArtifactIdentities } from "../client";
import type { Repository } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { RepositoryArtifactSelect } from "./RepositoryArtifactSelect";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  listRepositoryArtifactIdentities: vi.fn(),
}));

const repository: Repository = {
  id: "33333333-3333-4333-8333-333333333333",
  name: "raw-releases",
  format: "raw",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("shows readable Raw identities and canonicalizes literal percent signs in searches", async () => {
  vi.mocked(listRepositoryArtifactIdentities).mockResolvedValue({
    data: {
      items: [
        {
          coordinate: "report%2520final.txt",
          digest: `sha256:${"c".repeat(64)}`,
        },
      ],
    },
  } as never);
  const user = userEvent.setup();

  render(
    <PreferencesProvider>
      <RepositoryArtifactSelect
        repo={repository}
        purpose="distribution"
        value={null}
        onChange={vi.fn()}
        ariaLabel="选择 Raw 制品"
      />
    </PreferencesProvider>,
  );

  const input = screen.getByRole("combobox", { name: "选择 Raw 制品" });
  await user.click(input);
  expect(await screen.findByText("report%20final.txt")).toBeInTheDocument();

  await user.type(input, "report%20final.txt");
  await waitFor(
    () =>
      expect(listRepositoryArtifactIdentities).toHaveBeenLastCalledWith({
        path: { repositoryId: repository.id },
        query: {
          purpose: "distribution",
          q: "report%2520final.txt",
          pageSize: 50,
        },
      }),
    { timeout: 1500 },
  );
});
