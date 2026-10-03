import { useCallback, useEffect, useRef, useState } from "react";
import { listRuntimeLogs } from "../../client";
import type {
  ListRuntimeLogsData,
  RuntimeLogEntry,
  RuntimeLogPage,
} from "../../client";
import { boundRuntimeLogs } from "./runtimeLogView";

export type LogQuery = NonNullable<ListRuntimeLogsData["query"]>;
type LoadKind = "snapshot" | "older" | "follow";

export function useRuntimeLogStream(query: LogQuery) {
  const [page, setPage] = useState<RuntimeLogPage | null>(null);
  const [entries, setEntries] = useState<RuntimeLogEntry[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [following, setFollowing] = useState(false);
  const [visible, setVisible] = useState(!document.hidden);
  const [gap, setGap] = useState(false);
  const [scopeChanged, setScopeChanged] = useState(false);
  const [trimmed, setTrimmed] = useState(0);
  const [unread, setUnread] = useState(0);
  const [bytes, setBytes] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [hasOlder, setHasOlder] = useState(false);
  const [resetVersion, setResetVersion] = useState(0);
  const pageRef = useRef<RuntimeLogPage | null>(null);
  const entriesRef = useRef<RuntimeLogEntry[]>([]);
  const liveCursor = useRef<string | undefined>(undefined);
  const historyCursor = useRef<number | undefined>(undefined);
  const busy = useRef(false);
  const sequence = useRef(0);
  const active = useRef<{ kind: LoadKind; controller: AbortController } | null>(
    null,
  );
  const viewport = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const scrollIntent = useRef<"bottom" | number | null>(null);

  const cancel = useCallback(() => {
    sequence.current++;
    active.current?.controller.abort();
    active.current = null;
    busy.current = false;
    setLoading(false);
  }, []);

  const clear = useCallback(() => {
    pageRef.current = null;
    entriesRef.current = [];
    liveCursor.current = undefined;
    historyCursor.current = undefined;
    setPage(null);
    setEntries([]);
    setBytes(0);
    setGap(false);
    setHasMore(false);
    setHasOlder(false);
    setUnread(0);
    setTrimmed(0);
    setResetVersion((value) => value + 1);
  }, []);

  const load = useCallback(
    async (kind: LoadKind) => {
      if (busy.current || document.hidden) return;
      if (kind === "follow" && !liveCursor.current) return;
      const ticket = ++sequence.current;
      const controller = new AbortController();
      active.current = { kind, controller };
      busy.current = true;
      setLoading(true);
      setError(null);
      const timeout = setTimeout(() => controller.abort("timeout"), 5000);
      try {
        const result = await listRuntimeLogs({
          query: {
            ...query,
            beforeSequence:
              kind === "older" ? historyCursor.current : undefined,
            afterCursor: kind === "follow" ? liveCursor.current : undefined,
            limit: kind === "follow" ? 100 : 50,
          },
          signal: controller.signal,
        });
        if (ticket !== sequence.current) return;
        if (controller.signal.aborted) {
          if (controller.signal.reason === "timeout")
            throw new Error("Runtime log request timed out");
          return;
        }
        if (result.error) throw result.error;
        const next = result.data;
        if (!next) throw new Error("Runtime log response is missing");
        const previous = pageRef.current;
        const changedScope = Boolean(
          previous &&
          (previous.instanceId !== next.instanceId ||
            previous.sessionId !== next.sessionId),
        );
        if (kind !== "snapshot" && changedScope) {
          setFollowing(false);
          clear();
          setScopeChanged(true);
          setError({
            code: "log_cursor_scope_changed",
            message: "Runtime log source changed; refresh the snapshot.",
          });
          return;
        }
        const current = kind === "snapshot" ? [] : entriesRef.current;
        const keys = new Set(current.map((row) => row.sequence));
        const added = new Set(
          next.items
            .filter(
              (row) =>
                row.instanceId === next.instanceId &&
                row.sessionId === next.sessionId &&
                !keys.has(row.sequence),
            )
            .map((row) => row.sequence),
        ).size;
        const bounded = boundRuntimeLogs(
          [...current, ...next.items],
          next.instanceId,
          next.sessionId,
        );
        if (kind === "older" && viewport.current)
          scrollIntent.current =
            viewport.current.scrollHeight - viewport.current.scrollTop;
        else if (kind === "snapshot" || atBottom.current)
          scrollIntent.current = "bottom";
        if (kind !== "older") {
          liveCursor.current = next.afterCursor;
          setHasMore(Boolean(next.hasMore));
        }
        if (kind !== "follow") {
          historyCursor.current = next.nextSequence;
          setHasOlder(Boolean(next.nextSequence));
        }
        pageRef.current = next;
        entriesRef.current = bounded.entries;
        setPage(next);
        setEntries(bounded.entries);
        setBytes(bounded.bytes);
        setTrimmed((value) => value + bounded.dropped);
        if (kind === "follow" && !atBottom.current)
          setUnread((value) => value + added);
        if (kind === "snapshot") {
          setResetVersion((value) => value + 1);
          setGap(false);
          setScopeChanged(changedScope);
          setTrimmed(bounded.dropped);
          setUnread(0);
        }
        if (next.retention?.gap) setGap(true);
      } catch (nextError) {
        if (ticket !== sequence.current) return;
        if (controller.signal.aborted && controller.signal.reason !== "timeout")
          return;
        const problem =
          nextError && typeof nextError === "object"
            ? (nextError as { code?: string; status?: number })
            : null;
        if (problem?.code === "log_cursor_scope_changed") {
          clear();
          setScopeChanged(true);
          setFollowing(false);
        }
        if (
          problem?.status === 401 ||
          problem?.status === 403 ||
          problem?.code === "access_denied" ||
          problem?.code === "password_change_required"
        ) {
          clear();
          setFollowing(false);
        }
        setError(
          controller.signal.reason === "timeout"
            ? new Error("Runtime log request timed out")
            : nextError,
        );
      } finally {
        clearTimeout(timeout);
        if (ticket === sequence.current) {
          busy.current = false;
          active.current = null;
          setLoading(false);
        }
      }
    },
    [query, clear],
  );

  useEffect(() => {
    cancel();
    clear();
    setScopeChanged(false);
    setError(null);
    atBottom.current = true;
    void load("snapshot");
    return cancel;
  }, [load, cancel, clear]);

  useEffect(() => {
    const changed = () => {
      setVisible(!document.hidden);
      if (document.hidden) cancel();
      else if (!pageRef.current) void load("snapshot");
    };
    document.addEventListener("visibilitychange", changed);
    return () => document.removeEventListener("visibilitychange", changed);
  }, [cancel, load]);

  useEffect(() => {
    if (!following || !visible || loading || !liveCursor.current) return;
    const timer = setTimeout(() => void load("follow"), hasMore ? 1000 : 3000);
    return () => clearTimeout(timer);
  }, [following, visible, loading, hasMore, load, page]);

  useEffect(() => {
    const node = viewport.current;
    if (!node || scrollIntent.current === null) return;
    node.scrollTop =
      scrollIntent.current === "bottom"
        ? node.scrollHeight
        : node.scrollHeight - scrollIntent.current;
    scrollIntent.current = null;
  }, [entries]);

  const pause = () => {
    setFollowing(false);
    if (active.current?.kind === "follow") cancel();
  };
  const follow = () => {
    setFollowing(true);
    void load("follow");
  };
  const resumeBottom = () => {
    atBottom.current = true;
    setUnread(0);
    if (viewport.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
    follow();
  };
  const scrolled = () => {
    const node = viewport.current;
    if (!node) return;
    atBottom.current =
      node.scrollHeight - node.scrollTop - node.clientHeight <= 32;
    if (atBottom.current) setUnread(0);
  };
  const refresh = () => {
    pause();
    cancel();
    void load("snapshot");
  };

  return {
    page,
    entries,
    error,
    loading,
    following,
    visible,
    gap,
    scopeChanged,
    trimmed,
    unread,
    bytes,
    viewport,
    follow,
    pause,
    resumeBottom,
    scrolled,
    refresh,
    loadOlder: () => void load("older"),
    hasOlder,
    resetVersion,
  };
}
