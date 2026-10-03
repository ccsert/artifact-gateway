import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { listRuntimeLogs } from "../../client";
import { useRuntimeLogStream, type LogQuery } from "./useRuntimeLogStream";

vi.mock("../../client", () => ({ listRuntimeLogs: vi.fn() }));
const api = vi.mocked(listRuntimeLogs);
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});
it("keeps a duration-bound cursor after days of pause, and resets it when the window changes", async () => {
  api.mockResolvedValue({
    data: {
      scope: "local",
      instanceId: "node",
      sessionId: "session",
      items: [],
      afterCursor: "cursor-24h",
    },
  } as never);
  const initial: LogQuery = { windowSeconds: 86400 };
  const { result, rerender } = renderHook(
    ({ query }) => useRuntimeLogStream(query),
    { initialProps: { query: initial } },
  );
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(async () => result.current.follow());
  expect(api.mock.calls.at(-1)?.[0]?.query).toMatchObject({
    windowSeconds: 86400,
    afterCursor: "cursor-24h",
  });
  result.current.pause();
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-06T14:00:00Z"));
  await act(async () => result.current.follow());
  const resumed = api.mock.calls.at(-1)?.[0]?.query;
  expect(resumed?.windowSeconds).toBe(86400);
  expect(resumed?.afterCursor).toBe("cursor-24h");
  expect(resumed?.from).toBeUndefined();
  expect(resumed?.to).toBeUndefined();
  await act(async () => rerender({ query: { windowSeconds: 900 } }));
  expect(api.mock.calls.at(-1)?.[0]?.query).toMatchObject({
    windowSeconds: 900,
    afterCursor: undefined,
    beforeSequence: undefined,
  });
  expect(result.current.following).toBe(false);
});
