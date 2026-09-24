import { describe, expect, it } from "vitest";
import { consolePermissions, repositoryPermissionAllowed } from "./permissions";

describe("consolePermissions", () => {
  it("grants the repository catalog to reader and writer roles", () => {
    for (const role of ["reader", "writer"] as const) {
      const permissions = consolePermissions({
        actor: "user:alice",
        kind: "local_session",
        role,
        administrator: false,
      });
      expect(permissions).toEqual({
        isAdministrator: false,
        isPending: false,
        canBrowseRepositories: true,
      });
    }
  });

  it("keeps administrators inside every surface", () => {
    expect(
      consolePermissions({
        actor: "user:root",
        kind: "local_session",
        role: "admin",
        administrator: true,
      }),
    ).toEqual({
      isAdministrator: true,
      isPending: false,
      canBrowseRepositories: true,
    });
  });

  it("treats a pending account as neither administrator nor catalog reader", () => {
    expect(
      consolePermissions({
        actor: "user:new",
        kind: "oidc",
        role: "none",
        administrator: false,
      }),
    ).toEqual({
      isAdministrator: false,
      isPending: true,
      canBrowseRepositories: false,
    });
  });

  it("reports no capability without an identity", () => {
    expect(consolePermissions(null)).toEqual({
      isAdministrator: false,
      isPending: false,
      canBrowseRepositories: false,
    });
  });

  it("rejects an identity whose role was not reported", () => {
    expect(
      consolePermissions({
        actor: "api-key:1",
        kind: "api_key",
        administrator: false,
      }).canBrowseRepositories,
    ).toBe(false);
  });
});

describe("repositoryPermissionAllowed", () => {
  const permissions = {
    read: { allowed: true },
    write: { allowed: true },
    admin: { allowed: false },
    intelligence: { allowed: true },
  };

  it("allows read without an explicit decision", () => {
    expect(repositoryPermissionAllowed(undefined, "read")).toBe(true);
  });

  it("withholds privileged requirements until a decision allows them", () => {
    expect(repositoryPermissionAllowed(undefined, "write")).toBe(false);
    expect(repositoryPermissionAllowed(undefined, "admin")).toBe(false);
    expect(repositoryPermissionAllowed(undefined, "intelligence")).toBe(false);
  });

  it("reads the decision matching each requirement", () => {
    expect(repositoryPermissionAllowed(permissions, "write")).toBe(true);
    expect(repositoryPermissionAllowed(permissions, "admin")).toBe(false);
    expect(repositoryPermissionAllowed(permissions, "intelligence")).toBe(true);
  });
});
