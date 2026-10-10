import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import { listRepositories, type Repository } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { CommandPalette, type CommandPage } from "./CommandPalette";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  listRepositories: vi.fn(),
}));
const list = vi.mocked(listRepositories);
const scroll = vi.fn();
const originalScroll = Object.getOwnPropertyDescriptor(
  Element.prototype,
  "scrollIntoView",
);

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
function Location() {
  return <span data-testid="location">{useLocation().pathname}</span>;
}
function renderPalette(pages: readonly CommandPage[] = []) {
  const onClose = vi.fn();
  render(
    <PreferencesProvider>
      <MemoryRouter initialEntries={["/repositories"]}>
        <Location />
        <CommandPalette
          open
          onClose={onClose}
          pages={pages}
          canBrowseRepositories
          onSignOut={vi.fn()}
        />
      </MemoryRouter>
    </PreferencesProvider>,
  );
  return { onClose, input: screen.getByRole("combobox", { name: "命令面板" }) };
}
beforeEach(() => {
  window.localStorage.clear();
  list.mockReset();
  list.mockResolvedValue({ data: { items: [] } } as never);
  scroll.mockReset();
  Object.defineProperty(Element.prototype, "scrollIntoView", {
    configurable: true,
    value: scroll,
  });
});
afterEach(() => {
  cleanup();
  if (originalScroll)
    Object.defineProperty(Element.prototype, "scrollIntoView", originalScroll);
  else Reflect.deleteProperty(Element.prototype, "scrollIntoView");
});

describe("CommandPalette recovery and keyboard boundaries", () => {
  it("finds and opens a repository beyond the first API page", async () => {
    list
      .mockResolvedValueOnce({
        data: {
          items: Array.from({ length: 100 }, (_, i) => repository(`repo-${i}`)),
          nextPageToken: "opaque-next",
        },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [repository("later-release")] },
      } as never);
    const user = userEvent.setup();
    const { input } = renderPalette();
    await user.type(input, "later-release");
    expect(
      await screen.findByRole("option", { name: /^later-release/ }),
    ).toBeInTheDocument();
    expect(list).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        query: { pageSize: 100, pageToken: "opaque-next" },
      }),
    );
    await user.keyboard("{Enter}");
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/repositories/later-release",
    );
  });

  it.each(["response", "rejection"])(
    "distinguishes %s failure from empty results and retries",
    async (failure) => {
      if (failure === "response")
        list.mockResolvedValueOnce({
          error: { message: "Repository source unavailable" },
        } as never);
      else
        list.mockRejectedValueOnce(new Error("Repository source unavailable"));
      list.mockResolvedValueOnce({
        data: { items: [repository("recovered-release")] },
      } as never);
      const user = userEvent.setup();
      const { input } = renderPalette();
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Repository source unavailable",
      );
      await user.click(screen.getByRole("button", { name: /重\s*试/ }));
      await user.type(input, "recovered-release");
      expect(
        await screen.findByRole("option", { name: /^recovered-release/ }),
      ).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(list).toHaveBeenCalledTimes(2);
    },
  );

  it("retains an earlier page when a later page fails", async () => {
    list
      .mockResolvedValueOnce({
        data: {
          items: [repository("existing-release")],
          nextPageToken: "later",
        },
      } as never)
      .mockRejectedValueOnce(new Error("Later page unavailable"));
    const user = userEvent.setup();
    const { input } = renderPalette();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Later page unavailable",
    );
    await user.type(input, "existing-release");
    expect(
      screen.getByRole("option", { name: /^existing-release/ }),
    ).toBeInTheDocument();
  });

  it("keeps keyboard selection visible beyond the first screen of options", async () => {
    const pages = Array.from({ length: 15 }, (_, i) => ({
      to: `/page-${i}`,
      label: `Page ${i}`,
      icon: null,
    }));
    const user = userEvent.setup();
    const { input } = renderPalette(pages);
    await user.click(input);
    await user.keyboard("{ArrowDown>10/}");
    const active = screen.getByRole("option", { name: /^Page 10 / });
    expect(active).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(scroll.mock.instances.at(-1)).toBe(active));
  });

  it("does not execute a command while confirming a Chinese IME candidate", async () => {
    const { input, onClose } = renderPalette([
      { to: "/system", label: "系统", icon: null },
    ]);
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: "Enter", isComposing: true, keyCode: 229 });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByTestId("location")).toHaveTextContent("/repositories");
    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.getByTestId("location")).toHaveTextContent("/system");
  });
});
