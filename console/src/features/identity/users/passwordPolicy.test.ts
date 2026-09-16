import { describe, expect, it } from "vitest";
import {
  localPasswordByteLength,
  localPasswordCharacterLength,
  localPasswordFitsBcrypt,
  localPasswordMeetsMinimum,
} from "./passwordPolicy";

describe("local password policy", () => {
  it("measures UTF-8 bytes instead of JavaScript code units", () => {
    expect(localPasswordByteLength("password")).toBe(8);
    expect(localPasswordByteLength("密码密码")).toBe(12);
    expect(localPasswordCharacterLength("🔐🔐🔐🔐")).toBe(4);
    expect(localPasswordMeetsMinimum("🔐🔐🔐🔐")).toBe(false);
  });

  it("rejects values beyond bcrypt's byte limit", () => {
    expect(localPasswordFitsBcrypt("a".repeat(72))).toBe(true);
    expect(localPasswordFitsBcrypt("密".repeat(25))).toBe(false);
  });
});
