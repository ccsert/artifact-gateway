import { createHash } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadBlob, sha256Receipt } from "./download";

// jsdom implements neither `Blob.arrayBuffer` nor `SubtleCrypto`, while every
// browser this Console ships to implements both. The helpers below fill those
// two gaps so the receipt formatting itself is what gets asserted.
function stubBlobBytes(bytes: number[]) {
  Object.defineProperty(Blob.prototype, "arrayBuffer", {
    configurable: true,
    writable: true,
    value: async () => new Uint8Array(bytes).buffer,
  });
}

function stubDigest() {
  vi.stubGlobal("crypto", {
    subtle: {
      digest: async (_algorithm: string, data: ArrayBuffer) =>
        new Uint8Array(createHash("sha256").update(Buffer.from(data)).digest())
          .buffer,
    },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (Blob.prototype as { arrayBuffer?: unknown }).arrayBuffer;
});

describe("downloadBlob", () => {
  it("clicks a synthetic anchor and revokes the object URL", () => {
    const createObjectURL = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:archive");
    const revokeObjectURL = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => undefined);
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    downloadBlob("apt-stable-2.tar", new Blob(["archive"]));

    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:archive");
    // The anchor must not leak into the document after the click.
    expect(document.querySelectorAll("a[download]")).toHaveLength(0);
  });
});

describe("sha256Receipt", () => {
  it("returns the canonical sha256 receipt for the blob bytes", async () => {
    // The expected value is SHA-256 of the single byte 0x00.
    stubBlobBytes([0]);
    stubDigest();

    expect(await sha256Receipt(new Blob([new Uint8Array([0])]))).toBe(
      "sha256:6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d",
    );
  });

  it("falls back to null when the runtime has no SubtleCrypto", async () => {
    stubBlobBytes([0]);
    vi.stubGlobal("crypto", {});
    expect(await sha256Receipt(new Blob(["archive"]))).toBeNull();
  });

  it("falls back to null when the archive bytes cannot be read", async () => {
    // No stubBlobBytes call: this mirrors a runtime whose Blob cannot be read,
    // where the receipt must never be reported as valid.
    stubDigest();
    expect(await sha256Receipt(new Blob(["archive"]))).toBeNull();
  });
});
