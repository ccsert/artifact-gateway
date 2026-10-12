import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  deleteConanPackageRevision,
  getArtifactIntelligence,
  getArtifactQuarantine,
  getRepositoryArtifactScanStatus,
  listConanPackageIds,
  listConanPackageRevisions,
  listConanRecipeRevisions,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { ConanArtifactDetail } from "./ConanArtifactDetail";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  deleteConanPackageRevision: vi.fn(),
  getArtifactIntelligence: vi.fn(),
  getArtifactQuarantine: vi.fn(),
  getRepositoryArtifactScanStatus: vi.fn(),
  listConanPackageIds: vi.fn(),
  listConanPackageRevisions: vi.fn(),
  listConanRecipeRevisions: vi.fn(),
}));

const repoId = "00000000-0000-4000-8000-000000000001";
const reference = "zlib/1.3.1@demo/stable";
const createdAt = "2026-10-12T08:00:00Z";

function mockConan() {
  const notFound = {
    error: { status: 404, title: "Not Found" },
    response: new Response(null, { status: 404 }),
  } as never;
  vi.mocked(getArtifactQuarantine).mockResolvedValue(notFound);
  vi.mocked(getArtifactIntelligence).mockResolvedValue(notFound);
  vi.mocked(getRepositoryArtifactScanStatus).mockResolvedValue(notFound);
  vi.mocked(listConanRecipeRevisions).mockResolvedValue({
    data: {
      items: [
        {
          revision: "rrev-new",
          digest: `sha256:${"a".repeat(64)}`,
          createdAt,
          state: "visible",
        },
        {
          revision: "rrev-gone",
          digest: `sha256:${"b".repeat(64)}`,
          createdAt,
          state: "deleted",
        },
      ],
    },
  } as never);
  vi.mocked(listConanPackageIds).mockResolvedValue({
    data: { items: ["pkg-linux", "pkg-mac"] },
  } as never);
  vi.mocked(listConanPackageRevisions).mockImplementation(
    async ({ query }) =>
      ({
        data: {
          items: [
            {
              recipeRevision: query.recipeRevision,
              packageId: query.packageId,
              revision: `prev-${query.packageId}`,
              digest: `sha256:${"c".repeat(64)}`,
              createdAt,
              state: "visible",
            },
          ],
        },
      }) as never,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ConanArtifactDetail", () => {
  it("lists the newest visible recipe's package revisions and drops a deleted one", async () => {
    mockConan();
    vi.mocked(deleteConanPackageRevision).mockResolvedValue({} as never);
    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <ConanArtifactDetail
            repoId={repoId}
            repoName="conan-local"
            meta={{ coordinate: reference }}
            managed
            canDelete
          />
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    expect(
      await screen.findByText("pkg-linux#prev-pkg-linux"),
    ).toBeInTheDocument();
    expect(screen.getByText("pkg-mac#prev-pkg-mac")).toBeInTheDocument();
    expect(screen.queryByText("rrev-gone")).not.toBeInTheDocument();
    expect(listConanPackageIds).toHaveBeenCalledWith({
      path: { repositoryId: repoId },
      query: { reference, recipeRevision: "rrev-new" },
      signal: expect.any(AbortSignal),
    });

    const user = userEvent.setup();
    const linuxRow = screen
      .getByText("pkg-linux#prev-pkg-linux")
      .closest("div.flex") as HTMLElement;
    await user.click(
      linuxRow.querySelector<HTMLElement>("button.ant-btn-dangerous")!,
    );
    const confirm = await screen.findAllByRole("button", { name: /删\s*除/ });
    await user.click(confirm[confirm.length - 1]);

    await waitFor(() =>
      expect(
        screen.queryByText("pkg-linux#prev-pkg-linux"),
      ).not.toBeInTheDocument(),
    );
    expect(deleteConanPackageRevision).toHaveBeenCalledWith({
      path: { repositoryId: repoId, revision: "prev-pkg-linux" },
      query: {
        reference,
        recipeRevision: "rrev-new",
        packageId: "pkg-linux",
      },
    });
    expect(screen.getByText("pkg-mac#prev-pkg-mac")).toBeInTheDocument();
  });
});
