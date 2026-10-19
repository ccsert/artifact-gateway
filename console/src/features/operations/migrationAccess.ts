import { useSyncExternalStore } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { migrationError, migrationReadDenied } from "./migrationModel";

type Snapshot = {
  epoch: number;
  error: ReturnType<typeof migrationError> | null;
};
type Scope = {
  snapshot: Snapshot;
  listeners: Set<() => void>;
};
// Source access belongs to this identity's QueryClient, not a particular view.
// A remount, a cache write or a late response cannot undo a refusal.
const scopes = new WeakMap<QueryClient, Scope>();
function scopeFor(client: QueryClient) {
  let scope = scopes.get(client);
  if (!scope) {
    scope = { snapshot: { epoch: 0, error: null }, listeners: new Set() };
    scopes.set(client, scope);
  }
  return scope;
}
function publish(scope: Scope, snapshot: Snapshot) {
  scope.snapshot = snapshot;
  scope.listeners.forEach((listener) => listener());
}
function refuse(scope: Scope, error: unknown) {
  if (migrationReadDenied(error))
    publish(scope, {
      epoch: scope.snapshot.epoch + 1,
      error: migrationError(error),
    });
}
export function useMigrationAccess() {
  const client = useQueryClient();
  const scope = scopeFor(client);
  return useSyncExternalStore(
    (listener) => {
      scope.listeners.add(listener);
      return () => scope.listeners.delete(listener);
    },
    () => scope.snapshot,
  );
}
export async function migrationReadSnapshot<T>(
  client: QueryClient,
  read: () => Promise<T>,
) {
  const scope = scopeFor(client);
  const epoch = scope.snapshot.epoch;
  let value: T;
  try {
    value = await read();
  } catch (error) {
    refuse(scope, error);
    throw error;
  }
  if (epoch !== scope.snapshot.epoch) throw migrationError({ status: 403 });
  // Only a successful read started after the last refusal restores access.
  if (scope.snapshot.error) publish(scope, { epoch, error: null });
  return { epoch, value };
}
export async function migrationWrite<T>(
  client: QueryClient,
  write: () => Promise<T>,
) {
  try {
    return await write();
  } catch (error) {
    refuse(scopeFor(client), error);
    throw error;
  }
}
