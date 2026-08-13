import { describe, expect, it } from "vitest";
import {
  accessControlTabFromQuery,
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "./AccessControl";

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

describe("access evaluation principal visibility", () => {
  it("keeps active users and hides disabled users", () => {
    expect(isActiveUserPrincipal({ state: "active" })).toBe(true);
    expect(isActiveUserPrincipal({ state: "disabled" })).toBe(false);
  });

  it("keeps usable API keys and hides revoked or expired keys", () => {
    const now = Date.parse("2026-08-13T08:00:00Z");

    expect(
      isActiveApiKeyPrincipal(
        { revokedAt: undefined, expiresAt: undefined },
        now,
      ),
    ).toBe(true);
    expect(
      isActiveApiKeyPrincipal(
        { revokedAt: undefined, expiresAt: "2026-08-13T09:00:00Z" },
        now,
      ),
    ).toBe(true);
    expect(
      isActiveApiKeyPrincipal(
        {
          revokedAt: "2026-08-13T07:00:00Z",
          expiresAt: "2026-08-13T09:00:00Z",
        },
        now,
      ),
    ).toBe(false);
    expect(
      isActiveApiKeyPrincipal(
        { revokedAt: undefined, expiresAt: "2026-08-13T08:00:00Z" },
        now,
      ),
    ).toBe(false);
  });
});
