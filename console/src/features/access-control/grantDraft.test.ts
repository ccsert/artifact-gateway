import { describe, expect, it } from "vitest";
import type { AuthorizationRole } from "../../client";
import {
  SNAPSHOT_PERMISSION,
  emptyGrant,
  grantedCapabilitiesLabel,
  grantLevel,
  grantRowKey,
  permissionSelection,
  principalEditorKind,
  scopesForLevel,
  scopesForRole,
  type DraftGrant,
  type GrantLevel,
} from "./grantDraft";

const text = (_chinese: string, english: string) => english;

function role(
  id: string,
  scopes: AuthorizationRole["scopes"],
): AuthorizationRole {
  return {
    id,
    name: id,
    scopes,
    version: "1",
    createdAt: "2026-08-18T00:00:00Z",
    updatedAt: "2026-08-18T00:00:00Z",
  };
}

function draft(overrides: Partial<DraftGrant> = {}): DraftGrant {
  return {
    principal: "user:release",
    scopes: ["repositories:read"],
    ...overrides,
  };
}

const builtinLevels: Array<[GrantLevel, DraftGrant["scopes"]]> = [
  ["read", ["repositories:read"]],
  ["write", ["repositories:write"]],
  ["admin", ["repositories:admin"]],
  ["intelligence", ["repositories:intelligence"]],
];

describe("grant draft scope mapping", () => {
  it.each(builtinLevels)(
    "maps the %s level to its scope and back",
    (level, scopes) => {
      expect(scopesForLevel(level)).toEqual(scopes);
      expect(grantLevel(scopes)).toBe(level);
    },
  );

  it("keeps the strongest scope when a grant carries several", () => {
    expect(grantLevel(["repositories:read", "repositories:write"])).toBe(
      "write",
    );
    expect(grantLevel(["repositories:read", "repositories:admin"])).toBe(
      "admin",
    );
    expect(
      grantLevel(["repositories:write", "repositories:intelligence"]),
    ).toBe("intelligence");
    expect(grantLevel([])).toBe("read");
  });

  it("copies the scopes of a custom authorization role", () => {
    const custom = role("role-release", [
      "repositories:read",
      "repositories:write",
    ]);
    const copied = scopesForRole(custom);
    expect(copied).toEqual(["repositories:read", "repositories:write"]);
    expect(copied).not.toBe(custom.scopes);
    copied.push("repositories:admin");
    expect(custom.scopes).toEqual(["repositories:read", "repositories:write"]);
  });
});

describe("permissionSelection", () => {
  it("selects a custom role when the stored grant points at a known one", () => {
    const roles = [role("role-release", ["repositories:read"])];
    expect(
      permissionSelection(
        draft({
          roleId: "role-release",
          scopes: ["repositories:read", "repositories:write"],
        }),
        roles,
      ),
    ).toBe("role:role-release");
  });

  it("maps a single stored scope back to its built-in level", () => {
    expect(
      permissionSelection(draft({ scopes: ["repositories:write"] }), []),
    ).toBe("builtin:write");
  });

  it("falls back to the saved snapshot for a multi-scope grant without a matching level", () => {
    expect(
      permissionSelection(
        draft({ scopes: ["repositories:read", "repositories:write"] }),
        [],
      ),
    ).toBe(SNAPSHOT_PERMISSION);
  });

  it("ignores a role id that no longer exists", () => {
    expect(permissionSelection(draft({ roleId: "removed-role" }), [])).toBe(
      "builtin:read",
    );
    expect(
      permissionSelection(
        draft({
          roleId: "removed-role",
          scopes: ["repositories:read", "repositories:admin"],
        }),
        [],
      ),
    ).toBe(SNAPSHOT_PERMISSION);
  });
});

describe("grantedCapabilitiesLabel", () => {
  it("names every capability granted by admin", () => {
    expect(grantedCapabilitiesLabel(["repositories:admin"], text)).toBe(
      "Read + write + admin + intelligence",
    );
    expect(grantedCapabilitiesLabel(["repositories:admin"], (zh) => zh)).toBe(
      "读取 + 写入 + 管理 + 制品情报",
    );
  });

  it("combines read/write with the independent intelligence scope", () => {
    expect(
      grantedCapabilitiesLabel(
        ["repositories:read", "repositories:intelligence"],
        text,
      ),
    ).toBe("Read + Artifact intelligence");
    expect(
      grantedCapabilitiesLabel(
        ["repositories:write", "repositories:intelligence"],
        text,
      ),
    ).toBe("Read + write + Artifact intelligence");
  });

  it("keeps the least-privilege labels and the empty case", () => {
    expect(grantedCapabilitiesLabel(["repositories:read"], text)).toBe("Read");
    expect(grantedCapabilitiesLabel(["repositories:intelligence"], text)).toBe(
      "Artifact intelligence",
    );
    expect(grantedCapabilitiesLabel([], text)).toBe("");
  });
});

describe("principalEditorKind", () => {
  it("recognizes principal prefixes, the custom sentinel, and blanks", () => {
    expect(principalEditorKind("user:alice")).toBe("user");
    expect(principalEditorKind("api-key:key-1")).toBe("api-key");
    expect(principalEditorKind("service-account:sa-1")).toBe("service-account");
    expect(principalEditorKind("oidc:gitlab:team/release")).toBe("custom");
    expect(principalEditorKind("__custom__")).toBe("custom");
    expect(principalEditorKind("")).toBe("");
  });
});

describe("emptyGrant", () => {
  it("starts from a blank principal with the read scope", () => {
    const grant = emptyGrant();
    expect(grant.principal).toBe("");
    expect(grant.scopes).toEqual(["repositories:read"]);
    expect(grant.key).toBeTruthy();
  });
});

describe("grantRowKey", () => {
  it("keeps grants apart that a separator-joined key would collapse", () => {
    // Both pairs are legitimate, distinct rows: the server keys a grant by its
    // principal and resource prefix, and a prefix may contain spaces or dashes.
    expect(grantRowKey("service-account:deploy-bot", "")).not.toBe(
      grantRowKey("service-account:deploy", "bot"),
    );
    expect(grantRowKey("service-account:deploy-bot", "")).not.toBe(
      grantRowKey("service-account:deploy", "-bot"),
    );
    expect(grantRowKey("user:alice", "a b")).not.toBe(
      grantRowKey("user:alice a", "b"),
    );
  });

  it("treats a missing prefix as the repository-wide grant", () => {
    expect(grantRowKey("user:alice", undefined)).toBe(
      grantRowKey("user:alice", ""),
    );
  });

  it("keeps the same key for the same grant and distinguishes extra parts", () => {
    expect(grantRowKey("user:alice", "releases/")).toBe(
      grantRowKey("user:alice", "releases/"),
    );
    expect(grantRowKey("repo-1", "user:alice", "releases/")).not.toBe(
      grantRowKey("repo-2", "user:alice", "releases/"),
    );
  });
});
