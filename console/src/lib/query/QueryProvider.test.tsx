import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConsoleQueryProvider } from "./QueryProvider";

const auth = vi.hoisted(() => ({
  token: "token-a",
  identity: { actor: "user:alice" } as { actor: string } | null,
}));

vi.mock("../auth", () => ({ useAuth: () => auth }));

let read = vi.fn<() => Promise<string>>();

function Probe() {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["protected"], queryFn: () => read() });
  return (
    <div>
      <span data-testid="value">{query.data ?? "none"}</span>
      <span data-testid="cached">
        {String(queryClient.getQueryData(["protected"]) ?? "empty")}
      </span>
    </div>
  );
}

afterEach(() => {
  cleanup();
  auth.token = "token-a";
  auth.identity = { actor: "user:alice" };
  read = vi.fn();
});

describe("ConsoleQueryProvider", () => {
  it("drops cached answers when the signed-in principal changes", async () => {
    read.mockResolvedValue("alice's data");
    const { rerender } = render(
      <ConsoleQueryProvider>
        <Probe />
      </ConsoleQueryProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("value")).toHaveTextContent("alice's data"),
    );

    read.mockResolvedValue("bob's data");
    auth.identity = { actor: "user:bob" };
    rerender(
      <ConsoleQueryProvider>
        <Probe />
      </ConsoleQueryProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("value")).not.toHaveTextContent("alice's data"),
    );
    expect(screen.getByTestId("cached")).not.toHaveTextContent("alice's data");
  });

  it("keeps the cache while the same principal re-renders", async () => {
    read.mockResolvedValue("alice's data");
    const { rerender } = render(
      <ConsoleQueryProvider>
        <Probe />
      </ConsoleQueryProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("value")).toHaveTextContent("alice's data"),
    );
    rerender(
      <ConsoleQueryProvider>
        <Probe />
      </ConsoleQueryProvider>,
    );
    expect(screen.getByTestId("cached")).toHaveTextContent("alice's data");
    expect(read).toHaveBeenCalledTimes(1);
  });
});
