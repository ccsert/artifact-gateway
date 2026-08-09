import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { PreferencesProvider } from "../lib/preferences";
import { PreferenceControls } from "./PreferenceControls";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("PreferenceControls", () => {
  it("opens the language menu without showing a tooltip", async () => {
    const user = userEvent.setup();
    render(
      <PreferencesProvider>
        <PreferenceControls />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: "语言" }));

    expect(
      await screen.findByRole("menuitem", { name: "中文" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "English" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await user.click(screen.getByRole("menuitem", { name: "English" }));

    expect(screen.getByRole("button", { name: "Language" })).toHaveTextContent(
      "EN",
    );
    expect(localStorage.getItem("ag.console.locale")).toBe("en-US");
  });
});
