import { describe, expect, it } from "vitest";
import {
  isMigrationTargetFormat,
  migrationTargetFormat,
  migrationTargetURL,
} from "./migrationFormats";

describe("explicit npm migration format", () => {
  it("maps npm Hosted without admitting unrelated formats", () => {
    expect(migrationTargetFormat("npm")).toBe("npm");
    expect(isMigrationTargetFormat("npm")).toBe(true);
    expect(migrationTargetFormat("unknown")).toBeUndefined();
    expect(isMigrationTargetFormat("cargo")).toBe(false);
  });
  it("exports the native npm registry root", () => {
    expect(
      migrationTargetURL("npm", "npm-releases", "https://gateway.example"),
    ).toBe("https://gateway.example/npm/npm-releases/");
  });
});
