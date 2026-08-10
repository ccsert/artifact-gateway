import type { User } from "../../client";

export function isUserLocked(user: User, now = Date.now()): boolean {
  if (!user.lockedUntil) return false;
  const lockedUntil = Date.parse(user.lockedUntil);
  return Number.isFinite(lockedUntil) && lockedUntil > now;
}

export function roleTone(role: User["role"]): "red" | "blue" | "green" {
  if (role === "admin") return "red";
  if (role === "writer") return "blue";
  return "green";
}

export function userInitials(user: User): string {
  const value = (user.displayName || user.name).trim();
  if (!value) return "?";
  const words = value.split(/\s+/).filter(Boolean);
  if (words.length > 1) {
    return `${words[0][0]}${words[words.length - 1][0]}`.toUpperCase();
  }
  return Array.from(value).slice(0, 2).join("").toUpperCase();
}
