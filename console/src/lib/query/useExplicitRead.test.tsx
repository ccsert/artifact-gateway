import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { useExplicitRead } from "./useExplicitRead";

afterEach(cleanup);

it.each(["unmount", "identity reset"])(
  "does not apply cached data from a cancelled explicit read after %s",
  async (change) => {
    const client = new QueryClient();
    const applied = vi.fn();
    let finish!: (value: string) => void;
    const read = vi
      .fn()
      .mockResolvedValueOnce("previous target")
      .mockImplementationOnce(
        () =>
          new Promise<string>((resolve) => {
            finish = resolve;
          }),
      );
    function Editor() {
      const reload = useExplicitRead(["target"], read);
      return <button onClick={() => void reload.run(applied)}>Read</button>;
    }
    const view = render(
      <QueryClientProvider client={client}>
        <Editor />
      </QueryClientProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Read" }));
    await waitFor(() => expect(applied).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole("button", { name: "Read" }));
    await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
    await act(async () => {
      if (change === "unmount") view.unmount();
      else client.removeQueries();
      await Promise.resolve();
    });
    await act(async () => {
      finish("late target");
    });
    expect(applied).toHaveBeenCalledTimes(1);
  },
);
