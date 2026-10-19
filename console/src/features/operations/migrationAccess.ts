import { useSyncExternalStore } from "react";
import {
  CancelledError,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { migrationError, migrationReadDenied } from "./migrationModel";

type Snapshot = {
  epoch: number;
  error: ReturnType<typeof migrationError> | null;
};
type Scope = {
  snapshot: Snapshot;
  listeners: Set<() => void>;
  generation?: object;
};
// IdentityCacheScope removes this non-shared marker on every token/actor change.
// Its object identity fences requests even though the QueryClient is reused.
const accessKey = ["migration-source-access-generation"] as const;
const scopes = new WeakMap<QueryClient, Scope>();
function scopeFor(client: QueryClient) {
  let scope = scopes.get(client);
  if (!scope) {
    const created: Scope = {
      snapshot: { epoch: 0, error: null },
      listeners: new Set(),
    };
    scope = created;
    scopes.set(client, scope);
    client.getQueryCache().subscribe((event) => {
      if (event.type === "removed" && event.query === created.generation) {
        created.generation = undefined;
        // Keep the epoch monotonic so forms and observations are also replaced.
        publish(created, { epoch: created.snapshot.epoch + 1, error: null });
      }
    });
  }
  scope.generation ??= client
    .getQueryCache()
    .build(client, { queryKey: accessKey, gcTime: Infinity });
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
  const generation = scope.generation;
  let value: T;
  try {
    value = await read();
  } catch (error) {
    if (generation !== scope.generation)
      throw new CancelledError({ silent: true });
    refuse(scope, error);
    throw error;
  }
  if (generation !== scope.generation || epoch !== scope.snapshot.epoch)
    throw new CancelledError({ silent: true });
  // Only a successful read started after the last refusal restores access.
  if (scope.snapshot.error) publish(scope, { epoch, error: null });
  return { epoch, value };
}
export async function migrationWrite<T>(
  client: QueryClient,
  write: () => Promise<T>,
) {
  const scope = scopeFor(client);
  const generation = scope.generation;
  const epoch = scope.snapshot.epoch;
  try {
    const value = await write();
    if (generation !== scope.generation || epoch !== scope.snapshot.epoch)
      throw new CancelledError({ silent: true });
    return value;
  } catch (error) {
    if (generation !== scope.generation)
      throw new CancelledError({ silent: true });
    refuse(scope, error);
    throw error;
  }
}
