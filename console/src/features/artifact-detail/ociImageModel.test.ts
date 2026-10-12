import { afterEach, describe, expect, it, vi } from "vitest";
import { listOciManifests } from "../../client";
import { createQueryClient } from "../../lib/query/client";
import { fetchOciManifest, fetchOciManifests } from "./ociImageModel";

vi.mock("../../client", () => ({ listOciManifests: vi.fn() }));

afterEach(() => {
  vi.resetAllMocks();
  vi.restoreAllMocks();
});

describe("OCI query failures", () => {
  it("retains the manifest when optional config metadata is not found", async () => {
    const manifest = { schemaVersion: 2, config: { digest: "sha256:config" } };
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const target = String(input);
      if (target === "/auth/token")
        return Response.json({ token: "registry-token" });
      if (target.includes("/manifests/")) return Response.json(manifest);
      return new Response(null, { status: 404 });
    });
    await expect(
      fetchOciManifest("token", "repo/image", "1.0"),
    ).resolves.toEqual({ manifest, config: null });
  });

  it.each([401, 403, 404])(
    "preserves the SDK %s without retrying and reports authorization failures",
    async (status) => {
      const problem = { status, message: "Manifest list refused" };
      vi.mocked(listOciManifests).mockResolvedValue({
        error: problem,
      } as never);
      const authFailure = vi.fn();
      const queryClient = createQueryClient(authFailure);
      try {
        await expect(
          queryClient.fetchQuery({
            queryKey: ["oci-manifests"],
            queryFn: ({ signal }) => fetchOciManifests("repo", "image", signal),
            retryDelay: 0,
          }),
        ).rejects.toBe(problem);
        expect(listOciManifests).toHaveBeenCalledTimes(1);
        expect(listOciManifests).toHaveBeenCalledWith(
          expect.objectContaining({ signal: expect.any(AbortSignal) }),
        );
        if (status === 404) expect(authFailure).not.toHaveBeenCalled();
        else expect(authFailure).toHaveBeenCalledWith(status);
      } finally {
        queryClient.clear();
      }
    },
  );

  it.each(["token", "manifest", "config"])(
    "reports a native %s 401 to session handling without retrying",
    async (source) => {
      const fetchMock = vi
        .spyOn(globalThis, "fetch")
        .mockImplementation(async (input) => {
          const target = String(input);
          if (target === "/auth/token" && source !== "token")
            return Response.json({ token: "registry-token" });
          if (target.includes("/manifests/") && source === "config")
            return Response.json({
              schemaVersion: 2,
              config: { digest: "sha256:config" },
            });
          return new Response("Session expired", { status: 401 });
        });
      const authFailure = vi.fn();
      const queryClient = createQueryClient(authFailure);
      try {
        await expect(
          queryClient.fetchQuery({
            queryKey: ["oci-manifest", source],
            queryFn: ({ signal }) =>
              fetchOciManifest("token", "repo/image", "1.0", signal),
            retryDelay: 0,
          }),
        ).rejects.toMatchObject({ status: 401 });
        expect(authFailure).toHaveBeenCalledWith(401);
        const deniedPath =
          source === "token"
            ? "/auth/token"
            : source === "manifest"
              ? "/v2/repo/image/manifests/1.0"
              : "/v2/repo/image/blobs/sha256:config";
        expect(
          fetchMock.mock.calls.filter(
            ([input]) => String(input) === deniedPath,
          ),
        ).toHaveLength(1);
      } finally {
        queryClient.clear();
      }
    },
  );
});
