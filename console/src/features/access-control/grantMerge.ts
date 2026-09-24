import type { Grant } from "../../client";

/**
 * Pure read-merge-write helpers for the repository grant editors.
 *
 * `replaceGrants` replaces a repository's entire grant set, so any editor that
 * touches a single principal must merge its change into the full current list
 * instead of appending or building its own list. Keeping that merge here makes
 * it testable without rendering a component and keeps the write semantics in
 * one place.
 *
 * The unit of change is one entry: a principal may legitimately hold several
 * grants on the same repository, one per resource prefix (the repository
 * editor's duplicate key is the principal and the prefix). `upsertGrant` and
 * `replaceGrantEntry` therefore touch exactly one entry, and only
 * `removePrincipalGrants` is bulk.
 */

/** The part of a grant one entry's change carries. */
export type GrantChange = Pick<Grant, "scopes" | "resourcePrefix">;

/**
 * Normalize a resource prefix exactly like the repository grants editor does
 * before submitting: surrounding whitespace is dropped and a blank prefix
 * becomes `undefined`, which the API reads as repository-wide access.
 */
export function normalizeResourcePrefix(
  prefix: string | undefined,
): string | undefined {
  return prefix?.trim() || undefined;
}

/** Whether `grant` is the entry of `principal` under `prefix`. */
function isEntry(
  grant: Grant,
  principal: string,
  prefix: string | undefined,
): boolean {
  return (
    grant.principal.trim() === principal &&
    normalizeResourcePrefix(grant.resourcePrefix) === prefix
  );
}

/**
 * Add `change` as an entry of `principal`, or replace the entry already stored
 * under the change's own (trimmed principal, normalized resource prefix).
 *
 * - The entry is the unit of change: the same principal's entries under other
 *   prefixes are returned untouched, in their original order and with their
 *   original object identity, and a new entry is appended at the end.
 * - A blank or whitespace-only prefix means repository-wide access, so it
 *   matches (and replaces) an existing repository-wide entry of the principal.
 * - A replaced entry keeps its position in the list, and the written entry
 *   always carries the trimmed principal and the normalized prefix.
 * - The function never mutates `grants` or `change`, and copies the scopes
 *   array instead of sharing it.
 */
export function upsertGrant(
  grants: readonly Grant[],
  principal: string,
  change: GrantChange,
): Grant[] {
  const normalizedPrincipal = principal.trim();
  const prefix = normalizeResourcePrefix(change.resourcePrefix);
  const merged: Grant = {
    principal: normalizedPrincipal,
    scopes: [...change.scopes],
    resourcePrefix: prefix,
  };
  const index = grants.findIndex((grant) =>
    isEntry(grant, normalizedPrincipal, prefix),
  );
  if (index < 0) return [...grants, merged];
  return grants.map((grant, position) => (position === index ? merged : grant));
}

/**
 * Replace the entry an edit acted on: the entry of `principal` stored under
 * `editedPrefix` (the empty string means repository-wide) makes way for
 * `change`.
 *
 * The per-user panel opens its editor from one specific row and keeps that
 * row's prefix as the target while the whole draft, including its prefix, is
 * edited. An edit that changes the prefix therefore moves the acted-on entry
 * instead of leaving the old entry behind and adding a second one; the entry
 * keeps its position in the list, and another entry already stored under the
 * new prefix is dropped so one (principal, prefix) pair cannot appear twice.
 * If the acted-on entry has disappeared meanwhile, the change is simply
 * upserted.
 *
 * Never mutates `grants` or `change`.
 */
export function replaceGrantEntry(
  grants: readonly Grant[],
  principal: string,
  editedPrefix: string,
  change: GrantChange,
): Grant[] {
  const normalizedPrincipal = principal.trim();
  const previous = normalizeResourcePrefix(editedPrefix);
  const position = grants.findIndex((grant) =>
    isEntry(grant, normalizedPrincipal, previous),
  );
  if (position < 0) return upsertGrant(grants, normalizedPrincipal, change);
  const prefix = normalizeResourcePrefix(change.resourcePrefix);
  const merged: Grant = {
    principal: normalizedPrincipal,
    scopes: [...change.scopes],
    resourcePrefix: prefix,
  };
  return grants
    .map((grant, index) => (index === position ? merged : grant))
    .filter(
      (grant, index) =>
        index === position || !isEntry(grant, normalizedPrincipal, prefix),
    );
}

/**
 * Bulk removal: drop every entry of `principal`, whatever resource prefix it
 * is scoped to, and keep every other entry untouched (order and identity).
 *
 * This is deliberately the only bulk operation. It backs the per-user panel's
 * "remove this user's access to this repository" action, which takes the whole
 * account off the repository and must stay visibly different from the
 * entry-scoped upsert. Never mutates `grants`.
 */
export function removePrincipalGrants(
  grants: readonly Grant[],
  principal: string,
): Grant[] {
  const normalizedPrincipal = principal.trim();
  return grants.filter(
    (grant) => grant.principal.trim() !== normalizedPrincipal,
  );
}
