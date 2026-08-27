import { describe, expect, it } from "vitest";
import {
  canonicalRawSearchPrefix,
  containsDisallowedRawPathCharacters,
  decodeRawPathForDisplay,
  encodeRawPath,
  rawResourceURL,
} from "./rawPath";

describe("Raw path presentation", () => {
  const readable = "ChatGPT Image 2026年8月19日 13_56_07 (2).png";
  const canonical =
    "ChatGPT%20Image%202026%E5%B9%B48%E6%9C%8819%E6%97%A5%2013_56_07%20%282%29.png";

  it("keeps the canonical wire path separate from the readable name", () => {
    expect(encodeRawPath(readable)).toBe(canonical);
    expect(decodeRawPathForDisplay(canonical)).toBe(readable);
  });

  it("canonicalizes readable search text", () => {
    expect(canonicalRawSearchPrefix(readable)).toBe(canonical);
  });

  it("treats percent-looking text from a search box as a literal file name", () => {
    expect(canonicalRawSearchPrefix("report%20final.txt")).toBe(
      "report%2520final.txt",
    );
  });

  it("uses the canonical coordinate unchanged for Raw actions", () => {
    expect(rawResourceURL("raw-releases", canonical)).toBe(
      `/raw/raw-releases/${canonical}`,
    );
    expect(rawResourceURL("raw-releases", canonical)).not.toContain("%2520");
  });

  it("leaves malformed legacy display values intact", () => {
    expect(decodeRawPathForDisplay("broken%name.png")).toBe("broken%name.png");
  });

  it("rejects invisible path controls while keeping ordinary Unicode", () => {
    expect(containsDisallowedRawPathCharacters("中文 (2).png")).toBe(false);
    expect(containsDisallowedRawPathCharacters("report\u0000.png")).toBe(true);
    expect(containsDisallowedRawPathCharacters("report\u202e.png")).toBe(true);
  });
});
