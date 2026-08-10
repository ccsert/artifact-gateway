import { describe, expect, it } from "vitest";
import type { User } from "../../client";
import { isUserLocked, userInitials } from "./userPresentation";

const user: User = {
  id: "user-1",
  name: "alice",
  displayName: "Alice Chen",
  email: "",
  description: "",
  role: "reader",
  state: "active",
  passwordChangedAt: "2026-08-01T08:00:00Z",
  localPasswordEnabled: true,
  failedLoginAttempts: 0,
  mustChangePassword: false,
  createdAt: "2026-08-01T08:00:00Z",
  version: "1",
};

describe("user presentation", () => {
  it("treats only future lock deadlines as active locks", () => {
    expect(
      isUserLocked(
        { ...user, lockedUntil: "2026-08-10T09:00:00Z" },
        Date.parse("2026-08-10T08:00:00Z"),
      ),
    ).toBe(true);
    expect(
      isUserLocked(
        { ...user, lockedUntil: "2026-08-10T07:00:00Z" },
        Date.parse("2026-08-10T08:00:00Z"),
      ),
    ).toBe(false);
  });

  it("derives compact initials from the display name", () => {
    expect(userInitials(user)).toBe("AC");
    expect(userInitials({ ...user, displayName: "", name: "张三" })).toBe(
      "张三",
    );
  });
});
