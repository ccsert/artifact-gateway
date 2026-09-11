// Triggers a browser download for an in-memory blob without navigating away.
// The object URL is revoked immediately after the synthetic click so long
// operator sessions do not accumulate detached blobs.
export function downloadBlob(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  URL.revokeObjectURL(url);
}

// Returns the canonical `sha256:<hex>` receipt for a blob, or null when the
// runtime has no SubtleCrypto. Callers use this to hand the operator the exact
// digest they must store next to a backup; it is never derived from an
// uploaded archive.
export async function sha256Receipt(blob: Blob): Promise<string | null> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null;
  try {
    const digest = await subtle.digest("SHA-256", await blob.arrayBuffer());
    const hex = [...new Uint8Array(digest)]
      .map((byte) => byte.toString(16).padStart(2, "0"))
      .join("");
    return `sha256:${hex}`;
  } catch {
    return null;
  }
}
