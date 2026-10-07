import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useDashboardResource } from "./useDashboardResource";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

afterEach(cleanup);

describe("dashboard request ownership", () => {
  it("a newer request owns data, errors and loading even if the old transport ignores abort", async () => {
    const old = deferred<string>();
    const next = deferred<string>();
    const read = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(next.promise);
    const { result } = renderHook(() => useDashboardResource(read));
    let reload!: Promise<void>;
    act(() => {
      reload = result.current.reload();
    });
    expect(read.mock.calls[0][0].aborted).toBe(true);
    await act(async () => {
      old.reject(new Error("obsolete"));
    });
    expect(result.current.error).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await act(async () => {
      next.resolve("latest");
      await reload;
    });
    expect(result.current.data).toBe("latest");
    expect(result.current.loading).toBe(false);
  });

  it("a late success cannot overwrite the newer snapshot", async () => {
    const old = deferred<string>();
    const read = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce("latest");
    const { result } = renderHook(() => useDashboardResource(read));
    await act(async () => {
      await result.current.reload();
    });
    await act(async () => {
      old.resolve("obsolete");
    });
    expect(result.current.data).toBe("latest");
  });

  it("retains a loaded snapshot on refresh failure and releases loading", async () => {
    const failure = new Error("synthetic refresh failure");
    const read = vi
      .fn()
      .mockResolvedValueOnce("snapshot")
      .mockRejectedValueOnce(failure);
    const { result } = renderHook(() => useDashboardResource(read));
    await waitFor(() => expect(result.current.data).toBe("snapshot"));
    await act(async () => {
      await result.current.reload();
    });
    expect(result.current.data).toBe("snapshot");
    expect(result.current.error).toBe(failure);
    expect(result.current.loading).toBe(false);
  });

  it("survives StrictMode effect cleanup and replay", async () => {
    const old = deferred<string>();
    const read = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce("current");
    const { result, unmount } = renderHook(() => useDashboardResource(read), {
      reactStrictMode: true,
    });
    await waitFor(() => expect(result.current.data).toBe("current"));
    expect(read.mock.calls[0][0].aborted).toBe(true);
    unmount();
    expect(read.mock.calls[1][0].aborted).toBe(true);
    await act(async () => {
      old.resolve("obsolete");
    });
  });
});
