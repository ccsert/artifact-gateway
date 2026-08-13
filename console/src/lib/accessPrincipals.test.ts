import { describe, expect, it } from "vitest";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "./accessPrincipals";

describe("authorization principal visibility", () => {
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
