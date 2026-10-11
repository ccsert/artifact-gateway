import { useQuery } from "@tanstack/react-query";
import { getDiagnostics, listRuntimeNodes } from "../../client";
import type { Problem, RuntimeNode, RuntimeNodeList } from "../../client";
import { unwrap } from "../../lib/query/client";
import { systemKeys } from "../../lib/query/keys";

const AUTHORIZATION_CODES = [
  "unauthorized",
  "forbidden",
  "password_change_required",
];

/** A refusal for the signed-in principal rather than a failed refresh. */
export function isAuthorizationFailure(error: unknown): boolean {
  const problem = error as Problem | null | undefined;
  return (
    problem?.status === 401 ||
    problem?.status === 403 ||
    AUTHORIZATION_CODES.includes(problem?.code ?? "")
  );
}

/** The diagnostic snapshot; it changes on the server, so it is always re-read. */
export function useDiagnostics() {
  return useQuery({
    queryKey: systemKeys.diagnostics(),
    queryFn: async ({ signal }) => {
      const result = await getDiagnostics({ signal });
      const status = result.response?.status;
      // A refusal can arrive with an empty or plain-text body; give it a
      // Problem shape so the banner and the auth handling read its status.
      const body: unknown = result.error;
      if (
        (status === 401 || status === 403) &&
        (typeof body !== "object" || body === null)
      ) {
        const message = typeof body === "string" ? body.trim() : "";
        throw {
          status,
          code: status === 401 ? "unauthorized" : "forbidden",
          ...(message ? { message } : {}),
        };
      }
      return unwrap(Promise.resolve(result));
    },
    staleTime: 0,
  });
}

function normalizeRuntimeNode(node: RuntimeNode): RuntimeNode {
  const raw = node as RuntimeNode & {
    instanceId?: string | null;
    sessionId?: string | null;
    roles?: string[] | null;
    workerFormats?: string[] | null;
    workerKinds?: string[] | null;
  };
  return {
    ...node,
    instanceId: raw.instanceId ?? "unknown",
    sessionId: raw.sessionId ?? raw.instanceId ?? "unknown",
    roles: raw.roles ?? [],
    workerFormats: raw.workerFormats ?? [],
    workerKinds: raw.workerKinds ?? [],
  };
}

/** Older nodes may omit lists and identifiers; fill them before rendering. */
function normalizeRuntimeNodes(list: RuntimeNodeList) {
  return {
    nodes: (list?.items ?? []).map(normalizeRuntimeNode),
    currentSessionId: list?.currentSessionId ?? null,
    health: list?.health
      ? { ...list.health, issues: list.health.issues ?? [] }
      : null,
  };
}

/**
 * Runtime nodes, polled while the panel is on screen. A failed poll keeps
 * the last answer, so the table does not empty between heartbeats.
 */
export function useRuntimeNodes(pollIntervalMs: number) {
  return useQuery({
    queryKey: systemKeys.runtimeNodes(),
    queryFn: ({ signal }) => unwrap(listRuntimeNodes({ signal })),
    select: normalizeRuntimeNodes,
    refetchInterval: pollIntervalMs,
    staleTime: 0,
  });
}
