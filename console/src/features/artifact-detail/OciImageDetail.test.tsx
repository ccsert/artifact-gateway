import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getArtifactIntelligence,
  getArtifactQuarantine,
  getRepositoryArtifactScanStatus,
  listOciManifests,
} from "../../client";
import { AuthProvider } from "../../lib/auth";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { OciImageDetail } from "./OciImageDetail";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  getArtifactIntelligence: vi.fn(),
  getArtifactQuarantine: vi.fn(),
  getRepositoryArtifactScanStatus: vi.fn(),
  listOciManifests: vi.fn(),
}));

const repositoryId = "00000000-0000-4000-8000-000000000001";
const digestA = `sha256:${"a".repeat(64)}`;
const digestB = `sha256:${"b".repeat(64)}`;

function mockRegistry() {
  vi.mocked(listOciManifests).mockResolvedValue({
    data: {
      items: [
        { digest: digestA, tags: ["1.0"], size: 512 },
        { digest: digestB, tags: ["2.0"], size: 640 },
      ],
    },
  } as never);
  const notFound = {
    error: { status: 404, title: "Not Found" },
    response: new Response(null, { status: 404 }),
  } as never;
  vi.mocked(getArtifactQuarantine).mockResolvedValue(notFound);
  vi.mocked(getArtifactIntelligence).mockResolvedValue(notFound);
  vi.mocked(getRepositoryArtifactScanStatus).mockResolvedValue(notFound);
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const target = String(input);
    if (target === "/auth/session") return new Response(null, { status: 401 });
    if (target === "/auth/token")
      return Response.json({ token: "registry-token" });
    const manifest = target.match(
      /^\/v2\/docker-hosted\/app\/manifests\/(.+)$/,
    );
    if (manifest) {
      const layers = manifest[1] === "2.0" ? 3 : 1;
      return Response.json({
        schemaVersion: 2,
        mediaType: "application/vnd.oci.image.manifest.v1+json",
        config: { mediaType: "config", size: 10, digest: "sha256:config" },
        layers: Array.from({ length: layers }, (_, index) => ({
          mediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
          size: 100,
          digest: `sha256:${String(index).repeat(64)}`,
        })),
      });
    }
    if (target.endsWith("/blobs/sha256:config"))
      return Response.json({ architecture: "arm64", os: "linux" });
    return new Response(null, { status: 404 });
  });
}

function renderDetail(initialReference?: string) {
  const tree = (reference?: string) => (
    <AuthProvider>
      <TestQueryProvider>
        <PreferencesProvider>
          <OciImageDetail
            repositoryId={repositoryId}
            repository="docker-hosted"
            image="app"
            initialReference={reference}
          />
        </PreferencesProvider>
      </TestQueryProvider>
    </AuthProvider>
  );
  const view = render(tree(initialReference));
  return {
    ...view,
    relink: (reference?: string) => view.rerender(tree(reference)),
  };
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("OciImageDetail", () => {
  it("opens the deep-linked tag and reads its manifest and config", async () => {
    const fetchMock = mockRegistry();
    renderDetail("2.0");

    expect(await screen.findByText("arm64 / linux")).toBeInTheDocument();
    expect(screen.getByText(digestB)).toBeInTheDocument();
    expect(screen.getByText("#3")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/v2/docker-hosted/app/manifests/2.0",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer registry-token",
        }),
      }),
    );
    expect(listOciManifests).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { repositoryId },
        query: expect.objectContaining({ name: "app" }),
        signal: expect.any(AbortSignal),
      }),
    );
  });

  it("follows a changed deep link and defaults to the first version without one", async () => {
    mockRegistry();
    const { relink } = renderDetail();

    expect(await screen.findByText(digestA)).toBeInTheDocument();
    expect(await screen.findByText("#1")).toBeInTheDocument();

    relink("2.0");
    expect(await screen.findByText(digestB)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("#3")).toBeInTheDocument());
  });
});
