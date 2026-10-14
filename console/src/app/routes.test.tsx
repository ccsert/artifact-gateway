import { describe, expect, it } from "vitest";
import {
  canOpen,
  consoleRedirects,
  consoleRoutes,
  navItemsFor,
  routeAccess,
} from "./routes";

const admin = { platformAdmin: true, browseRepositories: true };
const reader = { platformAdmin: false, browseRepositories: true };
const searcher = { platformAdmin: false, browseRepositories: false };

describe("console route table", () => {
  it("defines each path once and loads a page for every route", async () => {
    const paths = [
      ...consoleRoutes.map((route) => route.path),
      ...consoleRedirects.map((redirect) => redirect.from),
    ];
    expect(new Set(paths).size).toBe(paths.length);
    for (const route of consoleRoutes) {
      expect(typeof (await route.lazy()).Component, route.path).toBe(
        "function",
      );
    }
  });

  it("guards a whole section by its page's access", () => {
    expect(routeAccess("/")).toBe("admin");
    expect(routeAccess("/operations")).toBe("admin");
    expect(routeAccess("/operations/unknown")).toBe("admin");
    expect(routeAccess("/groups/g-1/browse")).toBe("admin");
    expect(routeAccess("/repositories/")).toBe("repositories");
    expect(routeAccess("/repositories/repo-1")).toBe("repositories");
    expect(routeAccess("/search")).toBe("signedIn");
    expect(routeAccess("/not-a-page")).toBeUndefined();
  });

  it("decides access from platform capabilities", () => {
    expect(canOpen("admin", reader)).toBe(false);
    expect(canOpen("repositories", reader)).toBe(true);
    expect(canOpen("repositories", searcher)).toBe(false);
    expect(canOpen("signedIn", searcher)).toBe(true);
  });

  it("shows only the pages an identity can open, in table order", () => {
    expect(navItemsFor(admin).map((item) => item.to)).toEqual([
      "/",
      "/repositories",
      "/search",
      "/operations",
      "/system",
      "/groups",
      "/access",
      "/audits",
      "/audit-retention",
      "/identity-providers",
      "/site-settings",
      "/keys",
      "/service-accounts",
      "/users",
    ]);
    expect(navItemsFor(reader).map((item) => item.to)).toEqual([
      "/repositories",
      "/search",
    ]);
    expect(navItemsFor(searcher).map((item) => item.to)).toEqual(["/search"]);
  });
});
