import type { User } from "../../client";
import type { BadgeTone } from "../../components/Badge";

export function isUserLocked(user: User, now = Date.now()): boolean {
  if (!user.lockedUntil) return false;
  const lockedUntil = Date.parse(user.lockedUntil);
  return Number.isFinite(lockedUntil) && lockedUntil > now;
}

export function roleTone(role: User["role"]): BadgeTone {
  // Roles are categories, not success or failure states. Keep them on the
  // visualization palette so operational status colors retain one meaning.
  if (role === "admin") return "visualization-3";
  if (role === "writer") return "visualization-5";
  if (role === "reader") return "visualization-4";
  return "neutral";
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
