import { describe, expect, it, vi } from "vitest";
import {
  nextPublicOciTagCursor,
  readPublicOciManifestDetail,
  type PublicOciFetch,
} from "./publicOci";

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init.headers,
    },
  });
}

describe("public OCI reader", () => {
  it("uses the Registry Link cursor before the page-size fallback", () => {
    const linked = jsonResponse(
      {},
      { headers: { Link: '</v2/demo/tags/list?last=2.0&n=50>; rel="next"' } },
    );

    expect(
      nextPublicOciTagCursor(linked, ["1.0"], 50, "https://registry.test"),
    ).toBe("2.0");
    expect(
      nextPublicOciTagCursor(
        jsonResponse({}),
        ["1.0", "2.0"],
        2,
        "https://registry.test",
      ),
    ).toBe("2.0");
    expect(
      nextPublicOciTagCursor(
        jsonResponse({}),
        ["1.0"],
        2,
        "https://registry.test",
      ),
    ).toBeUndefined();
  });

  it("resolves a multi-platform child while retaining the selected digest", async () => {
    const fetchAdapter = vi
      .fn<PublicOciFetch>()
      .mockResolvedValueOnce(
        jsonResponse(
          { manifests: [{ digest: "sha256:child" }] },
          { headers: { "Docker-Content-Digest": "sha256:index" } },
        ),
      )
      .mockResolvedValueOnce(
        jsonResponse(
          {
            config: { digest: "sha256:config", size: 10 },
            layers: [{ size: 20 }, { size: 30 }],
          },
          { headers: { "Docker-Content-Digest": "sha256:child" } },
        ),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          created: "2026-08-21T00:00:00Z",
          config: {
            Labels: { "org.opencontainers.image.vendor": "Artifact Team" },
          },
        }),
      );

    await expect(
      readPublicOciManifestDetail(
        {
          repositoryName: "releases",
          imageName: "team/gateway",
          reference: "latest",
        },
        fetchAdapter,
      ),
    ).resolves.toEqual({
      digest: "sha256:index",
      size: 60,
      createdAt: "2026-08-21T00:00:00Z",
      publisher: "Artifact Team",
    });

    expect(fetchAdapter).toHaveBeenNthCalledWith(
      1,
      "/v2/releases/team/gateway/manifests/latest",
      expect.objectContaining({
        headers: expect.objectContaining({
          Accept: expect.stringContaining(
            "application/vnd.oci.image.manifest.v1+json",
          ),
        }),
      }),
    );
    expect(fetchAdapter).toHaveBeenNthCalledWith(
      2,
      "/v2/releases/team/gateway/manifests/sha256%3Achild",
      expect.any(Object),
    );
    expect(fetchAdapter).toHaveBeenNthCalledWith(
      3,
      "/v2/releases/team/gateway/blobs/sha256%3Aconfig",
    );
  });

  it("keeps manifest data when the optional config cannot be read", async () => {
    const fetchAdapter = vi
      .fn<PublicOciFetch>()
      .mockResolvedValueOnce(
        jsonResponse(
          { config: { digest: "sha256:config", size: 12 } },
          { headers: { "Docker-Content-Digest": "sha256:manifest" } },
        ),
      )
      .mockResolvedValueOnce(jsonResponse({}, { status: 404 }));

    await expect(
      readPublicOciManifestDetail(
        {
          repositoryName: "releases",
          imageName: "gateway",
          reference: "1.0.0",
        },
        fetchAdapter,
      ),
    ).resolves.toEqual({
      digest: "sha256:manifest",
      size: 12,
      createdAt: undefined,
      publisher: undefined,
    });
  });

  it("surfaces manifest response failures through the module interface", async () => {
    const fetchAdapter = vi
      .fn<PublicOciFetch>()
      .mockResolvedValue(jsonResponse({}, { status: 404 }));

    await expect(
      readPublicOciManifestDetail(
        {
          repositoryName: "releases",
          imageName: "missing",
          reference: "latest",
        },
        fetchAdapter,
      ),
    ).rejects.toThrow("读取 OCI manifest 失败 (404)");
  });
});
