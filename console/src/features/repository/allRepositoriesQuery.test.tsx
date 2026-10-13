import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listRepositories, type Repository } from "../../client";
import { repositoryKeys } from "../../lib/query/keys";
import { useAllRepositories } from "./allRepositoriesQuery";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  listRepositories: vi.fn(),
}));

const list = vi.mocked(listRepositories);

function repository(id: string): Repository {
  return {
    id,
    name: id,
    format: "raw",
    type: "hosted",
    anonymousRead: false,
    mavenStrictPublication: false,
    state: "active",
    version: "1",
  };
}

function renderCatalog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, ...renderHook(() => useAllRepositories(true), { wrapper }) };
}

beforeEach(() => list.mockReset());
afterEach(cleanup);

describe("command palette catalog recovery", () => {
  it.each([1, 2])(
    "Retry rereads a complete %i-page catalog after invalidation fails",
    async (pageCount) => {
      list.mockResolvedValueOnce({
        data: {
          items: [repository("old-first")],
          ...(pageCount === 2 ? { nextPageToken: "old-next" } : {}),
        },
      } as never);
      if (pageCount === 2)
        list.mockResolvedValueOnce({
          data: { items: [repository("old-last")] },
        } as never);
      const { client, result } = renderCatalog();
      await waitFor(() => {
        expect(result.current.repositories).toHaveLength(pageCount);
        expect(result.current.loading).toBe(false);
      });

      const failure = new Error("Catalog refresh unavailable");
      list.mockRejectedValueOnce(failure);
      await act(async () => {
        await client.invalidateQueries({ queryKey: repositoryKeys.list() });
      });
      await waitFor(() => expect(result.current.error).toBe(failure));
      expect(result.current.repositories?.[0].id).toBe("old-first");

      list.mockResolvedValueOnce({
        data: {
          items: [repository("new-first")],
          ...(pageCount === 2 ? { nextPageToken: "new-next" } : {}),
        },
      } as never);
      if (pageCount === 2)
        list.mockResolvedValueOnce({
          data: { items: [repository("new-last")] },
        } as never);
      await act(async () => {
        await result.current.retry();
      });
      await waitFor(() =>
        expect(result.current.repositories?.[0].id).toBe("new-first"),
      );
      expect(result.current.error).toBeNull();
      expect(result.current.repositories?.map((item) => item.id)).toEqual(
        pageCount === 2 ? ["new-first", "new-last"] : ["new-first"],
      );
      expect(list).toHaveBeenCalledTimes(pageCount * 2 + 1);
      if (pageCount === 2)
        expect(list).toHaveBeenLastCalledWith(
          expect.objectContaining({
            query: { pageSize: 100, pageToken: "new-next" },
          }),
        );
    },
  );

  it("Retry resumes a failed next page and retains the preceding page", async () => {
    list
      .mockResolvedValueOnce({
        data: { items: [repository("first")], nextPageToken: "next" },
      } as never)
      .mockRejectedValueOnce(new Error("Next page unavailable"))
      .mockResolvedValueOnce({
        data: { items: [repository("last")] },
      } as never);
    const { result } = renderCatalog();
    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));
    expect(result.current.repositories?.map((item) => item.id)).toEqual([
      "first",
    ]);
    await act(async () => {
      await result.current.retry();
    });
    await waitFor(() => expect(result.current.error).toBeNull());
    expect(result.current.repositories?.map((item) => item.id)).toEqual([
      "first",
      "last",
    ]);
    expect(list).toHaveBeenCalledTimes(3);
    expect(list).toHaveBeenLastCalledWith(
      expect.objectContaining({ query: { pageSize: 100, pageToken: "next" } }),
    );
  });
});
