import { describe, expect, it } from "vitest";
import { accessControlTabFromQuery } from "./AccessControl";

describe("accessControlTabFromQuery", () => {
  it.each(["evaluate", "policies"] as const)(
    "keeps the %s deep link",
    (tab) => {
      expect(accessControlTabFromQuery(tab)).toBe(tab);
    },
  );

  it.each([null, "", "unknown", "grants"])("uses grants for %s", (tab) => {
    expect(accessControlTabFromQuery(tab)).toBe("grants");
  });
});
