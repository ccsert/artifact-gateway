import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listConanRecipeRevisions, listOciManifests } from "../../client";
import { TestQueryProvider } from "../../test/queryClient";
import type { PublicRepository } from "./types";
import { usePublicVersionPages } from "./usePublicVersionPages";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  listConanRecipeRevisions: vi.fn(),
  listOciManifests: vi.fn(),
}));

const oci = {
  id: "repo-oci",
  name: "containers",
  format: "oci",
  type: "hosted",
} as PublicRepository;
const conan = {
  id: "repo-conan",
  name: "conan-local",
  format: "conan",
  type: "hosted",
} as PublicRepository;

const wrapper = ({ children }: { children: ReactNode }) => (
  <TestQueryProvider>{children}</TestQueryProvider>
);

function renderPages(repository: PublicRepository, tagParam = "") {
  return renderHook(
    ({ repo }) =>
      usePublicVersionPages({
        repositoryId: repo.id,
        selectedRepository: repo,
        artifactParam: tagParam ? "app" : "",
        tagParam,
      }),
    { wrapper, initialProps: { repo: repository } },
  );
}

const manifest = (digest: string, tags: string[]) => ({
  digest,
  tags,
  size: 1,
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(listOciManifests).mockReset();
  vi.mocked(listConanRecipeRevisions).mockReset();
  vi.unstubAllGlobals();
});

describe("public version pages", () => {
  it("accumulates OCI tag pages and reads the shown tag's manifest", async () => {
    const fetchMock = vi.fn(async () =>
      Response.json({ schemaVersion: 2, layers: [] }),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.mocked(listOciManifests)
      .mockResolvedValueOnce({
        data: {
          items: [manifest("sha256:a", ["1.0"]), manifest("sha256:b", ["2.0"])],
          nextPageToken: "page-2",
        },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [manifest("sha256:c", [])] },
      } as never);
    const { result } = renderPages(oci, "2.0");

    await act(() => result.current.loadOciTags("app"));
    await waitFor(() =>
      expect(result.current.ociTagPages.app).toMatchObject({
        items: ["1.0", "2.0"],
        nextCursor: "page-2",
        loaded: true,
        loading: false,
      }),
    );
    // The deep-linked tag, not the first one, is the version shown.
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/v2/containers/app/manifests/2.0",
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      ),
    );

    await act(() => result.current.loadOciTags("app", "page-2"));
    await waitFor(() =>
      expect(result.current.ociTagPages.app).toMatchObject({
        items: ["1.0", "2.0", "sha256:c"],
        nextCursor: undefined,
        loading: false,
      }),
    );
    expect(vi.mocked(listOciManifests).mock.calls[1][0]?.query).toMatchObject({
      name: "app",
      pageToken: "page-2",
    });
  });

  it("keeps earlier tags when a later page fails, and Retry reads only that page", async () => {
    vi.mocked(listOciManifests)
      .mockResolvedValueOnce({
        data: {
          items: [manifest("sha256:a", ["1.0"])],
          nextPageToken: "page-2",
        },
      } as never)
      .mockResolvedValueOnce({
        error: { status: 503, message: "unavailable" },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [manifest("sha256:b", ["2.0"])] },
      } as never);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({ schemaVersion: 2 })),
    );
    const { result } = renderPages(oci);

    await act(() => result.current.loadOciTags("app"));
    await waitFor(() =>
      expect(result.current.ociTagPages.app?.loaded).toBe(true),
    );
    await act(() => result.current.loadOciTags("app", "page-2"));
    await waitFor(() =>
      expect(result.current.ociTagPages.app?.error).toBe("读取 OCI 版本失败"),
    );
    expect(result.current.ociTagPages.app.items).toEqual(["1.0"]);

    await act(() => result.current.loadOciTags("app", "page-2"));
    await waitFor(() =>
      expect(result.current.ociTagPages.app).toMatchObject({
        items: ["1.0", "2.0"],
        error: undefined,
      }),
    );
    expect(listOciManifests).toHaveBeenCalledTimes(3);
  });

  it("starts Conan revisions over for a new filter and forgets them with the repository", async () => {
    vi.mocked(listConanRecipeRevisions)
      .mockResolvedValueOnce({
        data: {
          items: [{ revision: "r1", state: "visible" }],
          nextPageToken: "next",
        },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [{ revision: "r2", state: "visible" }] },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [{ revision: "r9", state: "visible" }] },
      } as never);
    const { result, rerender } = renderPages(conan);

    await act(() => result.current.loadConanRevisions("zlib/1.3"));
    await act(() => result.current.loadConanRevisions("zlib/1.3", "", "next"));
    await waitFor(() =>
      expect(
        result.current.conanRevisionPages["zlib/1.3"]?.items.map(
          (item) => item.revision,
        ),
      ).toEqual(["r1", "r2"]),
    );

    await act(() => result.current.loadConanRevisions("zlib/1.3", "r9"));
    await waitFor(() =>
      expect(result.current.conanRevisionPages["zlib/1.3"]).toMatchObject({
        query: "r9",
        items: [{ revision: "r9" }],
        loading: false,
      }),
    );
    expect(
      vi.mocked(listConanRecipeRevisions).mock.calls[2][0]?.query,
    ).toMatchObject({ reference: "zlib/1.3", q: "r9" });

    rerender({ repo: oci });
    expect(result.current.conanRevisionPages).toEqual({});
    rerender({ repo: conan });
    expect(result.current.conanRevisionPages).toEqual({});
  });
});
