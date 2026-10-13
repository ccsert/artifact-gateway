import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TestQueryProvider } from "../../test/queryClient";
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

const wrapper = ({ children }: { children: ReactNode }) => (
  <TestQueryProvider>{children}</TestQueryProvider>
);

function renderResource(read: (signal: AbortSignal) => Promise<string>) {
  return renderHook(() => useDashboardResource(["source"], read), { wrapper });
}

afterEach(cleanup);

describe("dashboard request ownership", () => {
  it("a Retry cancels the read in flight and owns data, errors and loading", async () => {
    const old = deferred<string>();
    const next = deferred<string>();
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(next.promise);
    const { result } = renderResource(read);
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    let reload!: Promise<void>;
    act(() => {
      reload = result.current.reload();
    });
    await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
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
    await waitFor(() => expect(result.current.data).toBe("latest"));
    expect(result.current.loading).toBe(false);
  });

  it("a late success cannot overwrite the newer snapshot", async () => {
    const old = deferred<string>();
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce("latest");
    const { result } = renderResource(read);
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await act(async () => {
      await result.current.reload();
    });
    await act(async () => {
      old.resolve("obsolete");
    });
    await waitFor(() => expect(result.current.data).toBe("latest"));
    await act(async () => {});
    expect(result.current.data).toBe("latest");
  });

  it("retains a loaded snapshot on refresh failure and releases loading", async () => {
    const failure = new Error("synthetic refresh failure");
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string>>()
      .mockResolvedValueOnce("snapshot")
      .mockRejectedValueOnce(failure);
    const { result } = renderResource(read);
    await waitFor(() => expect(result.current.data).toBe("snapshot"));
    await act(async () => {
      await result.current.reload();
    });
    await waitFor(() => expect(result.current.error).toBe(failure));
    expect(result.current.data).toBe("snapshot");
    expect(result.current.loading).toBe(false);
  });

  it("aborts a read still in flight when the overview unmounts", async () => {
    const pending = deferred<string>();
    const read = vi
      .fn<(signal: AbortSignal) => Promise<string>>()
      .mockReturnValueOnce(pending.promise);
    const { unmount } = renderResource(read);
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    unmount();
    await waitFor(() => expect(read.mock.calls[0][0].aborted).toBe(true));
  });
});
