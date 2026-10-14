import { createTranslator } from "../../messages/translate";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listConanRecipeRevisions, listOciManifests } from "../../client";
import { createQueryClient } from "../../lib/query/client";
import { TestQueryProvider } from "../../test/queryClient";
import type { PublicRepository } from "./types";
import { usePublicVersionPages } from "./usePublicVersionPages";
import { OciVersionPanel } from "./OciVersionPanel";
import { buildPublicArtifactRows } from "./publicArtifactRows";

vi.mock("../../lib/preferences", () => ({
  usePreferences: () => ({
    locale: "en-US",
    t: (key: string) => key,
  }),
}));

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

function renderPages(
  repository: PublicRepository,
  wrapper = TestQueryProvider,
) {
  return renderHook(
    ({ repo }) =>
      usePublicVersionPages({
        repositoryId: repo.id,
        selectedRepository: repo,
        artifactParam: "",
        tagParam: "",
      }),
    { wrapper, initialProps: { repo: repository } },
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(listOciManifests).mockReset();
  vi.mocked(listConanRecipeRevisions).mockReset();
  vi.unstubAllGlobals();
});

describe("public version recovery", () => {
  it("retains earlier Conan revisions and resumes the failed continuation", async () => {
    vi.mocked(listConanRecipeRevisions)
      .mockResolvedValueOnce({
        data: {
          items: [{ revision: "r1", state: "visible" }],
          nextPageToken: "next",
        },
      } as never)
      .mockResolvedValueOnce({ error: { status: 503 } } as never)
      .mockResolvedValueOnce({
        data: { items: [{ revision: "r2", state: "visible" }] },
      } as never);
    const { result } = renderPages(conan);
    await act(() => result.current.loadConanRevisions("zlib/1.3"));
    await waitFor(() =>
      expect(result.current.conanRevisionPages["zlib/1.3"]?.loaded).toBe(true),
    );
    await act(() => result.current.loadConanRevisions("zlib/1.3", "", "next"));
    await waitFor(() =>
      expect(
        result.current.conanRevisionPages["zlib/1.3"]?.error,
      ).toBeDefined(),
    );
    await act(() =>
      result.current.loadConanRevisions(
        "zlib/1.3",
        "",
        result.current.conanRevisionPages["zlib/1.3"].nextPageToken,
      ),
    );
    await waitFor(() =>
      expect(
        result.current.conanRevisionPages["zlib/1.3"]?.items.map(
          (item) => item.revision,
        ),
      ).toEqual(["r1", "r2"]),
    );
    expect(listConanRecipeRevisions).toHaveBeenCalledTimes(3);
    expect(
      vi.mocked(listConanRecipeRevisions).mock.calls[2][0]?.query?.pageToken,
    ).toBe("next");
  });

  it("keeps two loaded OCI pages when the panel retries a failed third page", async () => {
    vi.mocked(listOciManifests)
      .mockResolvedValueOnce({
        data: {
          items: [{ digest: "sha256:a", tags: ["1.0"] }],
          nextPageToken: "page-2",
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          items: [{ digest: "sha256:b", tags: ["2.0"] }],
          nextPageToken: "page-3",
        },
      } as never)
      .mockResolvedValueOnce({ error: { status: 503 } } as never)
      .mockResolvedValueOnce({
        data: { items: [{ digest: "sha256:c", tags: ["3.0"] }] },
      } as never);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({ schemaVersion: 2 })),
    );
    function Panel() {
      const pages = usePublicVersionPages({
        repositoryId: oci.id,
        selectedRepository: oci,
        artifactParam: "",
        tagParam: "",
      });
      const [row] = buildPublicArtifactRows({
        items: [{ coordinate: "app" }],
        selectedRepository: oci,
        expandedCoordinate: "app",
        artifactParam: "",
        tagParam: "",
        ociTagPages: pages.ociTagPages,
        ociManifestDetails: pages.ociManifestDetails,
        selectedProtocolVersions: {},
        artifactHref: () => "/artifact",
        t: createTranslator("zh-CN"),
      });
      return (
        <>
          <button onClick={() => void pages.loadOciTags("app")}>Start</button>
          <output>
            {row.protocolVersions.map((version) => version.value).join(",")}
          </output>
          <OciVersionPanel
            row={row}
            copiedCoordinate={null}
            onSelectVersion={() => {}}
            onLoadOciTags={pages.loadOciTags}
            onLoadOciManifest={pages.loadOciManifest}
            onCopyPageLink={() => {}}
            onCopyUsage={() => {}}
          />
        </>
      );
    }
    render(
      <TestQueryProvider>
        <Panel />
      </TestQueryProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await screen.findByText("1.0", { selector: "output" });
    fireEvent.click(
      screen.getByRole("button", { name: "publicBrowse.loadMoreVersions" }),
    );
    await screen.findByText("1.0,2.0", { selector: "output" });
    fireEvent.click(
      screen.getByRole("button", { name: "publicBrowse.loadMoreVersions" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /publicBrowse\.retry/ }),
    );
    await screen.findByText("1.0,2.0,3.0", { selector: "output" });
    expect(listOciManifests).toHaveBeenCalledTimes(4);
    expect(vi.mocked(listOciManifests).mock.calls[3][0]?.query?.pageToken).toBe(
      "page-3",
    );
  });

  it("reads a moved OCI tag's manifest again when returning to its repository", async () => {
    vi.mocked(listOciManifests).mockResolvedValue({
      data: { items: [{ digest: "sha256:a", tags: ["latest"], size: 1 }] },
    } as never);
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json(
          { schemaVersion: 2 },
          { headers: { "Docker-Content-Digest": "sha256:a" } },
        ),
      )
      .mockResolvedValueOnce(
        Response.json(
          { schemaVersion: 2 },
          { headers: { "Docker-Content-Digest": "sha256:b" } },
        ),
      );
    vi.stubGlobal("fetch", fetchMock);
    const { result, rerender } = renderPages(oci);
    await act(() => result.current.loadOciTags("app"));
    await waitFor(() =>
      expect(result.current.ociManifestDetails["app\x00latest"]?.digest).toBe(
        "sha256:a",
      ),
    );
    rerender({ repo: conan });
    rerender({ repo: oci });
    await act(() => result.current.loadOciTags("app"));
    await waitFor(() =>
      expect(result.current.ociManifestDetails["app\x00latest"]?.digest).toBe(
        "sha256:b",
      ),
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each([401, 403, 404])(
    "does not retry an OCI manifest HTTP %s or end the session",
    async (status) => {
      const authFailure = vi.fn();
      const client = createQueryClient(authFailure);
      client.setDefaultOptions({
        queries: { ...client.getDefaultOptions().queries, retryDelay: 0 },
      });
      const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      );
      const fetchMock = vi.fn(async () => new Response("refused", { status }));
      vi.stubGlobal("fetch", fetchMock);
      const { result } = renderPages(oci, wrapper);
      await act(() => result.current.loadOciManifest("app", "latest"));
      await waitFor(() =>
        expect(
          result.current.ociManifestDetails["app\x00latest"],
        ).toMatchObject({
          loading: false,
          error: "lib.failedToReadOciManifestStatus",
        }),
      );
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(authFailure).not.toHaveBeenCalled();
      client.clear();
    },
  );

  it("still retries a transient OCI manifest 503 once", async () => {
    const client = createQueryClient();
    client.setDefaultOptions({
      queries: { ...client.getDefaultOptions().queries, retryDelay: 0 },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("unavailable", { status: 503 }))
      .mockResolvedValueOnce(
        Response.json(
          { schemaVersion: 2 },
          { headers: { "Docker-Content-Digest": "sha256:a" } },
        ),
      );
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderPages(oci, wrapper);
    await act(() => result.current.loadOciManifest("app", "latest"));
    await waitFor(() =>
      expect(result.current.ociManifestDetails["app\x00latest"]?.digest).toBe(
        "sha256:a",
      ),
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
    client.clear();
  });
});
