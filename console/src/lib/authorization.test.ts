import { describe, expect, it } from "vitest";
import type { CurrentIdentity, RepositoryEffectiveAccess } from "../client";
import { platformCapabilities, repositoryPermissions } from "./authorization";

function identity(overrides: Partial<CurrentIdentity>): CurrentIdentity {
  return {
    actor: "user:member",
    kind: "local_session",
    administrator: false,
    ...overrides,
  };
}

describe("platform capabilities", () => {
  it("gives the platform administrator every platform surface", () => {
    expect(
      platformCapabilities(
        identity({ actor: "user:root", role: "admin", administrator: true }),
      ),
    ).toEqual({
      pending: false,
      platformAdmin: true,
      browseRepositories: true,
    });
  });

  it("lets a member browse repositories without any platform surface", () => {
    expect(platformCapabilities(identity({ role: "member" }))).toEqual({
      pending: false,
      platformAdmin: false,
      browseRepositories: true,
    });
  });

  it("treats a none level as pending with nothing reachable", () => {
    expect(platformCapabilities(identity({ role: "none" }))).toEqual({
      pending: true,
      platformAdmin: false,
      browseRepositories: false,
    });
  });

  it("reaches nothing when no identity is loaded", () => {
    for (const value of [null, undefined]) {
      expect(platformCapabilities(value)).toEqual({
        pending: false,
        platformAdmin: false,
        browseRepositories: false,
      });
    }
  });

  it("does not treat a missing role as a member", () => {
    // A service account and a static resolver carry no level at all.
    expect(platformCapabilities(identity({ administrator: false }))).toEqual({
      pending: false,
      platformAdmin: false,
      browseRepositories: false,
    });
  });
});

describe("repository permissions", () => {
  const access = (
    permissions: Partial<RepositoryEffectiveAccess["permissions"]>,
  ) => ({ permissions }) as RepositoryEffectiveAccess;

  it("reads each decision on its own", () => {
    const decision = {
      allowed: true,
      source: "repository_grants",
      reason: "scope_granted",
    };
    const denied = {
      allowed: false,
      source: "repository_grants",
      reason: "scope_not_granted",
    };
    expect(
      repositoryPermissions(
        access({
          read: decision,
          write: denied,
          admin: denied,
          intelligence: decision,
        }),
      ),
    ).toEqual({
      read: true,
      write: false,
      administer: false,
      intelligence: true,
    });
  });

  it("denies everything before the access answer arrives", () => {
    for (const value of [null, undefined]) {
      expect(repositoryPermissions(value)).toEqual({
        read: false,
        write: false,
        administer: false,
        intelligence: false,
      });
    }
  });

  it("does not infer a broader permission from a narrower one", () => {
    const granted = {
      allowed: true,
      source: "repository_grants",
      reason: "scope_granted",
    };
    const denied = {
      allowed: false,
      source: "repository_grants",
      reason: "scope_not_granted",
    };
    const permissions = repositoryPermissions(
      access({
        read: denied,
        write: granted,
        admin: denied,
        intelligence: denied,
      }),
    );
    // The server already folds write into read; the Console must not re-derive it.
    expect(permissions).toEqual({
      read: false,
      write: true,
      administer: false,
      intelligence: false,
    });
  });
});
