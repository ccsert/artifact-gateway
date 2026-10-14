import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TestQueryProvider } from "../../test/queryClient";
import { useQuotaSnapshot } from "./useQuotaSnapshot";

function deferred<T>() {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("useQuotaSnapshot", () => {
  it("keeps a local write when an older read finishes after it", async () => {
    const pending = deferred<string[]>();
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string[]>>()
      .mockResolvedValueOnce(["first"])
      .mockImplementationOnce(() => pending.promise);
    const { result } = renderHook(() => useQuotaSnapshot(read, "rules"), {
      wrapper: TestQueryProvider,
    });
    await waitFor(() => expect(result.current.data).toEqual(["first"]));
    const readAt = result.current.readAt;

    let refreshed: Promise<boolean> | undefined;
    act(() => {
      refreshed = result.current.refresh();
    });
    act(() => result.current.update((old) => [...old, "written"]));
    pending.resolve(["stale"]);

    // The overtaken refresh reports failure, so it cannot lift a block.
    await expect(refreshed).resolves.toBe(false);
    // Query results reach the component a scheduler tick later.
    await waitFor(() =>
      expect(result.current.data).toEqual(["first", "written"]),
    );
    expect(read).toHaveBeenCalledTimes(2);
    // A local write is not a read.
    expect(result.current.readAt).toBe(readAt);
  });

  it("reports a denial to the panel and retains the last data", async () => {
    const onAuthBlocked = vi.fn();
    const denial = { code: "password_change_required" };
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string[]>>()
      .mockResolvedValueOnce(["first"])
      .mockRejectedValueOnce(denial);
    const { result } = renderHook(
      () => useQuotaSnapshot(read, "rules", onAuthBlocked),
      { wrapper: TestQueryProvider },
    );
    await waitFor(() => expect(result.current.data).toEqual(["first"]));

    let refreshed = true;
    await act(async () => {
      refreshed = await result.current.refresh();
    });
    expect(refreshed).toBe(false);
    expect(onAuthBlocked).toHaveBeenCalledWith(denial);
    await waitFor(() => expect(result.current.error).toBe(denial));
    expect(result.current.data).toEqual(["first"]);
  });
});
